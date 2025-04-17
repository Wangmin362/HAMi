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

package main

import (
	"fmt"

	"github.com/Project-HAMi/HAMi/pkg/device-plugin/nvidiadevice/nvinternal/cdi"
	"github.com/Project-HAMi/HAMi/pkg/device-plugin/nvidiadevice/nvinternal/plugin/manager"
	"github.com/Project-HAMi/HAMi/pkg/device/nvidia"

	"github.com/NVIDIA/go-nvlib/pkg/nvml"
	spec "github.com/NVIDIA/k8s-device-plugin/api/config/v1"
)

// NewPluginManager creates an NVML-based plugin manager.
func NewPluginManager(config *nvidia.DeviceConfig) (manager.Interface, error) {
	var err error
	switch *config.Flags.MigStrategy {
	case spec.MigStrategyNone:
	case spec.MigStrategySingle:
	case spec.MigStrategyMixed:
	default:
		return nil, fmt.Errorf("unknown strategy: %v", *config.Flags.MigStrategy)
	}

	nvmllib := nvml.New()

	deviceListStrategies, err := spec.NewDeviceListStrategies(*config.Flags.Plugin.DeviceListStrategy)
	if err != nil {
		return nil, fmt.Errorf("invalid device list strategy: %v", err)
	}

	// CDI是container device interface的缩写，用于容器设备的抽象和管理。
	// 它提供了一种统一的方式来描述和管理容器中的设备，包括GPU、FPGA和其他类型的设备。
	// CDI的目标是简化容器设备的管理和使用，使得容器化环境中的设备管理更加高效和可靠。
	// CDI的核心概念包括设备插件（Device Plugin）、设备资源描述符（Device Resource Descriptor）和设备资源分配器（Device Resource Allocator）。
	// 设备插件负责发现和管理容器中的设备，设备资源描述符描述了设备的属性和功能，设备资源分配器负责将设备资源分配给容器。
	// CDI的设计目标是提供一种灵活、可扩展的设备管理方式，使得容器环境中的设备管理更加简单和高效。
	cdiEnabled := deviceListStrategies.IsCDIEnabled()

	cdiHandler, err := cdi.New(
		cdi.WithEnabled(cdiEnabled),
		cdi.WithDriverRoot(*config.Flags.Plugin.ContainerDriverRoot),
		cdi.WithTargetDriverRoot(*config.Flags.NvidiaDriverRoot),
		cdi.WithNvidiaCTKPath(*config.Flags.Plugin.NvidiaCTKPath),
		cdi.WithNvml(nvmllib),
		cdi.WithDeviceIDStrategy(*config.Flags.Plugin.DeviceIDStrategy),
		cdi.WithVendor("k8s.device-plugin.nvidia.com"),
		cdi.WithGdsEnabled(*config.Flags.GDSEnabled),
		cdi.WithMofedEnabled(*config.Flags.MOFEDEnabled),
	)
	if err != nil {
		return nil, fmt.Errorf("unable to create cdi handler: %v", err)
	}

	m, err := manager.New(
		manager.WithNVML(nvmllib),
		manager.WithCDIEnabled(cdiEnabled),
		manager.WithCDIHandler(cdiHandler),
		manager.WithConfig(config),
		manager.WithFailOnInitError(*config.Flags.FailOnInitError),
		manager.WithMigStrategy(*config.Flags.MigStrategy),
	)
	if err != nil {
		return nil, fmt.Errorf("unable to create plugin manager: %v", err)
	}

	if err := m.CreateCDISpecFile(); err != nil {
		return nil, fmt.Errorf("unable to create cdi spec file: %v", err)
	}

	return m, nil
}
