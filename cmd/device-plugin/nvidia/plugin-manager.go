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

	// nvml路径一般在/usr/src/gdk/nvml/lib64/libnvml.so
	nvmllib := nvml.New()

	// TODO 这玩意干嘛的？
	deviceListStrategies, err := spec.NewDeviceListStrategies(*config.Flags.Plugin.DeviceListStrategy)
	if err != nil {
		return nil, fmt.Errorf("invalid device list strategy: %v", err)
	}

	// 1. CDI是container device interface的缩写，用于容器设备的抽象和管理。
	// 2. 本质上CDI规范其实主要是为了解决不同设备挂在和hook问题，一般来说我们使用一个gpu, fpga等设备，在容器中需要挂在对应设备的
	// 驱动文件，和一些必要的命令工具，譬如nvidia-smi，nvidia-ctk, ascend-smi等。 如果没有CDI规范，每个厂商就需要自己想办法把容器
	// 需要的文件挂载到容器里面，因为这些文件用户并不关心，用户的需求就是指定需要数量的设备即可。在这种情况下，不同的厂商基本都是需要实现自己
	// 的docker-runtime, 本质上就是修改了OCI规范的spec文件，然后调用了runc来启动容器。
	// 3. 但是CDI规范就解决了这个问题，它提供了一个抽象的接口，用于容器设备的抽象和管理。用户只需要指定需要的设备，CDI规范就会自动把容器需要的
	// 文件挂载到容器里面，用户不需要关心这些文件的具体路径。
	// 4. 具体来说，CDI规范提供了一个spec文件，用于描述容器需要的设备，譬如gpu, fpga等。然后CDI规范提供了一个runtime，用于启动容器。
	// runtime会根据spec文件来启动容器，然后runtime会自动把容器需要的文件挂载到容器里面。
	// TODO 如果CDI没有启用，或者用户的K8S集群版本低于1.28版本，如何解决文件的挂载问题？
	cdiEnabled := deviceListStrategies.IsCDIEnabled()

	// 1. 本质上CDIHandler的核心目标其实就是：当启动一个容器时，若想要这个容器正常使用设备，那么就必须要把设备的驱动文件挂载到容器里面，所以
	// CDIHandler的核心目标其实就是生成对应的CDI Spec文件，然后由容器运行时来完成挂在动作。
	// TODO 2. 若底层的容器运行时没有开启CDI功能怎么办？ device-plugin是否应该支持检查底层是否支持CDI的能力？
	cdiHandler, err := cdi.New(
		cdi.WithEnabled(cdiEnabled),                                  // 是否启用CDI
		cdi.WithDriverRoot(*config.Flags.Plugin.ContainerDriverRoot), // 启动device-plugin时必须要指定驱动的根目录
		cdi.WithTargetDriverRoot(*config.Flags.NvidiaDriverRoot),     // TODO 和上面的有啥区别？
		cdi.WithNvidiaCTKPath(*config.Flags.Plugin.NvidiaCTKPath),    // nvidia-ctk的路径,即container toolkit的路径
		cdi.WithNvml(nvmllib),                                        // 调用底层设备的接口
		cdi.WithDeviceIDStrategy(*config.Flags.Plugin.DeviceIDStrategy),
		cdi.WithVendor("k8s.device-plugin.nvidia.com"),
		cdi.WithGdsEnabled(*config.Flags.GDSEnabled),     // NVIDIA GPUDirect Storage（GDS）是一种存储加速技术
		cdi.WithMofedEnabled(*config.Flags.MOFEDEnabled), // NVIDIA MOFED（Mellanox OFED，OpenFabrics Enterprise Distribution for Mellanox）是针对 Mellanox 网络适配器的驱动和软件集合
	)
	if err != nil {
		return nil, fmt.Errorf("unable to create cdi handler: %v", err)
	}

	m, err := manager.New(
		manager.WithNVML(nvmllib),
		manager.WithCDIEnabled(cdiEnabled),
		manager.WithCDIHandler(cdiHandler),
		manager.WithConfig(config),                                 // 设备配置
		manager.WithFailOnInitError(*config.Flags.FailOnInitError), // 初始化失败是否退出
		manager.WithMigStrategy(*config.Flags.MigStrategy),         // MIG策略，目前支持none, single, mixed
	)
	if err != nil {
		return nil, fmt.Errorf("unable to create plugin manager: %v", err)
	}

	// 生成CDI Spec文件放在/var/run/cdi目录下
	if err := m.CreateCDISpecFile(); err != nil {
		return nil, fmt.Errorf("unable to create cdi spec file: %v", err)
	}

	return m, nil
}
