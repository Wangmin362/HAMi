/**
# Copyright (c) 2022, NVIDIA CORPORATION.  All rights reserved.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.
**/

package rm

import (
	"fmt"

	"github.com/Project-HAMi/HAMi/pkg/device/nvidia"

	"github.com/NVIDIA/go-nvlib/pkg/nvlib/device"
	"github.com/NVIDIA/go-nvlib/pkg/nvml"
	spec "github.com/NVIDIA/k8s-device-plugin/api/config/v1"
)

type deviceMapBuilder struct {
	device.Interface                      // 通过nvml驱动接口获取gpu设备的各种信息
	config           *nvidia.DeviceConfig // TODO 这个配置是怎么拿到的？ 用户配置的么？
}

// DeviceMap stores a set of devices per resource name.
type DeviceMap map[spec.ResourceName]Devices

// NewDeviceMap creates a device map for the specified NVML library and config.
func NewDeviceMap(nvmllib nvml.Interface, config *nvidia.DeviceConfig) (DeviceMap, error) {
	b := deviceMapBuilder{
		Interface: device.New(device.WithNvml(nvmllib)),
		config:    config,
	}
	return b.build()
}

// build builds a map of resource names to devices.
func (b *deviceMapBuilder) build() (DeviceMap, error) {
	devices, err := b.buildDeviceMapFromConfigResources()
	if err != nil {
		return nil, fmt.Errorf("error building device map from config.resources: %v", err)
	}
	devices, err = updateDeviceMapWithReplicas(b.config, devices)
	if err != nil {
		return nil, fmt.Errorf("error updating device map with replicas from config.sharing.timeSlicing.resources: %v", err)
	}
	return devices, nil
}

// buildDeviceMapFromConfigResources builds a map of resource names to devices from spec.Config.Resources
func (b *deviceMapBuilder) buildDeviceMapFromConfigResources() (DeviceMap, error) {
	// 本质上还是通过调用底层的nvml函数库获取设备信息以及gpu设备的numa节点信息，并保存起来
	deviceMap, err := b.buildGPUDeviceMap()
	if err != nil {
		return nil, fmt.Errorf("error building GPU device map: %v", err)
	}

	if *b.config.Flags.MigStrategy == spec.MigStrategyNone {
		return deviceMap, nil
	}

	// 说明需要对gpu进行算力切分

	// 本质上就是通过调用nvml驱动接口获取每个设备的vgpu划分情况，同样也需要获取每个vgpu的numa节点信息，然后保存各个vgpu设备
	migDeviceMap, err := b.buildMigDeviceMap()
	if err != nil {
		return nil, fmt.Errorf("error building MIG device map: %v", err)
	}

	var requireUniformMIGDevices bool
	if *b.config.Flags.MigStrategy == spec.MigStrategySingle {
		// 由于开启的是mig single模式，因此一张卡只能被划分为相同规格的gpu
		requireUniformMIGDevices = true
	}

	// 检查vgpu划分是否有效，特别的。若gpu模式为mig single，需要检查当前gpu所有的vgpu是否是相同的规格
	err = b.assertAllMigDevicesAreValid(requireUniformMIGDevices)
	if err != nil {
		return nil, fmt.Errorf("invalid MIG configuration: %v", err)
	}

	// 说明一个节点上的卡要么配置算力切分，要么都是整卡调度
	if requireUniformMIGDevices && !deviceMap.isEmpty() && !migDeviceMap.isEmpty() {
		return nil, fmt.Errorf("all devices on the node must be configured with the same migEnabled value")
	}

	deviceMap.merge(migDeviceMap)

	return deviceMap, nil
}

