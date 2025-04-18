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
	"strings"

	"github.com/NVIDIA/go-nvlib/pkg/nvlib/device"
	"github.com/NVIDIA/go-nvlib/pkg/nvlib/info"
	"github.com/NVIDIA/go-nvlib/pkg/nvml"
	spec "github.com/NVIDIA/k8s-device-plugin/api/config/v1"
	"github.com/Project-HAMi/HAMi/pkg/device/nvidia"
	"k8s.io/klog/v2"
)

// resourceManager forms the base type for specific resource manager implementations
// 资源管理器的基础实现
type resourceManager struct {
	// TODO 如何理解这里的设备配置
	config   *nvidia.DeviceConfig // 设备配置
	resource spec.ResourceName    // 当前资源管理器管理的资源名
	devices  Devices              // 当前资源管理器管理的设备
}

// ResourceManager provides an interface for listing a set of Devices and checking health on them
// 1. 资源管理器主要是用于管理当前节点上的计算资源。资源和设备应该是有区别的。因为可以在一个设备上通过gpu single和gpu mixed来划分资源。
// 2. 这样一个设备就可以被划分为多个资源。此时即使你只有一个节点，即便每张gpu卡都是一样的，但是可能由于划分策略的不同，造成一个节点会有多个资源。
type ResourceManager interface {
	Resource() spec.ResourceName // 当前管理的资源名
	Devices() Devices            // 当前资源的设备数量
	// GetDevicePaths 1. /dev目录下设备的名字，譬如：/dev/nvidia0，容器在使用的时候需要把这些设备挂在进去
	// 当然，也有可能还有其它命令，譬如/dev/nvidiactl, /dev/nvidia-uvm, /dev/nvidia-uvm-tools, /dev/nvidia-modeset等等
	GetDevicePaths([]string) []string
	GetPreferredAllocation(available, required []string, size int) ([]string, error)
	// CheckHealth 用于检查当前设备的健康状态，如果检查到设备不健康了，此时通过unhealthy channel将设备信息发送出去。
	CheckHealth(stop <-chan interface{}, unhealthy chan<- *Device) error
}

// NewResourceManagers returns a []ResourceManager, one for each resource in 'config'.
// 1. nvidia中资源管理器只有两种，分别是：nvml资源管理器和tegra资源管理器。
// 2. 如果一个节点上及支持nvml，又支持tegra,那么这里将会实例化两种资源管理器。 后续可以看到，将会默认使用nvml资源管理器。
// 3. Q: 什么情况下，一个节点上会同时支持nvml和tegra？ A: 从我目前理解到的信息看来，不大可能出现这种情况。
// 3.1 NVIDIA NVML（NVIDIA Management Library） 是一个基于 C 语言的编程接口，用于监控和管理 NVIDIA Tesla 系列 GPU 的各种状态，
// 是一种软件层面的管理工具，主要用于服务器、工作站等专业计算场景下对 GPU 进行管理和监控。
// 3.2 而 Tegra 是 NVIDIA 推出的一系列系统级芯片（SoC），主要应用于移动设备、嵌入式设备以及一些特定的低功耗计算场景，如平板电脑、智能手机、
// 车载信息娱乐系统等。Tegra 芯片集成了 CPU、GPU、内存控制器等多种功能模块，以满足移动和嵌入式设备对低功耗、小体积和高度集成化的要求。
// 3.3 从硬件角度来看，计算机使用的 GPU 要么是支持 NVML 的独立 NVIDIA GPU，要么是集成了 Tegra SoC 的芯片，两者的硬件架构和设计用途不同，
// 一般不会同时出现在同一台计算机中。从软件角度讲，两种模式所对应的驱动程序和软件环境也有所不同，很难同时在一个系统中兼容并让计算机同时处于这两种模式。
// 3.4 不过，在一些特殊的开发或实验环境中，如果通过特殊的硬件设计和软件配置，理论上可能实现类似的功能，但这并不是常见的应用场景。
func NewResourceManagers(nvmllib nvml.Interface, config *nvidia.DeviceConfig) ([]ResourceManager, error) {
	// logWithReason logs the output of the has* / is* checks from the info.Interface
	logWithReason := func(f func() (bool, string), tag string) bool {
		is, reason := f()
		if !is {
			tag = "non-" + tag
		}
		klog.Infof("Detected %v platform: %v", tag, reason)
		return is
	}

	infolib := info.New()

	// 1. 判断是否支持nvml，其实就是判断当前节点是否存在libnvidia-ml.so.1库文件，若存在说明支持nvml。
	// 2. 这里需要注意的是，由于device-plugin运行在容器当中，因此在启动容器的时候肯定需要吧nvml库文件挂载到容器当中。
	hasNVML := logWithReason(infolib.HasNvml, "NVML")
	// 1. 本质上还是通过节点上是否存在/etc/nv_tegra_release,/sys/devices/soc0/family文件来判断是否是Tegra平台。
	isTegra := logWithReason(infolib.IsTegraSystem, "Tegra")

	if !hasNVML && !isTegra {
		klog.Error("Incompatible platform detected")
		klog.Error("If this is a GPU node, did you configure the NVIDIA Container Toolkit?")
		klog.Error("You can check the prerequisites at: https://github.com/NVIDIA/k8s-device-plugin#prerequisites")
		klog.Error("You can learn how to set the runtime at: https://github.com/NVIDIA/k8s-device-plugin#quick-start")
		klog.Error("If this is not a GPU node, you should set up a toleration or nodeSelector to only deploy this plugin on GPU nodes")
		if *config.Flags.FailOnInitError {
			return nil, fmt.Errorf("platform detection failed")
		}
		return nil, nil
	}

	// The NVIDIA container stack does not yet support the use of integrated AND discrete GPUs on the same node.
	// 如果当前节点既支持NVML有是Tegra频台，那么默认使用Tegra
	if hasNVML && isTegra {
		klog.Warning("Disabling Tegra-based resources on NVML system")
		isTegra = false
	}

	var resourceManagers []ResourceManager

	if hasNVML {
		nvmlManagers, err := NewNVMLResourceManagers(nvmllib, config)
		if err != nil {
			return nil, fmt.Errorf("failed to construct NVML resource managers: %v", err)
		}
		resourceManagers = append(resourceManagers, nvmlManagers...)
	}

	if isTegra {
		tegraManagers, err := NewTegraResourceManagers(config)
		if err != nil {
			return nil, fmt.Errorf("failed to construct Tegra resource managers: %v", err)
		}
		resourceManagers = append(resourceManagers, tegraManagers...)
	}

	return resourceManagers, nil
}

