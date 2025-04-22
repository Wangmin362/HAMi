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
	"sort"
	"strings"
	"sync"

	corev1 "k8s.io/api/core/v1"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/klog/v2"

	"github.com/Project-HAMi/HAMi/pkg/device"
	"github.com/Project-HAMi/HAMi/pkg/scheduler/config"
	"github.com/Project-HAMi/HAMi/pkg/scheduler/policy"
	"github.com/Project-HAMi/HAMi/pkg/util"
)

func viewStatus(usage NodeUsage) {
	klog.V(5).Info("devices status")
	for _, val := range usage.Devices.DeviceLists {
		klog.V(5).InfoS("device status", "device id", val.Device.ID, "device detail", val)
	}
}

func checkType(annos map[string]string, d util.DeviceUsage, n util.ContainerDeviceRequest) (bool, bool) {
	//General type check, NVIDIA->NVIDIA MLU->MLU
	klog.V(3).InfoS("Type check", "device", d.Type, "req", n.Type)
	if !strings.Contains(d.Type, n.Type) {
		return false, false
	}
	for _, val := range device.GetDevices() { // 遍历HAMi支持的所有设备类型
		found, pass, numaAssert := val.CheckType(annos, d, n)
		if found {
			return pass, numaAssert
		}
	}
	klog.Infof("Unrecognized device %s", n.Type)
	return false, false
}

func checkUUID(annos map[string]string, d util.DeviceUsage, n util.ContainerDeviceRequest) bool {
	devices, ok := device.GetDevices()[n.Type]
	if !ok {
		klog.Errorf("can not get device for %s type", n.Type)
		return false
	}
	result := devices.CheckUUID(annos, d)
	klog.V(2).Infof("checkUUID result is %v for %s type", result, n.Type)
	return result
}

func fitInCertainDevice(
	node *NodeUsage, // 当前节点的设备使用信息
	request util.ContainerDeviceRequest, // 容器请求的某一种资源
	annos map[string]string, // 当前正在调度容器的注解
	pod *corev1.Pod,
	allocated *util.PodDevices,
) (bool, map[string]util.ContainerDevices) {
	k := request
	originReq := k.Nums
	prevnuma := -1
	klog.InfoS("Allocating device for container request", "pod", klog.KObj(pod), "card request", k)
	var tmpDevs map[string]util.ContainerDevices
	tmpDevs = make(map[string]util.ContainerDevices)
	for i := len(node.Devices.DeviceLists) - 1; i >= 0; i-- {
		klog.InfoS("scoring pod", "pod", klog.KObj(pod), "Memreq", k.Memreq, "MemPercentagereq", k.MemPercentagereq, "Coresreq", k.Coresreq, "Nums", k.Nums, "device index", i, "device", node.Devices.DeviceLists[i].Device.ID)
		found, numa := checkType(annos, *node.Devices.DeviceLists[i].Device, k)
		if !found {
			klog.InfoS("card type mismatch,continuing...", "pod", klog.KObj(pod), (node.Devices.DeviceLists[i].Device).Type, k.Type)
			continue
		}
		if numa && prevnuma != node.Devices.DeviceLists[i].Device.Numa {
			klog.InfoS("Numa not fit, resotoreing", "pod", klog.KObj(pod), "k.nums", k.Nums, "numa", numa, "prevnuma", prevnuma, "device numa", node.Devices.DeviceLists[i].Device.Numa)
			k.Nums = originReq
			prevnuma = node.Devices.DeviceLists[i].Device.Numa
			tmpDevs = make(map[string]util.ContainerDevices)
		}
		if !checkUUID(annos, *node.Devices.DeviceLists[i].Device, k) {
			klog.InfoS("card uuid mismatch,", "pod", klog.KObj(pod), "current device info is:", *node.Devices.DeviceLists[i].Device)
			continue
		}

		memreq := int32(0)
		// 说明当前GPU上运行的任务数量已经达到上限
		if node.Devices.DeviceLists[i].Device.Count <= node.Devices.DeviceLists[i].Device.Used {
			continue
		}
		if k.Coresreq > 100 {
			klog.ErrorS(nil, "core limit can't exceed 100", "pod", klog.KObj(pod))
			k.Coresreq = 100
			//return false, tmpDevs
		}
		if k.Memreq > 0 {
			memreq = k.Memreq
		}
		if k.MemPercentagereq != 101 && k.Memreq == 0 {
			//This incurs an issue
			memreq = node.Devices.DeviceLists[i].Device.Totalmem * k.MemPercentagereq / 100
		}
		if node.Devices.DeviceLists[i].Device.Totalmem-node.Devices.DeviceLists[i].Device.Usedmem < memreq {
			klog.V(5).InfoS("card Insufficient remaining memory", "pod", klog.KObj(pod), "device index", i, "device", node.Devices.DeviceLists[i].Device.ID, "device total memory", node.Devices.DeviceLists[i].Device.Totalmem, "device used memory", node.Devices.DeviceLists[i].Device.Usedmem, "request memory", memreq)
			continue
		}
		if node.Devices.DeviceLists[i].Device.Totalcore-node.Devices.DeviceLists[i].Device.Usedcores < k.Coresreq {
			klog.V(5).InfoS("card Insufficient remaining cores", "pod", klog.KObj(pod), "device index", i, "device", node.Devices.DeviceLists[i].Device.ID, "device total core", node.Devices.DeviceLists[i].Device.Totalcore, "device used core", node.Devices.DeviceLists[i].Device.Usedcores, "request cores", k.Coresreq)
			continue
		}
		// Coresreq=100 indicates it want this card exclusively
		if node.Devices.DeviceLists[i].Device.Totalcore == 100 && k.Coresreq == 100 && node.Devices.DeviceLists[i].Device.Used > 0 {
			klog.V(5).InfoS("the container wants exclusive access to an entire card, but the card is already in use", "pod", klog.KObj(pod), "device index", i, "device", node.Devices.DeviceLists[i].Device.ID, "used", node.Devices.DeviceLists[i].Device.Used)
			continue
		}
		// You can't allocate core=0 job to an already full GPU
		if node.Devices.DeviceLists[i].Device.Totalcore != 0 && node.Devices.DeviceLists[i].Device.Usedcores == node.Devices.DeviceLists[i].Device.Totalcore && k.Coresreq == 0 {
			klog.V(5).InfoS("can't allocate core=0 job to an already full GPU", "pod", klog.KObj(pod), "device index", i, "device", node.Devices.DeviceLists[i].Device.ID)
			continue
		}
		if !device.GetDevices()[k.Type].CustomFilterRule(allocated, request, tmpDevs[k.Type], node.Devices.DeviceLists[i].Device) {
			continue
		}
		if k.Nums > 0 {
			klog.InfoS("first fitted", "pod", klog.KObj(pod), "device", node.Devices.DeviceLists[i].Device.ID)
			k.Nums--
			// 之前的检查全部通过，说明当前设备可以满足容器的需求
			tmpDevs[k.Type] = append(tmpDevs[k.Type], util.ContainerDevice{
				Idx:       int(node.Devices.DeviceLists[i].Device.Index),
				UUID:      node.Devices.DeviceLists[i].Device.ID,
				Type:      k.Type,
				Usedmem:   memreq,
				Usedcores: k.Coresreq,
			})
		}
		if k.Nums == 0 {
			klog.InfoS("device allocate success", "pod", klog.KObj(pod), "allocate device", tmpDevs)
			return true, tmpDevs
		}
		// 如果是GPU，GPU在MIG模式下可以划分为多个实例。因此GPU分配两个一个MIG实例，也许剩下的MIG实例还可以给容器分配
		if node.Devices.DeviceLists[i].Device.Mode == "mig" {
			i++
		}
	}
	return false, tmpDevs
}