// buildGPUDeviceMap builds a map of resource names to GPU devices
// 本质上还是通过调用底层的nvml函数库获取设备信息以及gpu设备的numa节点信息，并保存起来
func (b *deviceMapBuilder) buildGPUDeviceMap() (DeviceMap, error) {
	devices := make(DeviceMap)

	// 遍历当前节点每一个设备，通过nvml设备可以知道当前节点gpu的数量，然后遍历每一个设备进行处理
	b.VisitDevices(func(i int, gpu device.Device) error {
		name, ret := gpu.GetName()
		if ret != nvml.SUCCESS {
			return fmt.Errorf("error getting product name for GPU: %v", ret)
		}
		migEnabled, err := gpu.IsMigEnabled()
		if err != nil {
			return fmt.Errorf("error checking if MIG is enabled on GPU: %v", err)
		}
		// TODO 如果开启了MIG，但是MIG策略不是None，那么就不应该将GPU设备加入到设备映射表中
		if migEnabled && *b.config.Flags.MigStrategy != spec.MigStrategyNone {
			return nil
		}
		for _, resource := range b.config.Resources.GPUs {
			if resource.Pattern.Matches(name) {
				index, info := newGPUDevice(i, gpu)
				// 获取当前gpu设备的numa节点信息，然后保存在map中
				return devices.setEntry(resource.Name, index, info)
			}
		}
		return fmt.Errorf("GPU name '%v' does not match any resource patterns", name)
	})
	return devices, nil
}

// buildMigDeviceMap builds a map of resource names to MIG devices
// 本质上就是通过调用nvml驱动接口获取每个设备的vgpu划分情况，同样也需要获取每个vgpu的numa节点信息，然后保存各个vgpu设备
func (b *deviceMapBuilder) buildMigDeviceMap() (DeviceMap, error) {
	devices := make(DeviceMap)
	// 遍历mig设备，i应该表示的是第几个gpu设备，j表示当前gpu设备的第几个vgpu设备
	err := b.VisitMigDevices(func(i int, d device.Device, j int, mig device.MigDevice) error {
		migProfile, err := mig.GetProfile()
		if err != nil {
			return fmt.Errorf("error getting MIG profile for MIG device at index '(%v, %v)': %v", i, j, err)
		}
		for _, resource := range b.config.Resources.MIGs {
			if resource.Pattern.Matches(migProfile.String()) {
				index, info := newMigDevice(i, j, mig)
				return devices.setEntry(resource.Name, index, info)
			}
		}
		return fmt.Errorf("MIG profile '%v' does not match any resource patterns", migProfile)
	})
	return devices, err
}

