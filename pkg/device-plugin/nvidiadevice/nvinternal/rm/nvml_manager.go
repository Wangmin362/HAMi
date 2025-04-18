/*
 * Copyright (c) 2019-2022, NVIDIA CORPORATION.  All rights reserved.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY Type, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package rm

import (
	"fmt"

	"github.com/Project-HAMi/HAMi/pkg/device/nvidia"

	"github.com/NVIDIA/go-nvlib/pkg/nvml"
	"k8s.io/klog/v2"
)

type nvmlResourceManager struct {
	resourceManager
	nvml nvml.Interface
}

var _ ResourceManager = (*nvmlResourceManager)(nil)

// NewNVMLResourceManagers returns a set of ResourceManagers, one for each NVML resource in 'config'.
// 1. nvml资源管理器管理的其实就是服务器级别的独立显卡GPU，通过调用底层驱动nvml库来管理这些设备。
// 2. 资源管理器的核心功能就是发现当前节点上所有插入的gpu卡，包括mig之后的vgpu卡。同时，也需要负责检查卡的健康状态。并及时将不健康的卡信息发送出去。
func NewNVMLResourceManagers(nvmllib nvml.Interface, config *nvidia.DeviceConfig) ([]ResourceManager, error) {
	ret := nvmllib.Init()
	if ret != nvml.SUCCESS {
		return nil, fmt.Errorf("failed to initialize NVML: %v", ret)
	}
	defer func() {
		ret := nvmllib.Shutdown()
		if ret != nvml.SUCCESS {
			klog.Infof("Error shutting down NVML: %v", ret)
		}
	}()

	// 1. 这里是NVML资源管理器的核心功能，主要是用于发现当前节点上所有的设备。
	// 2. 本质上还是通过调用底层的nvml函数库获取设备信息以及gpu设备的numa节点信息，并保存起来
	deviceMap, err := NewDeviceMap(nvmllib, config)
	if err != nil {
		return nil, fmt.Errorf("error building device map: %v", err)
	}

	var rms []ResourceManager
	for resourceName, devices := range deviceMap {
		if len(devices) == 0 {
			continue
		}
		// 每一种资源，可能不止一个设备，很有可能存在多个设备
		for key, value := range devices {
			// 可能配置了部分设备不需要注册，此时直接忽略这个设备
			if nvidia.FilterDeviceToRegister(value.ID, value.Index) {
				klog.V(5).InfoS("Filtering device", "device", value.ID)
				delete(devices, key)
				continue
			}
		}
		r := &nvmlResourceManager{
			resourceManager: resourceManager{
				config:   config,       // 设备配置
				resource: resourceName, // 资源名， 譬如nvidia.com/3g-32g等等
				devices:  devices,      // 当前资源所有的设备
			},
			nvml: nvmllib,
		}
		rms = append(rms, r)
	}

	return rms, nil
}

// GetPreferredAllocation runs an allocation algorithm over the inputs.
// The algorithm chosen is based both on the incoming set of available devices and various config settings.
func (r *nvmlResourceManager) GetPreferredAllocation(available, required []string, size int) ([]string, error) {
	return r.getPreferredAllocation(available, required, size)
}

// GetDevicePaths returns the required and optional device nodes for the requested resources
func (r *nvmlResourceManager) GetDevicePaths(ids []string) []string {
	paths := []string{
		"/dev/nvidiactl",
		"/dev/nvidia-uvm",
		"/dev/nvidia-uvm-tools",
		"/dev/nvidia-modeset",
	}

	// 每个设备的设备路径，譬如：/dev/nvidia0
	for _, p := range r.Devices().Subset(ids).GetPaths() {
		paths = append(paths, p)
	}

	return paths
}

// CheckHealth performs health checks on a set of devices, writing to the 'unhealthy' channel with any unhealthy devices
// 检查每种资源的健康状况
func (r *nvmlResourceManager) CheckHealth(stop <-chan interface{}, unhealthy chan<- *Device) error {
	return r.checkHealth(stop, r.devices, unhealthy)
}
