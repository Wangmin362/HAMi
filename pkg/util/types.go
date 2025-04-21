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

package util

import (
	corev1 "k8s.io/api/core/v1"
)

const (
	//ResourceName = "nvidia.com/gpu"
	//ResourceName = "hami.io/vgpu".
	AssignedTimeAnnotations = "hami.io/vgpu-time"
	AssignedNodeAnnotations = "hami.io/vgpu-node"  // 在extender的filter节点会被hami-scheduler设置为合适的节点名
	BindTimeAnnotations     = "hami.io/bind-time"  // Pod绑定到Node的时间，由hami-scheduler在bind api中设置
	DeviceBindPhase         = "hami.io/bind-phase" // Pod的绑定状态，由hami-scheduler在bind api中设置为allocate TODO 谁会改？

	DeviceBindAllocating = "allocating"
	DeviceBindFailed     = "failed"
	DeviceBindSuccess    = "success"

	DeviceLimit = 100
	//TimeLayout = "ANSIC"
	//DefaultTimeout = time.Second * 60.

	BestEffort string = "best-effort"
	Restricted string = "restricted"
	Guaranteed string = "guaranteed"

	// NodeNameEnvName define env var name for use get node name.
	NodeNameEnvName = "NODE_NAME"
)

var (
	DebugMode bool

	NodeName          string
	RuntimeSocketFlag string
)

type ContainerDevice struct {
	// TODO current Idx cannot use, because EncodeContainerDevices method not encode this filed.
	Idx       int
	UUID      string
	Type      string
	Usedmem   int32
	Usedcores int32
}

type ContainerDeviceRequest struct {
	Nums             int32  // 请求的资源数量，譬如2个cpu，或者2个gpu
	Type             string // 这里应该是资源类型
	Memreq           int32  // 设置当前容器内存的上限
	MemPercentagereq int32  // 内存百分比，譬如30表示最多使用30%的内存
	Coresreq         int32  // 算力请求，这是一个百分比，譬如30表示30%的算力
}

// 一个容器可能申请多个gpu设备
type ContainerDevices []ContainerDevice

// 每个容器可以申请多种不同类型的资源，key为资源类型，value为资源请求
type ContainerDeviceRequests map[string]ContainerDeviceRequest

// type ContainerAllDevices map[string]ContainerDevices.
// 一个pod可能有多个容器
type PodSingleDevice []ContainerDevices

// 当前Pod申请的资源
type PodDeviceRequests []ContainerDeviceRequests

// TODO 这里为什么是一个map， key表示什么？ key似乎表示deviceid,暂时没看懂这个map的设计
type PodDevices map[string]PodSingleDevice

type DeviceUsage struct {
	ID        string
	Index     uint
	Used      int32 // TODO 当前使用这个设备容器数量
	Count     int32
	Usedmem   int32 // 所有使用这个设备的容器，已经申请使用的内存
	Totalmem  int32
	Totalcore int32
	Usedcores int32 // 所有使用这个设备的容器，已经申请使用的算力
	Numa      int
	Type      string
	Health    bool
}

type DeviceInfo struct {
	ID           string // 这里因该是设备的UUID
	Index        uint   // 设备索引
	Count        int32  // TODO 这个是啥？
	Devmem       int32  // 内存限制
	Devcore      int32  // 算力限制
	Type         string
	Numa         int  // 当前设备在哪个numa节点上
	Health       bool // 当前设备是否健康
	DeviceVendor string
}

type NodeInfo struct {
	ID      string
	Node    *corev1.Node
	Devices []DeviceInfo
}

type SchedulerPolicyName string

const (
	// NodeSchedulerPolicyBinpack is node use binpack scheduler policy.
	NodeSchedulerPolicyBinpack SchedulerPolicyName = "binpack"
	// NodeSchedulerPolicySpread is node use spread scheduler policy.
	NodeSchedulerPolicySpread SchedulerPolicyName = "spread"
	// GPUSchedulerPolicyBinpack is GPU use binpack scheduler.
	GPUSchedulerPolicyBinpack SchedulerPolicyName = "binpack"
	// GPUSchedulerPolicySpread is GPU use spread scheduler.
	// 这种情况下尽可能将任务分散在不同的GPU上，以最大化利用GPU使用效率
	GPUSchedulerPolicySpread SchedulerPolicyName = "spread"
)

func (s SchedulerPolicyName) String() string {
	return string(s)
}