// assertAllMigDevicesAreValid ensures that each MIG-enabled device has at least one MIG device
// associated with it.
// 检查vgpu划分是否有效，特别的。若gpu模式为mig single，需要检查当前gpu所有的vgpu是否是相同的规格
func (b *deviceMapBuilder) assertAllMigDevicesAreValid(uniform bool) error {
	err := b.VisitDevices(func(i int, d device.Device) error {
		isMigEnabled, err := d.IsMigEnabled()
		if err != nil {
			return err
		}
		if !isMigEnabled {
			return nil
		}
		migDevices, err := d.GetMigDevices()
		if err != nil {
			return err
		}
		if len(migDevices) == 0 {
			i := 0
			return fmt.Errorf("device %v has an invalid MIG configuration", i)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("at least one device with migEnabled=true was not configured correctly: %v", err)
	}

	if !uniform { // 说明当前gpu模式为mig mixed模式，所以不需要检查
		return nil
	}

	// 检查当前gpu划分出来的vgpu是否是相同的规格，如果不是需要报错
	var previousAttributes *nvml.DeviceAttributes
	return b.VisitMigDevices(func(i int, d device.Device, j int, m device.MigDevice) error {
		attrs, ret := m.GetAttributes()
		if ret != nvml.SUCCESS {
			return fmt.Errorf("error getting device attributes: %v", ret)
		}
		if previousAttributes == nil {
			previousAttributes = &attrs
		} else if attrs != *previousAttributes {
			return fmt.Errorf("more than one MIG device type present on node")
		}

		return nil
	})
}

// setEntry sets the DeviceMap entry for the specified resource.
// 获取当前gpu设备的numa节点信息，然后保存在map中
func (d DeviceMap) setEntry(name spec.ResourceName, index string, info deviceInfo) error {
	// 这里主要是获取numa信息
	dev, err := BuildDevice(index, info)
	if err != nil {
		return fmt.Errorf("error building Device: %v", err)
	}
	// 保存在map当中
	d.insert(name, dev)
	return nil
}

// insert adds the specified device to the device map
func (d DeviceMap) insert(name spec.ResourceName, dev *Device) {
	if d[name] == nil {
		d[name] = make(Devices)
	}
	d[name][dev.ID] = dev
}

// merge merges two devices maps
func (d DeviceMap) merge(o DeviceMap) {
	for name, devices := range o {
		for _, device := range devices {
			d.insert(name, device)
		}
	}
}

// isEmpty checks whether a device map is empty
func (d DeviceMap) isEmpty() bool {
	for _, devices := range d {
		if len(devices) > 0 {
			return false
		}
	}
	return true
}

// getIDsOfDevicesToReplicate returns a list of dervice IDs that we want to replicate.
func (d DeviceMap) getIDsOfDevicesToReplicate(r *spec.ReplicatedResource) ([]string, error) {
	devices, exists := d[r.Name]
	if !exists {
		return nil, nil
	}

	// If all devices for this resource type are to be replicated.
	if r.Devices.All {
		return devices.GetIDs(), nil
	}

	// If a specific number of devices for this resource type are to be replicated.
	if r.Devices.Count > 0 {
		if r.Devices.Count > len(devices) {
			return nil, fmt.Errorf("requested %d devices to be replicated, but only %d devices available", r.Devices.Count, len(devices))
		}
		return devices.GetIDs()[:r.Devices.Count], nil
	}

	// If a specific set of devices for this resource type are to be replicated.
	if len(r.Devices.List) > 0 {
		var ids []string
		for _, ref := range r.Devices.List {
			if ref.IsUUID() {
				d := devices.GetByID(string(ref))
				if d == nil {
					return nil, fmt.Errorf("no matching device with UUID: %v", ref)
				}
				ids = append(ids, d.ID)
			}
			if ref.IsGPUIndex() || ref.IsMigIndex() {
				d := devices.GetByIndex(string(ref))
				if d == nil {
					return nil, fmt.Errorf("no matching device at index: %v", ref)
				}
				ids = append(ids, d.ID)
			}
		}
		return ids, nil
	}

	return nil, fmt.Errorf("unexpected error")
}

// updateDeviceMapWithReplicas returns an updated map of resource names to devices with replica information from spec.Config.Sharing.TimeSlicing.Resources
func updateDeviceMapWithReplicas(config *nvidia.DeviceConfig, oDevices DeviceMap) (DeviceMap, error) {
	devices := make(DeviceMap)

	// Begin by walking config.Sharing.TimeSlicing.Resources and building a map of just the resource names.
	names := make(map[spec.ResourceName]bool)
	for _, r := range config.Sharing.TimeSlicing.Resources {
		names[r.Name] = true
	}

	// Copy over all devices from oDevices without a resource reference in TimeSlicing.Resources.
	for r, ds := range oDevices {
		if !names[r] {
			devices[r] = ds
		}
	}

	// Walk TimeSlicing.Resources and update devices in the device map as appropriate.
	for _, r := range config.Sharing.TimeSlicing.Resources {
		// Get the IDs of the devices we want to replicate from oDevices
		ids, err := oDevices.getIDsOfDevicesToReplicate(&r)
		if err != nil {
			return nil, fmt.Errorf("unable to get IDs of devices to replicate for '%v' resource: %v", r.Name, err)
		}
		// Skip any resources not matched in oDevices
		if len(ids) == 0 {
			continue
		}

		// Add any devices we don't want replicated directly into the device map.
		for _, d := range oDevices[r.Name].Difference(oDevices[r.Name].Subset(ids)) {
			devices.insert(r.Name, d)
		}

		// Create replicated devices add them to the device map.
		// Rename the resource for replicated devices as requested.
		name := r.Name
		if r.Rename != "" {
			name = r.Rename
		}
		for _, id := range ids {
			for i := 0; i < r.Replicas; i++ {
				annotatedID := string(NewAnnotatedID(id, i))
				replicatedDevice := *(oDevices[r.Name][id])
				replicatedDevice.ID = annotatedID
				devices.insert(name, &replicatedDevice)
			}
		}
	}

	return devices, nil
}
