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
	"fmt"
	"maps"
	"sort"
	"strconv"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	listerscorev1 "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/record"
	"k8s.io/klog/v2"
	extenderv1 "k8s.io/kube-scheduler/extender/v1"

	"github.com/Project-HAMi/HAMi/pkg/device"
	"github.com/Project-HAMi/HAMi/pkg/k8sutil"
	"github.com/Project-HAMi/HAMi/pkg/scheduler/config"
	"github.com/Project-HAMi/HAMi/pkg/scheduler/policy"
	"github.com/Project-HAMi/HAMi/pkg/util"
	"github.com/Project-HAMi/HAMi/pkg/util/client"
)

type Scheduler struct {
	*nodeManager
	*podManager

	stopCh     chan struct{}
	kubeClient kubernetes.Interface
	podLister  listerscorev1.PodLister
	nodeLister listerscorev1.NodeLister
	//Node status returned by filter
	// TODO 和overviewstatus有啥区别？
	cachedstatus map[string]*NodeUsage
	nodeNotify   chan struct{}
	//Node Overview
	overviewstatus map[string]*NodeUsage

	eventRecorder record.EventRecorder
}

func NewScheduler() *Scheduler {
	klog.InfoS("Initializing HAMi scheduler")
	s := &Scheduler{
		stopCh:       make(chan struct{}),
		cachedstatus: make(map[string]*NodeUsage),
		nodeNotify:   make(chan struct{}, 1),
	}
	// 与其说是一个nodeManager, 其实倒不如说是个nodeCache， 这个nodeCache主要是用来保存节点信息的
	s.nodeManager = newNodeManager()
	// pod缓存
	s.podManager = newPodManager()
	klog.V(2).InfoS("Scheduler initialized successfully")
	return s
}

func (s *Scheduler) doNodeNotify() {
	select {
	case s.nodeNotify <- struct{}{}:
	default:
	}
}

func (s *Scheduler) onAddPod(obj any) {
	pod, ok := obj.(*corev1.Pod)
	if !ok {
		klog.ErrorS(fmt.Errorf("invalid pod object"), "Failed to process pod addition")
		return
	}
	klog.V(5).InfoS("Pod added", "pod", pod.Name, "namespace", pod.Namespace)
	nodeID, ok := pod.Annotations[util.AssignedNodeAnnotations]
	if !ok {
		return
	}
	if k8sutil.IsPodInTerminatedState(pod) {
		s.delPod(pod)
		return
	}
	podDev, _ := util.DecodePodDevices(util.SupportDevices, pod.Annotations)
	s.addPod(pod, nodeID, podDev)
}

func (s *Scheduler) onUpdatePod(_, newObj any) {
	s.onAddPod(newObj)
}

func (s *Scheduler) onDelPod(obj any) {
	pod, ok := obj.(*corev1.Pod)
	if !ok {
		klog.Errorf("unknown add object type")
		return
	}
	_, ok = pod.Annotations[util.AssignedNodeAnnotations]
	if !ok {
		return
	}
	s.delPod(pod)
}

func (s *Scheduler) Start() {
	klog.InfoS("Starting HAMi scheduler components")
	s.kubeClient = client.GetClient()
	informerFactory := informers.NewSharedInformerFactoryWithOptions(s.kubeClient, time.Hour*1)
	s.podLister = informerFactory.Core().V1().Pods().Lister()
	s.nodeLister = informerFactory.Core().V1().Nodes().Lister()

	// 监听Pod的变化
	informerFactory.Core().V1().Pods().Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    s.onAddPod,
		UpdateFunc: s.onUpdatePod,
		DeleteFunc: s.onDelPod,
	})
	// 监听Node的变化
	informerFactory.Core().V1().Nodes().Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    func(_ any) { s.doNodeNotify() },
		UpdateFunc: func(_, _ any) { s.doNodeNotify() },
		DeleteFunc: func(_ any) { s.doNodeNotify() },
	})
	informerFactory.Start(s.stopCh)
	informerFactory.WaitForCacheSync(s.stopCh)
	s.addAllEventHandlers()
}

func (s *Scheduler) Stop() {
	close(s.stopCh)
}