// Resource gets the resource name associated with the ResourceManager
func (r *resourceManager) Resource() spec.ResourceName {
	return r.resource
}

// Resource gets the devices managed by the ResourceManager
func (r *resourceManager) Devices() Devices {
	return r.devices
}

// AddDefaultResourcesToConfig adds default resource matching rules to config.Resources
// 根据MIG的single, mixed不同的策略，设置不同的资源匹配规则，后续通过nvml库获取到的资源信息，就可以根据这些规则来匹配资源了。
func AddDefaultResourcesToConfig(config *nvidia.DeviceConfig) error {
	//config.Resources.AddGPUResource("*", "gpu")
	config.Resources.GPUs = append(config.Resources.GPUs, spec.Resource{
		Pattern: "*",
		Name:    spec.ResourceName(*config.ResourceName),
	})
	fmt.Println("config=", config.Resources.GPUs)
	switch *config.Flags.MigStrategy {
	case spec.MigStrategySingle: // 所谓MIG Single其实就是将一张GPU划分为多个相同规格的vGPU, 所有的vGPU都是相同的规格
		return config.Resources.AddMIGResource("*", "gpu")
	case spec.MigStrategyMixed: // 所谓MIG Mixed其实就是将一张GPU划分为多个相同规格的vGPU, 每个vGPU的规格可以不同
		// nvml即nvidia management library, 是NVIDIA提供的一个库, 用于管理和监控GPU
		// 这个库可以用来获取GPU的信息, 比如GPU的数量, GPU的型号, GPU的内存大小, GPU的显存大小, GPU的温度, GPU的功耗, GPU的利用率等等
		hasNVML, reason := info.New().HasNvml()
		if !hasNVML {
			klog.Warningf("mig-strategy=%q is only supported with NVML", spec.MigStrategyMixed)
			klog.Warningf("NVML not detected: %v", reason)
			return nil
		}

		nvmllib := nvml.New()
		ret := nvmllib.Init()
		if ret != nvml.SUCCESS {
			if *config.Flags.FailOnInitError {
				return fmt.Errorf("failed to initialize NVML: %v", ret)
			}
			return nil
		}
		defer func() {
			ret := nvmllib.Shutdown()
			if ret != nvml.SUCCESS {
				klog.Errorf("Error shutting down NVML: %v", ret)
			}
		}()

		devicelib := device.New(
			device.WithNvml(nvmllib),
		)
		return devicelib.VisitMigProfiles(func(p device.MigProfile) error {
			profileInfo := p.GetInfo()
			if profileInfo.C != profileInfo.G {
				return nil
			}
			// TODO 这里应该就是不同规格的GPU，可能需要查看英伟达GPU使用文档才知道这玩意的使用姿势
			resourceName := strings.ReplaceAll("mig-"+p.String(), "+", ".")
			return config.Resources.AddMIGResource(p.String(), resourceName)
		})
	}
	return nil
}
