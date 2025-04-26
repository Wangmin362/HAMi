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
	// 1. 当一个Pod在执行hami-scheduler的Filter接口的时候，如果为这个Pod找到了一个合适的Node, 此时就会把需要绑定的节点写入
	// hami.io/vgpu-node注解，并且把当前时间写入到hami.io/vgpu-time注解
	AssignedTimeAnnotations = "hami.io/vgpu-time"
	// AssignedNodeAnnotations
	// 1. 当前一个Pod被hami调度之后，如果在Filter完成的时候找到了合适的Node，此时就会通过这个注解把选好的节点名写入到Pod上
	// 2. 也就是说，只要一个Pod上被标记了这个注解，正常情况下都是hami设置的，说明这个Pod已经完成了hami调度
	AssignedNodeAnnotations = "hami.io/vgpu-node"
	// 1. 把一个Pod绑定到一个Node的时候会打上这个注解，实在hami-scheduler的binder接口上做的
	// 2. 如果一个Pod没有被打上这个注解，说明这个Pod还没有真正调用hami-scheduler的binder接口绑定节点
	BindTimeAnnotations = "hami.io/bind-time"
	// 1. 目前有allocating, failed, success.
	// 2. 如果一个Pod经过binder接口之后，就会被设置为allocating状态，此时还没有分配真正的卡，当kubelet调用allocate接口之后，就会分配
	// 真正的卡，此时才会处于success状态
	// 3. 当kubelet调用Allocate接口之后，如果设备分配成功，这个注解会被设置为success，如果设备分配失败，这个注解会被设置为failed
	DeviceBindPhase = "hami.io/bind-phase"

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
	TaskPriority    = "CUDA_TASK_PRIORITY"
	CoreLimitSwitch = "GPU_CORE_UTILIZATION_POLICY"
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
	Nums             int32  // 容器请求当前设备的数量
	Type             string // 容器请求当前设备的类型
	Memreq           int32  // 内存请求
	MemPercentagereq int32  // 内存百分比请求
	Coresreq         int32  // 算力请求
}

type ContainerDevices []ContainerDevice

// 这里的Key为设备类型
type ContainerDeviceRequests map[string]ContainerDeviceRequest

// type ContainerAllDevices map[string]ContainerDevices.
type PodSingleDevice []ContainerDevices

// 数组索引为容器索引
type PodDeviceRequests []ContainerDeviceRequests
type PodDevices map[string]PodSingleDevice

type MigTemplate struct {
	Name   string `yaml:"name"`
	Memory int32  `yaml:"memory"`
	Count  int32  `yaml:"count"`
}

type MigTemplateUsage struct {
	Name   string `json:"name,omitempty"`
	Memory int32  `json:"memory,omitempty"`
	InUse  bool   `json:"inuse,omitempty"`
}

type Geometry []MigTemplate

type MIGS []MigTemplateUsage

type MigInUse struct {
	Index     int32
	UsageList MIGS
}

type AllowedMigGeometries struct {
	Models     []string   `yaml:"models"`
	Geometries []Geometry `yaml:"allowedGeometries"`
}

type DeviceUsage struct {
	ID          string // 设备UUID
	Index       uint   // 设备的索引
	Used        int32  // 消耗的数量，如果一个卡可以部署10个任务，那么没来一个任务就会消耗一个，直到消耗完10个
	Count       int32  // 其实就是replica或者device-split-count数量，即一个GPU可以部署多少个任务
	Usedmem     int32  // 使用内存，目前有两种形式指定使用方式：1. 直接指定使用内存，2. 指定使用内存的百分比
	Totalmem    int32  // 总内存
	Totalcore   int32  // 总算力
	Usedcores   int32  // 使用算力
	Mode        string // 设备模式：mig
	MigTemplate []Geometry
	MigUsage    MigInUse
	Numa        int    // NUMA节点
	Type        string // 当前设备的类型
	Health      bool   // 是否健康，一般是通过底层驱动上报上来的
}

type DeviceInfo struct {
	ID           string     `json:"id,omitempty"`
	Index        uint       `json:"index,omitempty"`
	Count        int32      `json:"count,omitempty"`
	Devmem       int32      `json:"devmem,omitempty"`
	Devcore      int32      `json:"devcore,omitempty"`
	Type         string     `json:"type,omitempty"`
	Numa         int        `json:"numa,omitempty"`
	Mode         string     `json:"mode,omitempty"`
	MIGTemplate  []Geometry `json:"migtemplate,omitempty"`
	Health       bool       `json:"health,omitempty"`
	DeviceVendor string     `json:"devicevendor,omitempty"`
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
	GPUSchedulerPolicySpread SchedulerPolicyName = "spread"
)

func (s SchedulerPolicyName) String() string {
	return string(s)
}