// 当前节点是否能够部署单个容器，原理就是：如果节点资源能够满足容器需要的每个资源，那么就可以部署这个容器
func fitInDevices(
	node *NodeUsage, // 当前节点设备使用情况
	requests util.ContainerDeviceRequests, // 当前需要部署的容器，显然每成功部署一个容器，就需要消耗这个节点的资源
	annos map[string]string, // 当前要调度的Pod的注解
	pod *corev1.Pod, // 当前要调度的Pod
	devinput *util.PodDevices, // TODO 如何理解这个结构？
) (bool, float32) {
	//devmap := make(map[string]util.ContainerDevices)
	devs := util.ContainerDevices{}
	total, totalCore, totalMem := int32(0), int32(0), int32(0)
	free, freeCore, freeMem := int32(0), int32(0), int32(0)
	sums := 0
	// computer all device score for one node
	for index := range node.Devices.DeviceLists {
		// 计算每个卡的分数 = 使用卡数/总卡数 + 使用内存/总内存 + 使用算力/总算力 的和，这里的卡数指的是设备的虚拟卡数，也有可能是一个GPU
		// 支持同时部署的任务数量
		node.Devices.DeviceLists[index].ComputeScore(requests)
	}
	//This loop is for requests for different devices
	for _, k := range requests { // 遍历容器申请的每一种资源
		sums += int(k.Nums)
		if int(k.Nums) > len(node.Devices.DeviceLists) { // 如果当前容器申请的设备数量已经大于节点上的设备数量，那么肯定无法部署这个容器
			klog.InfoS("request devices nums cannot exceed the total number of devices on the node.", "pod", klog.KObj(pod), "request devices nums", k.Nums, "node device nums", len(node.Devices.DeviceLists))
			return false, 0
		}
		sort.Sort(node.Devices)
		// 针对容器申请的每一种资源，遍历节点上的每一个设备，看看当前设备是否能够满足容器的要求，如果满足，那么就需要消耗这个设备的资源
		fit, tmpDevs := fitInCertainDevice(node, k, annos, pod, devinput)
		if fit {
			for idx, val := range tmpDevs[k.Type] { // 遍历给当前容器分配的设备，虽然只有一种设备，但是容器可能一次性申请了多个
				for nidx, v := range node.Devices.DeviceLists {
					//bc node.Devices has been sorted, so we should find out the correct device
					if v.Device.ID != val.UUID {
						continue
					}
					total += v.Device.Count
					totalCore += v.Device.Totalcore
					totalMem += v.Device.Totalmem
					// 当前节点可以部署这个容器，因此如果部署了这个容器，就需要减去对应消耗的资源
					free += v.Device.Count - v.Device.Used
					freeCore += v.Device.Totalcore - v.Device.Usedcores
					freeMem += v.Device.Totalmem - v.Device.Usedmem
					err := device.GetDevices()[k.Type].AddResourceUsage(node.Devices.DeviceLists[nidx].Device, &tmpDevs[k.Type][idx])
					if err != nil {
						klog.Errorf("AddResource failed:%s", err.Error())
						return false, 0
					}
					klog.Infoln("After AddResourceUsage:", node.Devices.DeviceLists[nidx].Device)
				}
			}
			devs = append(devs, tmpDevs[k.Type]...)
		} else {
			return false, 0
		}
		(*devinput)[k.Type] = append((*devinput)[k.Type], devs)
	}
	return true, 0
}

