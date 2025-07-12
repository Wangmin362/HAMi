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

package policy

import (
	"github.com/Project-HAMi/HAMi/pkg/util"

	"k8s.io/klog/v2"
)

// DeviceListsScore 抽象一个设备的详细信息，譬如
type DeviceListsScore struct {
	Device *util.DeviceUsage
	// Score recode every device user/allocate score
	// 设备的分数计算公式为：使用卡数/总卡数 + 使用内存/总内存 + 使用算力/总算力 的和，其中的卡数指的是设备的虚拟卡数
	Score float32
}

type DeviceUsageList struct {
	DeviceLists []*DeviceListsScore
	// GPU卡的调度策略，目前之后：spread和binpack, 默认是spread. spread表示尽可能的将卡分配到不同的节点上，binpack表示尽可能的将卡分配到同一个节点上
	Policy string
}

func (l DeviceUsageList) Len() int {
	return len(l.DeviceLists)
}

func (l DeviceUsageList) Swap(i, j int) {
	l.DeviceLists[i], l.DeviceLists[j] = l.DeviceLists[j], l.DeviceLists[i]
}

func (l DeviceUsageList) Less(i, j int) bool {
	if l.Policy == util.GPUSchedulerPolicyBinpack.String() {
		if l.DeviceLists[i].Device.Numa == l.DeviceLists[j].Device.Numa {
			return l.DeviceLists[i].Score < l.DeviceLists[j].Score
		}
		return l.DeviceLists[i].Device.Numa > l.DeviceLists[j].Device.Numa
	}
	// default policy is spread
	if l.DeviceLists[i].Device.Numa == l.DeviceLists[j].Device.Numa {
		return l.DeviceLists[i].Score > l.DeviceLists[j].Score
	}
	return l.DeviceLists[i].Device.Numa < l.DeviceLists[j].Device.Numa
}

// ComputeScore 计算每个卡的分数 = 使用卡数/总卡数 + 使用内存/总内存 + 使用算力/总算力 的和，这里的卡数指的是设备的虚拟卡数，也有可能是一个GPU
// 支持同时部署的任务数量
func (ds *DeviceListsScore) ComputeScore(requests util.ContainerDeviceRequests) {
	request, core, mem := int32(0), int32(0), int32(0)
	// Here we are required to use the same type device
	for _, container := range requests {
		request += container.Nums
		core += container.Coresreq
		if container.MemPercentagereq != 0 && container.MemPercentagereq != 101 {
			mem += ds.Device.Totalmem * (container.MemPercentagereq / 100.0)
			continue
		}
		mem += container.Memreq
	}
	klog.V(2).Infof("device %s user %d, userCore %d, userMem %d,", ds.Device.ID, ds.Device.Used, ds.Device.Usedcores, ds.Device.Usedmem)

	usedScore := float32(request+ds.Device.Used) / float32(ds.Device.Count)
	coreScore := float32(core+ds.Device.Usedcores) / float32(ds.Device.Totalcore)
	memScore := float32(mem+ds.Device.Usedmem) / float32(ds.Device.Totalmem)
	ds.Score = float32(Weight) * (usedScore + coreScore + memScore)
	klog.V(2).Infof("device %s computer score is %f", ds.Device.ID, ds.Score)
}