func (s *Scheduler) RegisterFromNodeAnnotations() {
	klog.InfoS("Entering RegisterFromNodeAnnotations")
	defer klog.InfoS("Exiting RegisterFromNodeAnnotations")

	labelSelector := labels.Set(config.NodeLabelSelector).AsSelector()
	klog.InfoS("Using label selector for list nodes", "selector", labelSelector.String())

	ticker := time.NewTicker(time.Second * 15)
	defer ticker.Stop()
	printedLog := map[string]bool{}
	for {
		select {
		case <-s.nodeNotify:
			klog.V(5).InfoS("Received node notification")
		case <-ticker.C:
			klog.InfoS("Ticker triggered")
		case <-s.stopCh:
			klog.InfoS("Received stop signal, exiting RegisterFromNodeAnnotations")
			return
		}
		// 获取所有节点
		rawNodes, err := s.nodeLister.List(labelSelector)
		if err != nil {
			klog.ErrorS(err, "Failed to list nodes with selector", "selector", labelSelector.String())
			continue
		}
		klog.V(5).InfoS("Listed nodes", "nodeCount", len(rawNodes))
		var nodeNames []string
		for _, val := range rawNodes {
			nodeNames = append(nodeNames, val.Name)
			klog.V(5).InfoS("Processing node", "nodeName", val.Name)

			for devhandsk, devInstance := range device.GetDevices() {
				klog.V(5).InfoS("Checking device health", "nodeName", val.Name, "deviceVendor", devhandsk)

				// 从节点上获取设备信息
				nodedevices, err := devInstance.GetNodeDevices(*val)
				if err != nil {
					klog.V(5).InfoS("Failed to get node devices", "nodeName", val.Name, "deviceVendor", devhandsk)
					continue
				}

				health, needUpdate := devInstance.CheckHealth(devhandsk, val)
				klog.V(5).InfoS("Device health check result", "nodeName", val.Name, "deviceVendor", devhandsk, "health", health, "needUpdate", needUpdate)

				if !health {
					klog.Warning("Device is unhealthy, cleaning up node", "nodeName", val.Name, "deviceVendor", devhandsk)
					err := devInstance.NodeCleanUp(val.Name)
					if err != nil {
						klog.ErrorS(err, "Node cleanup failed", "nodeName", val.Name, "deviceVendor", devhandsk)
					}

					// 节点不健康了，移除节点的所有设备
					s.rmNodeDevices(val.Name, devhandsk)
					continue
				}
				if !needUpdate {
					klog.V(5).InfoS("No update needed for device", "nodeName", val.Name, "deviceVendor", devhandsk)
					continue
				}
				_, ok := util.HandshakeAnnos[devhandsk]
				if ok {
					tmppat := make(map[string]string)
					tmppat[util.HandshakeAnnos[devhandsk]] = "Requesting_" + time.Now().Format(time.DateTime)
					klog.InfoS("New timestamp for annotation", "nodeName", val.Name, "annotationKey", util.HandshakeAnnos[devhandsk], "annotationValue", tmppat[util.HandshakeAnnos[devhandsk]])
					n, err := util.GetNode(val.Name)
					if err != nil {
						klog.ErrorS(err, "Failed to get node", "nodeName", val.Name)
						continue
					}
					klog.V(5).InfoS("Patching node annotations", "nodeName", val.Name, "annotations", tmppat)
					if err := util.PatchNodeAnnotations(n, tmppat); err != nil {
						klog.ErrorS(err, "Failed to patch node annotations", "nodeName", val.Name)
					}
				}
				nodeInfo := &util.NodeInfo{}
				nodeInfo.ID = val.Name
				nodeInfo.Node = val
				klog.V(5).InfoS("Fetching node devices", "nodeName", val.Name, "deviceVendor", devhandsk)
				nodeInfo.Devices = make([]util.DeviceInfo, 0)
				for _, deviceinfo := range nodedevices {
					nodeInfo.Devices = append(nodeInfo.Devices, *deviceinfo)
				}
				s.addNode(val.Name, nodeInfo)
				if s.nodes[val.Name] != nil && len(nodeInfo.Devices) > 0 {
					if printedLog[val.Name] {
						klog.V(5).InfoS("Node device updated", "nodeName", val.Name, "deviceVendor", devhandsk, "nodeInfo", nodeInfo, "totalDevices", s.nodes[val.Name].Devices)
					} else {
						klog.InfoS("Node device added", "nodeName", val.Name, "deviceVendor", devhandsk, "nodeInfo", nodeInfo, "totalDevices", s.nodes[val.Name].Devices)
						printedLog[val.Name] = true
					}
				}
			}
		}
		// 从Pod上获取分配的设备信息
		_, _, err = s.getNodesUsage(&nodeNames, nil)
		if err != nil {
			klog.ErrorS(err, "Failed to get node usage", "nodeNames", nodeNames)
		}
	}
}

