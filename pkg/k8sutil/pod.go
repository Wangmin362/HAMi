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

package k8sutil

import (
	"github.com/Project-HAMi/HAMi/pkg/device"
	"github.com/Project-HAMi/HAMi/pkg/util"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/klog/v2"
)

// Resourcereqs 解析出当前Pod每个容器申请的计算资源的大小：主要包括：数量，内存，算力
func Resourcereqs(pod *corev1.Pod) (counts util.PodDeviceRequests) {
	counts = make(util.PodDeviceRequests, len(pod.Spec.Containers))
	//Count Nvidia GPU
	for i := 0; i < len(pod.Spec.Containers); i++ {
		// 获取hami当前所有支持的设备
		devices := device.GetDevices()
		counts[i] = make(util.ContainerDeviceRequests)
		for idx, val := range devices { // 遍历hami支持的每一种类型的设备，看看当前容器申请了几个这样的设备
			// 获取当前容器对于当前类型的计算资源申请的大小，主要包括：数量，内存，算力
			request := val.GenerateResourceRequests(&pod.Spec.Containers[i])
			if request.Nums > 0 { // 数量大于零，说明当前容器申请了这个类型的设备
				// TODO 这里为什么不直接使用上面计算出来的值？
				counts[i][idx] = val.GenerateResourceRequests(&pod.Spec.Containers[i])
			}
		}
	}
	klog.InfoS("collect requestreqs", "counts", counts)
	return counts
}

func IsPodInTerminatedState(pod *corev1.Pod) bool {
	return pod.Status.Phase == corev1.PodFailed || pod.Status.Phase == corev1.PodSucceeded
}

func AllContainersCreated(pod *corev1.Pod) bool {
	return len(pod.Status.ContainerStatuses) >= len(pod.Spec.Containers)
}