func (s *Scheduler) calcScore(nodes *map[string]*NodeUsage, nums util.PodDeviceRequests, annos map[string]string, task *corev1.Pod, failedNodes map[string]string) (*policy.NodeScoreList, error) {
	userNodePolicy := config.NodeSchedulerPolicy
	if annos != nil {
		// 用户可以在Pod注解上指定节点调度策略，默认为binpack策略，即优先把任务调度到剩余资源最少的节点上，尽量减少集群资源碎片
		if value, ok := annos[policy.NodeSchedulerPolicyAnnotationKey]; ok {
			userNodePolicy = value
		}
	}
	res := policy.NodeScoreList{
		Policy:   userNodePolicy,
		NodeList: make([]*policy.NodeScore, 0),
	}

	wg := sync.WaitGroup{}
	mutex := sync.Mutex{}
	errCh := make(chan error, len(*nodes))
	for nodeID, node := range *nodes {
		wg.Add(1)
		// 每个节点的打分是独立的，互不影响。因为每个节点的分数只和当前节点总的资源以及使用的资源有关，因此可以分开统计。
		go func(nodeID string, node *NodeUsage) {
			defer wg.Done()

			viewStatus(*node)
			score := policy.NodeScore{NodeID: nodeID, Node: node.Node, Devices: make(util.PodDevices), Score: 0}
			// 计算当前节点的分数 = 可用卡数/总卡数 + 可用内存/总内存 + 可用算力/总算力 的和
			score.ComputeDefaultScore(node.Devices)

			//This loop is for different container request
			ctrfit := false
			for ctrid, n := range nums {
				sums := 0 // 当前容器请求的计算资源总数量
				for _, k := range n {
					sums += int(k.Nums)
				}

				if sums == 0 { // what situation will happen?
					for idx := range score.Devices {
						for len(score.Devices[idx]) <= ctrid {
							score.Devices[idx] = append(score.Devices[idx], util.ContainerDevices{})
						}
						score.Devices[idx][ctrid] = append(score.Devices[idx][ctrid], util.ContainerDevice{})
					}
				}
				klog.V(5).InfoS("fitInDevices", "pod", klog.KObj(task), "node", nodeID)
				// 1. 判断当前节点是否能够满足当前容器的需求，如果当前节点满足Pod所有容器的需求，那么当前节点就是一个合适的节点，一次可以部署这个Pod
				fit, _ := fitInDevices(node, n, annos, task, &score.Devices)
				ctrfit = fit
				if !fit { // 只要Pod的任何一个容器不能满足需求，那么当前节点就不是一个合适的节点，不能部署这个Pod
					// 这里没有指定日志级别，因此是0
					klog.InfoS("calcScore:node not fit pod", "pod", klog.KObj(task), "node", nodeID)
					failedNodes[nodeID] = "node not fit pod"
					break
				}
			}

			if ctrfit {
				mutex.Lock()
				// 说明当前节点满足当前Pod所有容器的需求
				res.NodeList = append(res.NodeList, &score)
				mutex.Unlock()
				// 统计当前节点每个卡的分数，如果每个卡的分数之和大于0，则当前节点的分数就是每个卡的分数之和
				score.OverrideScore(node.Devices, userNodePolicy)
			}
		}(nodeID, node)
	}
	wg.Wait()
	close(errCh)

	var errorsSlice []error
	for e := range errCh {
		errorsSlice = append(errorsSlice, e)
	}
	return &res, utilerrors.NewAggregate(errorsSlice)
}