// InspectAllNodesUsage is used by metrics monitor.
func (s *Scheduler) InspectAllNodesUsage() *map[string]*NodeUsage {
	return &s.overviewstatus
}

// returns all nodes and its device memory usage, and we filter it with nodeSelector, taints, nodeAffinity
// unschedulerable and nodeName.
func (s *Scheduler) getNodesUsage(nodes *[]string, task *corev1.Pod) (*map[string]*NodeUsage, map[string]string, error) {
	overallnodeMap := make(map[string]*NodeUsage)
	cachenodeMap := make(map[string]*NodeUsage)
	failedNodes := make(map[string]string)
	// TODO remote unless code  done
	//for _, nodeID := range *nodes {
	// 这里获取的节点其实就是通过KS8 informer机制获取到的节点信息，每个节点的设备信息会被缓存起来，
	allNodes, err := s.ListNodes()
	if err != nil {
		return &overallnodeMap, failedNodes, err
	}

	for _, node := range allNodes {
		nodeInfo := &NodeUsage{}
		userGPUPolicy := config.GPUSchedulerPolicy
		// Pod上可以通过hami.io/gpu-scheduler-policy来指定GPU的调度策略，主要是通过打分来影响筛选的卡
		if task != nil && task.Annotations != nil {
			if value, ok := task.Annotations[policy.GPUSchedulerPolicyAnnotationKey]; ok {
				userGPUPolicy = value
			}
		}
		nodeInfo.Node = node.Node
		nodeInfo.Devices = policy.DeviceUsageList{
			Policy:      userGPUPolicy,
			DeviceLists: make([]*policy.DeviceListsScore, 0),
		}
		// 遍历当前节点上所有的设备情况，这里获取的是节点设备总资源情况
		for _, d := range node.Devices {
			nodeInfo.Devices.DeviceLists = append(nodeInfo.Devices.DeviceLists, &policy.DeviceListsScore{
				Score: 0,
				Device: &util.DeviceUsage{
					ID:        d.ID,
					Index:     d.Index,
					Used:      0,
					Count:     d.Count,
					Usedmem:   0,
					Totalmem:  d.Devmem,
					Totalcore: d.Devcore,
					Usedcores: 0,
					MigUsage: util.MigInUse{
						Index:     0,
						UsageList: make(util.MIGS, 0),
					},
					MigTemplate: d.MIGTemplate,
					Mode:        d.Mode,
					Type:        d.Type,
					Numa:        d.Numa,
					Health:      d.Health,
					CustomInfo:  maps.Clone(d.CustomInfo),
				},
			})
		}
		overallnodeMap[node.ID] = nodeInfo
	}

	// 1. 这里获取的Pod实际上也是从Informer得到的，这里主要是为了得到每个Pod分配的设备信息，这样就知道一个节点剩余的资源信息
	// 2. 这里后续的逻辑主要是为了计算每个节点资源的使用情况
	podsInfo := s.ListPodsInfo()
	for _, p := range podsInfo {
		// 如果当前Pod分配了当前还没有识别的节点，直接跳过。
		node, ok := overallnodeMap[p.NodeID]
		if !ok {
			// TODO 添加日志
			continue
		}
		// 遍历Pod分配的设备，key为不同厂商，譬如NVIDIA, MLU， DCU等等
		for _, podsingleds := range p.Devices {
			// 一个Pod可能有多个容器，因此这里需要遍历每个容器的设备
			for _, ctrdevs := range podsingleds {
				// 一个容器可能申请多个设备，这里遍历每一个设备
				for _, udevice := range ctrdevs {
					// 遍历节点的设备
					for _, d := range node.Devices.DeviceLists {
						deviceID := udevice.UUID
						// TODO mig相关的逻辑
						if strings.Contains(deviceID, "[") {
							deviceID = strings.Split(deviceID, "[")[0]
						}
						if d.Device.ID == deviceID { // 找到了节点上对应的设备
							// 统计Pod资源使用情况
							d.Device.Used++
							d.Device.Usedmem += udevice.Usedmem
							d.Device.Usedcores += udevice.Usedcores
							// TODO mig处理逻辑
							if strings.Contains(udevice.UUID, "[") {
								if strings.Compare(d.Device.Mode, "hami-core") == 0 {
									klog.Errorf("found a mig task running on a hami-core GPU\n")
									d.Device.Health = false
									continue
								}
								tmpIdx, Instance, _ := util.ExtractMigTemplatesFromUUID(udevice.UUID)
								if len(d.Device.MigUsage.UsageList) == 0 {
									util.PlatternMIG(&d.Device.MigUsage, d.Device.MigTemplate, tmpIdx)
								}
								d.Device.MigUsage.UsageList[Instance].InUse = true
								klog.V(5).Infoln("add mig usage", d.Device.MigUsage, "template=", d.Device.MigTemplate, "uuid=", d.Device.ID)
							}
						}
					}
				}
			}
		}
		klog.V(5).Infof("usage: pod %v assigned %v %v", p.Name, p.NodeID, p.Devices)
	}
	s.overviewstatus = overallnodeMap
	for _, nodeID := range *nodes {
		node, err := s.GetNode(nodeID)
		if err != nil {
			// The identified node does not have a gpu device, so the log here has no practical meaning,increase log priority.
			klog.V(5).InfoS("node unregistered", "node", nodeID, "error", err)
			failedNodes[nodeID] = "node unregistered"
			continue
		}
		cachenodeMap[node.ID] = overallnodeMap[node.ID]
	}
	s.cachedstatus = cachenodeMap
	return &cachenodeMap, failedNodes, nil
}

