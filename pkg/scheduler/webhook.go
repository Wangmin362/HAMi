/*
Copyright 2024 The HAMi Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package scheduler

import (
	"context"
	"encoding/json"
	"net/http"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/klog/v2"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"github.com/Project-HAMi/HAMi/pkg/device"
	"github.com/Project-HAMi/HAMi/pkg/scheduler/config"
)

const template = "Processing admission hook for pod %v/%v, UID: %v"

type webhook struct {
	decoder *admission.Decoder
}

func NewWebHook() (*admission.Webhook, error) {
	logf.SetLogger(klog.NewKlogr())
	schema := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(schema); err != nil {
		return nil, err
	}
	// schema中维护了goType -> GVK以及GVK -> goType的映射关系
	decoder := admission.NewDecoder(schema)
	wh := &admission.Webhook{Handler: &webhook{decoder: decoder}}
	return wh, nil
}

func (h *webhook) Handle(_ context.Context, req admission.Request) admission.Response {
	pod := &corev1.Pod{}
	// 可以理解为反序列化获取请求参数中的Pod
	err := h.decoder.Decode(req, pod)
	if err != nil {
		klog.Errorf("Failed to decode request: %v", err)
		return admission.Errored(http.StatusBadRequest, err)
	}
	// 如果当前Pod没有任何容器，那么显然Pod必须要被调度，直接返回
	if len(pod.Spec.Containers) == 0 {
		klog.Warningf(template+" - Denying admission as pod has no containers", req.Namespace, req.Name, req.UID)
		return admission.Denied("pod has no containers")
	}
	klog.Infof(template, req.Namespace, req.Name, req.UID)
	hasResource := false
	// 遍历当前Pod的每个容器，看看容器是否申请了HAMi管理的gpu资源，如果有，那么需要修改这个Pod的调度器为hami-scheduler
	for idx, ctr := range pod.Spec.Containers {
		c := &pod.Spec.Containers[idx]
		if ctr.SecurityContext != nil {
			// 1. 如果当前容器是特权容器，那么特权容器本身就可以访问到宿主上所有的资源，这里做限制没有意义，直接忽略这种类型的容器
			// 2. 因为开启特权模式之后，Pod 可以访问宿主机上的所有设备，再做限制也没意义了，因此这里直接忽略。
			if ctr.SecurityContext.Privileged != nil && *ctr.SecurityContext.Privileged {
				klog.Warningf(template+" - Denying admission as container %s is privileged", req.Namespace, req.Name, req.UID, c.Name)
				continue
			}
		}
		// 遍历当前HAMi管理的所有类型的设备，看看当前容器是否申请了对应类型的设备
		for _, val := range device.GetDevices() {
			// 判断当前容器是否申请了对应类型的gpu设备
			found, err := val.MutateAdmission(c, pod)
			if err != nil {
				klog.Errorf("validating pod failed:%s", err.Error())
				return admission.Errored(http.StatusInternalServerError, err)
			}
			hasResource = hasResource || found
		}
		// TODO 这里可以优化一下，但凡hasResource=true，可以直接跳出循环，因为只要有一个容器申请了HAMi管理的设备，那么这个Pod就需要被hami-scheduler调度
	}

	if !hasResource { // 说明当前Pod没有任何容器申请了HAMi管理的设备，因此直接让默认调度器进行调度
		klog.Infof(template+" - Allowing admission for pod: no resource found", req.Namespace, req.Name, req.UID)
		//return admission.Allowed("no resource found")
	} else if len(config.SchedulerName) > 0 {
		// 否则，直接把Pod的调度器修改为hami-scheduler
		pod.Spec.SchedulerName = config.SchedulerName

		// 对于Pod已经指定了调度节点的Pod，注解忽略这种类型的Pod，因为用户在创建的时候就希望自己指定调度节点
		if pod.Spec.NodeName != "" {
			klog.Infof(template+" - Pod already has node assigned", req.Namespace, req.Name, req.UID)
			return admission.Denied("pod has node assigned")
		}
	}
	marshaledPod, err := json.Marshal(pod)
	if err != nil {
		klog.Errorf(template+" - Failed to marshal pod, error: %v", req.Namespace, req.Name, req.UID, err)
		return admission.Errored(http.StatusInternalServerError, err)
	}
	// 如果Pod中的容器有任何一个申请了HAMi管理的设备，那么就需要修改Pod的调度器为hami-scheduler
	return admission.PatchResponseFromRaw(req.Object.Raw, marshaledPod)
}