func (s *Scheduler) getPodUsage() (map[string]PodUseDeviceStat, error) {
	podUsageStat := make(map[string]PodUseDeviceStat)
	pods, err := s.podLister.List(labels.NewSelector())
	if err != nil {
		return nil, err
	}
	for _, pod := range pods {
		if pod.Status.Phase != corev1.PodSucceeded {
			continue
		}
		podUseDeviceNum := 0
		if v, ok := pod.Annotations[util.DeviceBindPhase]; ok && v == util.DeviceBindSuccess {
			podUseDeviceNum = 1
		}
		nodeName := pod.Spec.NodeName
		if _, ok := podUsageStat[nodeName]; !ok {
			podUsageStat[nodeName] = PodUseDeviceStat{
				TotalPod:     1,
				UseDevicePod: podUseDeviceNum,
			}
		} else {
			exist := podUsageStat[nodeName]
			podUsageStat[nodeName] = PodUseDeviceStat{
				TotalPod:     exist.TotalPod + 1,
				UseDevicePod: exist.UseDevicePod + podUseDeviceNum,
			}
		}
	}
	return podUsageStat, nil
}

func (s *Scheduler) Bind(args extenderv1.ExtenderBindingArgs) (*extenderv1.ExtenderBindingResult, error) {
	klog.InfoS("Attempting to bind pod to node", "pod", args.PodName, "namespace", args.PodNamespace, "node", args.Node)
	var res *extenderv1.ExtenderBindingResult

	binding := &corev1.Binding{
		ObjectMeta: metav1.ObjectMeta{Name: args.PodName, UID: args.PodUID},
		Target:     corev1.ObjectReference{Kind: "Node", Name: args.Node},
	}
	current, err := s.kubeClient.CoreV1().Pods(args.PodNamespace).Get(context.Background(), args.PodName, metav1.GetOptions{})
	if err != nil {
		klog.ErrorS(err, "Failed to get pod", "pod", args.PodName, "namespace", args.PodNamespace)
		return &extenderv1.ExtenderBindingResult{Error: err.Error()}, err
	}
	klog.InfoS("Trying to get the target node for pod", "pod", args.PodName, "namespace", args.PodNamespace, "node", args.Node)
	node, err := s.kubeClient.CoreV1().Nodes().Get(context.Background(), args.Node, metav1.GetOptions{})
	if err != nil {
		klog.ErrorS(err, "Failed to get node", "node", args.Node)
		s.recordScheduleBindingResultEvent(current, EventReasonBindingFailed, []string{}, fmt.Errorf("failed to get node %s", args.Node))
		res = &extenderv1.ExtenderBindingResult{Error: err.Error()}
		return res, nil
	}

	tmppatch := map[string]string{
		util.DeviceBindPhase:     "allocating",
		util.BindTimeAnnotations: strconv.FormatInt(time.Now().Unix(), 10),
	}

	for _, val := range device.GetDevices() {
		err = val.LockNode(node, current)
		if err != nil {
			klog.ErrorS(err, "Failed to lock node", "node", args.Node, "device", val)
			goto ReleaseNodeLocks
		}
	}

	err = util.PatchPodAnnotations(current, tmppatch)
	if err != nil {
		klog.ErrorS(err, "Failed to patch pod annotations", "pod", klog.KObj(current))
		return &extenderv1.ExtenderBindingResult{Error: err.Error()}, err
	}

	err = s.kubeClient.CoreV1().Pods(args.PodNamespace).Bind(context.Background(), binding, metav1.CreateOptions{})
	if err != nil {
		klog.ErrorS(err, "Failed to bind pod", "pod", args.PodName, "namespace", args.PodNamespace, "node", args.Node)
		goto ReleaseNodeLocks
	}

	s.recordScheduleBindingResultEvent(current, EventReasonBindingSucceed, []string{args.Node}, nil)
	klog.InfoS("Successfully bound pod to node", "pod", args.PodName, "namespace", args.PodNamespace, "node", args.Node)
	return &extenderv1.ExtenderBindingResult{Error: ""}, nil

ReleaseNodeLocks:
	klog.InfoS("Release node locks", "node", args.Node)
	for _, val := range device.GetDevices() {
		val.ReleaseNodeLock(node, current)
	}
	s.recordScheduleBindingResultEvent(current, EventReasonBindingFailed, []string{}, err)
	return &extenderv1.ExtenderBindingResult{Error: err.Error()}, nil
}

func (s *Scheduler) Filter(args extenderv1.ExtenderArgs) (*extenderv1.ExtenderFilterResult, error) {
	klog.InfoS("Starting schedule filter process", "pod", args.Pod.Name, "uuid", args.Pod.UID, "namespace", args.Pod.Namespace)
	// 通过解析pod.spec.resource.limit/request解析容器对于所有资源的申请
	// TODO 优化命名 done
	nums := k8sutil.Resourcereqs(args.Pod)
	total := 0
	for _, n := range nums {
		for _, k := range n {
			total += int(k.Nums)
		}
	}
	// 如果数量为0，说明当前Pod没有申请HAMI管理的任何异构资源，因此不需要HAMI决策调度流程，直接使用原生K8S调度器调度即可
	if total == 0 {
		klog.V(1).InfoS("Pod does not request any resources",
			"pod", args.Pod.Name)
		s.recordScheduleFilterResultEvent(args.Pod, EventReasonFilteringFailed, "", fmt.Errorf("does not request any resource"))
		return &extenderv1.ExtenderFilterResult{
			NodeNames:   args.NodeNames,
			FailedNodes: nil,
			Error:       "",
		}, nil
	}
	annos := args.Pod.Annotations
	// 1. 这里之所以要从缓存中删除节点信息，是因为后续会给节点分配设备重新添加进来
	// 2. TODO 同时有可能这个设备之前已经分配过卡了，因为这里在重新分配，所以也需要把之前使用的资源释放出来  不过感觉这种不大可能
	s.delPod(args.Pod)
	// 1. 获取当前K8S集群中每个Node资源使用情况
	// 2. 这里的args.NodeNames是K8S调度器经过预选之后传递给HAMI的可选节点，表示这些节点已经经过内存，磁盘，CPU等资源的过滤，也就是当前
	// Node在CPU, 内存，磁盘等方面已经满足了Pod的需求，但是需要HAMI决策，是否满足Pod的异构资源需求
	// TODO 有哪些情况导致一个节点失败？ 节点上没有设备？
	nodeUsage, failedNodes, err := s.getNodesUsage(args.NodeNames, args.Pod)
	if err != nil {
		s.recordScheduleFilterResultEvent(args.Pod, EventReasonFilteringFailed, "", err)
		return nil, err
	}
	if len(failedNodes) != 0 {
		klog.V(5).InfoS("Nodes failed during usage retrieval",
			"nodes", failedNodes)
	}
	// 给所有的节点进行打分
	nodeScores, err := s.calcScore(nodeUsage, nums, annos, args.Pod, failedNodes)
	if err != nil {
		err := fmt.Errorf("calcScore failed %v for pod %v", err, args.Pod.Name)
		s.recordScheduleFilterResultEvent(args.Pod, EventReasonFilteringFailed, "", err)
		return nil, err
	}
	if len((*nodeScores).NodeList) == 0 {
		klog.V(4).InfoS("No available nodes meet the required scores",
			"pod", args.Pod.Name)
		s.recordScheduleFilterResultEvent(args.Pod, EventReasonFilteringFailed, "", fmt.Errorf("no available node, %d nodes do not meet", len(*args.NodeNames)))
		return &extenderv1.ExtenderFilterResult{
			FailedNodes: failedNodes,
		}, nil
	}
	klog.V(4).Infoln("nodeScores_len=", len((*nodeScores).NodeList))
	// 按照分数，从小到大排序
	sort.Sort(nodeScores)
	// 选出最高分的那个节点
	m := (*nodeScores).NodeList[len((*nodeScores).NodeList)-1]
	klog.InfoS("Scheduling pod to node",
		"podNamespace", args.Pod.Namespace,
		"podName", args.Pod.Name,
		"nodeID", m.NodeID,
		"devices", m.Devices)
	annotations := make(map[string]string)
	annotations[util.AssignedNodeAnnotations] = m.NodeID
	annotations[util.AssignedTimeAnnotations] = strconv.FormatInt(time.Now().Unix(), 10)

	for _, val := range device.GetDevices() {
		val.PatchAnnotations(args.Pod, &annotations, m.Devices)
	}

	//InRequestDevices := util.EncodePodDevices(util.InRequestDevices, m.devices)
	//supportDevices := util.EncodePodDevices(util.SupportDevices, m.devices)
	//maps.Copy(annotations, InRequestDevices)
	//maps.Copy(annotations, supportDevices)
	s.addPod(args.Pod, m.NodeID, m.Devices)
	err = util.PatchPodAnnotations(args.Pod, annotations)
	if err != nil {
		s.recordScheduleFilterResultEvent(args.Pod, EventReasonFilteringFailed, "", err)
		s.delPod(args.Pod)
		return nil, err
	}
	successMsg := genSuccessMsg(len(*args.NodeNames), m.NodeID, nodeScores.NodeList)
	s.recordScheduleFilterResultEvent(args.Pod, EventReasonFilteringSucceed, successMsg, nil)
	res := extenderv1.ExtenderFilterResult{NodeNames: &[]string{m.NodeID}}
	return &res, nil
}

func genSuccessMsg(totalNodes int, target string, nodes []*policy.NodeScore) string {
	successMsg := "find fit node(%s), %d nodes not fit, %d nodes fit(%s)"
	var scores []string
	for _, no := range nodes {
		scores = append(scores, fmt.Sprintf("%s:%.2f", no.NodeID, no.Score))
	}
	score := strings.Join(scores, ",")
	return fmt.Sprintf(successMsg, target, totalNodes-len(nodes), len(nodes), score)
}
