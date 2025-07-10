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

package device

import (
	"context"
	"flag"
	"fmt"
	"os"
	"reflect"
	"strings"

	"github.com/Project-HAMi/HAMi/pkg/device/kunlun"

	"github.com/Project-HAMi/HAMi/pkg/device/ascend"
	"github.com/Project-HAMi/HAMi/pkg/device/cambricon"
	"github.com/Project-HAMi/HAMi/pkg/device/enflame"
	"github.com/Project-HAMi/HAMi/pkg/device/hygon"
	"github.com/Project-HAMi/HAMi/pkg/device/iluvatar"
	"github.com/Project-HAMi/HAMi/pkg/device/metax"
	"github.com/Project-HAMi/HAMi/pkg/device/mthreads"
	"github.com/Project-HAMi/HAMi/pkg/device/nvidia"
	"github.com/Project-HAMi/HAMi/pkg/util"
	"github.com/Project-HAMi/HAMi/pkg/util/client"
	"github.com/Project-HAMi/HAMi/pkg/util/nodelock"

	"gopkg.in/yaml.v2"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog/v2"
)

type Devices interface {
	// CommonWord 芯片类型，不同的芯片有不同的类型
	CommonWord() string
	// MutateAdmission Webhook中会调用这个函数，主要用于根据芯片的特点对Pod资源做校验，判断是否是当前管理的芯片。甚至修改动作
	MutateAdmission(ctr *corev1.Container, pod *corev1.Pod) (bool, error)
	// CheckHealth 检查当前节点是否健康,一般是通过节点的握手注解获取到的信息
	// 如果当前芯片类型的DP不是由HAMI提供的，那么无法获取节点的健康信息。因为健康信息是基于节点握手注解来的，第三方DP并没有上报握手注解
	CheckHealth(devType string, n *corev1.Node) (healthy bool, needUpdate bool)
	// 节点的握手信息设置为Delete_xxx_xxx
	NodeCleanUp(nn string) error
	// GetNodeDevices
	// 1. 从节点注解获取设备信息, 因此DP在上报的时候就需要当前芯片的类型上报合适的注解上来，表明当前节点的设备信息
	// 2. 获取节点上的设备一般有两种实现，一种是通过节点的注解信息获取，这种情况一般是HAMI自己实现的DP，这种情况下获取的设备信息i比较丰富，譬如
	// 内存，算力，支持的TimeSlice数量，健康状态等等。另外一种是直接从节点的NodeStatus中获取，这种一般是第三方的DP，因为第三方DP并没有上报
	// 设备信息到节点注解上。所以只能通过最基础的方式从节点状态中获取。本质上就是Kubelet通过ListAndWatch获取到的的设备信息。
	GetNodeDevices(n corev1.Node) ([]*util.DeviceInfo, error)
	// LockNode 给节点添加锁，当前调度器分配完成之后，会给节点加锁，表明自己当前需要给这个Pod分配设备信息，后续Kubelet调用DP Allocate接口分配设备的时候需要解锁
	LockNode(n *corev1.Node, p *corev1.Pod) error
	// ReleaseNodeLock 释放节点锁
	ReleaseNodeLock(n *corev1.Node, p *corev1.Pod) error
	// GenerateResourceRequests 解析容器申请的设备信息
	GenerateResourceRequests(ctr *corev1.Container) util.ContainerDeviceRequest
	// PatchAnnotations 调度器给Pod分配设备后，会调用这个函数，主要用于给Pod添加一些注解，表明当前Pod想要分配的设备信息
	PatchAnnotations(pod *corev1.Pod, annoinput *map[string]string, pd util.PodDevices) map[string]string
	ScoreNode(node *corev1.Node, podDevices util.PodSingleDevice, previous []*util.DeviceUsage, policy string) float32
	// 记录当前设备的使用情况，譬如内存使用情况，core使用情况
	AddResourceUsage(pod *corev1.Pod, n *util.DeviceUsage, ctr *util.ContainerDevice) error
	Fit(devices []*util.DeviceUsage, request util.ContainerDeviceRequest, annos map[string]string, pod *corev1.Pod, allocated *util.PodDevices) (bool, map[string]util.ContainerDevices, string)
	// This should not be associated with a specific device object
	//ParseConfig(fs *flag.FlagSet)
}

/* 各种类型的设备配置如下
   nvidia:
     resourceCountName: nvidia.com/gpu
     resourceMemoryName: nvidia.com/gpumem
     resourceMemoryPercentageName: nvidia.com/gpumem-percentage
     resourceCoreName: nvidia.com/gpucores
     resourcePriorityName: nvidia.com/priority
     overwriteEnv: false
     defaultMemory: 0
     defaultCores: 0
     defaultGPUNum: 1
     deviceSplitCount: 10
     deviceMemoryScaling: 1
     deviceCoreScaling: 1
     gpuCorePolicy: default
     knownMigGeometries:
     - models: [ "A30" ]
       allowedGeometries:
         -
           - name: 1g.6gb
             memory: 6144
             count: 4
         -
           - name: 2g.12gb
             memory: 12288
             count: 2
         -
           - name: 4g.24gb
             memory: 24576
             count: 1
     - models: [ "A100-SXM4-40GB", "A100-40GB-PCIe", "A100-PCIE-40GB", "A100-SXM4-40GB" ]
       allowedGeometries:
         -
           - name: 1g.5gb
             memory: 5120
             count: 7
         -
           - name: 2g.10gb
             memory: 10240
             count: 3
           - name: 1g.5gb
             memory: 5120
             count: 1
         -
           - name: 3g.20gb
             memory: 20480
             count: 2
         -
           - name: 7g.40gb
             memory: 40960
             count: 1
     - models: [ "A100-SXM4-80GB", "A100-80GB-PCIe", "A100-PCIE-80GB"]
       allowedGeometries:
         -
           - name: 1g.10gb
             memory: 10240
             count: 7
         -
           - name: 2g.20gb
             memory: 20480
             count: 3
           - name: 1g.10gb
             memory: 10240
             count: 1
         -
           - name: 3g.40gb
             memory: 40960
             count: 2
         -
           - name: 7g.79gb
             memory: 80896
             count: 1
   cambricon:
     resourceCountName: cambricon.com/vmlu
     resourceMemoryName: cambricon.com/mlu.smlu.vmemory
     resourceCoreName: cambricon.com/mlu.smlu.vcore
   hygon:
     resourceCountName: hygon.com/dcunum
     resourceMemoryName: hygon.com/dcumem
     resourceCoreName: hygon.com/dcucores
   metax:
     resourceCountName: "metax-tech.com/gpu"
     resourceVCountName: metax-tech.com/sgpu
     resourceVMemoryName: metax-tech.com/vmemory
     resourceVCoreName: metax-tech.com/vcore
   enflame:
     resourceCountName: "enflame.com/vgcu"
     resourcePercentageName: "enflame.com/vgcu-percentage"
   mthreads:
     resourceCountName: "mthreads.com/vgpu"
     resourceMemoryName: "mthreads.com/sgpu-memory"
     resourceCoreName: "mthreads.com/sgpu-core"
   iluvatar:
     resourceCountName: iluvatar.ai/vgpu
     resourceMemoryName: iluvatar.ai/vcuda-memory
     resourceCoreName: iluvatar.ai/vcuda-core
   vnpus:
   - chipName: 910B
     commonWord: Ascend910A
     resourceName: huawei.com/Ascend910A
     resourceMemoryName: huawei.com/Ascend910A-memory
     memoryAllocatable: 32768
     memoryCapacity: 32768
     aiCore: 30
     templates:
       - name: vir02
         memory: 2184
         aiCore: 2
       - name: vir04
         memory: 4369
         aiCore: 4
       - name: vir08
         memory: 8738
         aiCore: 8
       - name: vir16
         memory: 17476
         aiCore: 16
   - chipName: 910B2
     commonWord: Ascend910B2
     resourceName: huawei.com/Ascend910B2
     resourceMemoryName: huawei.com/Ascend910B2-memory
     memoryAllocatable: 65536
     memoryCapacity: 65536
     aiCore: 24
     aiCPU: 6
     templates:
       - name: vir03_1c_8g
         memory: 8192
         aiCore: 3
         aiCPU: 1
       - name: vir06_1c_16g
         memory: 16384
         aiCore: 6
         aiCPU: 1
       - name: vir12_3c_32g
         memory: 32768
         aiCore: 12
         aiCPU: 3
   - chipName: 910B3
     commonWord: Ascend910B
     resourceName: huawei.com/Ascend910B
     resourceMemoryName: huawei.com/Ascend910B-memory
     memoryAllocatable: 65536
     memoryCapacity: 65536
     aiCore: 20
     aiCPU: 7
     templates:
       - name: vir05_1c_16g
         memory: 16384
         aiCore: 5
         aiCPU: 1
       - name: vir10_3c_32g
         memory: 32768
         aiCore: 10
         aiCPU: 3
   - chipName: 910B4
     commonWord: Ascend910B4
     resourceName: huawei.com/Ascend910B4
     resourceMemoryName: huawei.com/Ascend910B4-memory
     memoryAllocatable: 32768
     memoryCapacity: 32768
     aiCore: 20
     aiCPU: 7
     templates:
       - name: vir05_1c_8g
         memory: 8192
         aiCore: 5
         aiCPU: 1
       - name: vir10_3c_16g
         memory: 16384
         aiCore: 10
         aiCPU: 3
   - chipName: 310P3
     commonWord: Ascend310P
     resourceName: huawei.com/Ascend310P
     resourceMemoryName: huawei.com/Ascend310P-memory
     memoryAllocatable: 21527
     memoryCapacity: 24576
     aiCore: 8
     aiCPU: 7
     templates:
       - name: vir01
         memory: 3072
         aiCore: 1
         aiCPU: 1
       - name: vir02
         memory: 6144
         aiCore: 2
         aiCPU: 2
       - name: vir04
         memory: 12288
         aiCore: 4
         aiCPU: 4
*/

type Config struct {
	NvidiaConfig    nvidia.NvidiaConfig       `yaml:"nvidia"`
	MetaxConfig     metax.MetaxConfig         `yaml:"metax"`
	HygonConfig     hygon.HygonConfig         `yaml:"hygon"`
	CambriconConfig cambricon.CambriconConfig `yaml:"cambricon"`
	MthreadsConfig  mthreads.MthreadsConfig   `yaml:"mthreads"`
	IluvatarConfig  iluvatar.IluvatarConfig   `yaml:"iluvatar"`
	EnflameConfig   enflame.EnflameConfig     `yaml:"enflame"`
	KunlunConfig    kunlun.KunlunConfig       `yaml:"kunlun"`
	VNPUs           []ascend.VNPUConfig       `yaml:"vnpus"`
}

var (
	HandshakeAnnos = map[string]string{}
	RegisterAnnos  = map[string]string{}
	// key为不同类型的设备，value为设备
	devicesMap      map[string]Devices
	DevicesToHandle []string
	configFile      string
	DebugMode       bool
)

func GetDevices() map[string]Devices {
	return devicesMap
}

// InitDevicesWithConfig 初始化各种类型的设备预分配设备注解，分配设备主机，以及握手注解使用的名字
func InitDevicesWithConfig(config *Config) error {
	if err := validateConfig(config); err != nil {
		klog.Errorf("Invalid configuration: %v", err)
		return err
	}

	klog.Info("Initializing devices with configuration")

	devicesMap = make(map[string]Devices)
	DevicesToHandle = []string{}
	var initErrors []error

	// Helper function to initialize devices and handle errors
	initializeDevice := func(deviceType string, commonWord string, initFunc func(any) (Devices, error), config any) {
		klog.Infof("Initializing %s device", commonWord)
		device, err := initFunc(config)
		if err != nil {
			klog.Errorf("Failed to initialize %s device: %v", commonWord, err)
			initErrors = append(initErrors, fmt.Errorf("%s: %v", commonWord, err))
			return
		}
		devicesMap[deviceType] = device
		DevicesToHandle = append(DevicesToHandle, commonWord)
		klog.Infof("%s device initialized successfully", commonWord)
	}

	// Wrapper for each device's initialization function to include type assertion check
	deviceInitializers := []struct {
		deviceType string
		commonWord string
		initFunc   func(any) (Devices, error)
		config     any
	}{
		{nvidia.NvidiaGPUDevice, nvidia.NvidiaGPUCommonWord, func(cfg any) (Devices, error) {
			nvidiaConfig, ok := cfg.(nvidia.NvidiaConfig)
			if !ok {
				return nil, fmt.Errorf("invalid configuration for %s", nvidia.NvidiaGPUCommonWord)
			}
			return nvidia.InitNvidiaDevice(nvidiaConfig), nil
		}, config.NvidiaConfig},
		{cambricon.CambriconMLUDevice, cambricon.CambriconMLUCommonWord, func(cfg any) (Devices, error) {
			cambriconConfig, ok := cfg.(cambricon.CambriconConfig)
			if !ok {
				return nil, fmt.Errorf("invalid configuration for %s", cambricon.CambriconMLUCommonWord)
			}
			return cambricon.InitMLUDevice(cambriconConfig), nil
		}, config.CambriconConfig},
		{hygon.HygonDCUDevice, hygon.HygonDCUCommonWord, func(cfg any) (Devices, error) {
			hygonConfig, ok := cfg.(hygon.HygonConfig)
			if !ok {
				return nil, fmt.Errorf("invalid configuration for %s", hygon.HygonDCUCommonWord)
			}
			return hygon.InitDCUDevice(hygonConfig), nil
		}, config.HygonConfig},
		{iluvatar.IluvatarGPUDevice, iluvatar.IluvatarGPUCommonWord, func(cfg any) (Devices, error) {
			iluvatarConfig, ok := cfg.(iluvatar.IluvatarConfig)
			if !ok {
				return nil, fmt.Errorf("invalid configuration for %s", iluvatar.IluvatarGPUCommonWord)
			}
			return iluvatar.InitIluvatarDevice(iluvatarConfig), nil
		}, config.IluvatarConfig},
		{enflame.EnflameGPUDevice, enflame.EnflameGPUCommonWord, func(cfg any) (Devices, error) {
			enflameConfig, ok := cfg.(enflame.EnflameConfig)
			if !ok {
				return nil, fmt.Errorf("invalid configuration for %s", enflame.EnflameGPUCommonWord)
			}
			return enflame.InitEnflameDevice(enflameConfig), nil
		}, config.EnflameConfig},
		{mthreads.MthreadsGPUDevice, mthreads.MthreadsGPUCommonWord, func(cfg any) (Devices, error) {
			mthreadsConfig, ok := cfg.(mthreads.MthreadsConfig)
			if !ok {
				return nil, fmt.Errorf("invalid configuration for %s", mthreads.MthreadsGPUCommonWord)
			}
			return mthreads.InitMthreadsDevice(mthreadsConfig), nil
		}, config.MthreadsConfig},
		{metax.MetaxGPUDevice, metax.MetaxGPUCommonWord, func(cfg any) (Devices, error) {
			metaxConfig, ok := cfg.(metax.MetaxConfig)
			if !ok {
				return nil, fmt.Errorf("invalid configuration for %s", metax.MetaxGPUCommonWord)
			}
			return metax.InitMetaxDevice(metaxConfig), nil
		}, config.MetaxConfig},
		{metax.MetaxSGPUDevice, metax.MetaxSGPUCommonWord, func(cfg any) (Devices, error) {
			metaxConfig, ok := cfg.(metax.MetaxConfig)
			if !ok {
				return nil, fmt.Errorf("invalid configuration for %s", metax.MetaxGPUCommonWord)
			}
			return metax.InitMetaxSDevice(metaxConfig), nil
		}, config.MetaxConfig},
		{kunlun.KunlunGPUDevice, kunlun.KunlunGPUCommonWord, func(cfg any) (Devices, error) {
			kunlunConfig, ok := cfg.(kunlun.KunlunConfig)
			if !ok {
				return nil, fmt.Errorf("invalid configuration for %s", kunlun.KunlunGPUCommonWord)
			}
			return kunlun.InitKunlunDevice(kunlunConfig), nil
		}, config.KunlunConfig},
	}

	// Initialize all devices using the wrapped functions
	for _, initializer := range deviceInitializers {
		initializeDevice(initializer.deviceType, initializer.commonWord, initializer.initFunc, initializer.config)
	}

	// Initialize Ascend devices 不同型号的昇腾设备的名字初始化
	for _, dev := range ascend.InitDevices(config.VNPUs) {
		commonWord := dev.CommonWord()
		devicesMap[commonWord] = dev
		DevicesToHandle = append(DevicesToHandle, commonWord)
		klog.Infof("Ascend device %s initialized", commonWord)
	}

	if len(initErrors) > 0 {
		return fmt.Errorf("errors occurred during initialization: %v", initErrors)
	}

	klog.Info("All devices initialized successfully")
	return nil
}

func InitDevices() {
	if len(devicesMap) > 0 {
		klog.Info("Devices are already initialized, skipping initialization")
		return
	}
	// 一般配置为：/device-config.yaml
	klog.Infof("Loading device configuration from file: %s", configFile)
	config, err := LoadConfig(configFile)
	if err != nil {
		klog.Fatalf("Failed to load device config file %s: %v", configFile, err)
	}
	klog.Infof("Loaded config: %v", config)
	err = InitDevicesWithConfig(config)
	if err != nil {
		klog.Fatalf("Failed to initialize devices: %v", err)
	}
}

func InitDefaultDevices() {
	configMapdata := `
nvidia:
  resourceCountName: "nvidia.com/gpu"
  resourceMemoryName: "nvidia.com/gpumem"
  resourceMemoryPercentageName: "nvidia.com/gpumem-percentage"
  resourceCoreName: "nvidia.com/gpucores"
  resourcePriorityName: "nvidia.com/priority"
  overwriteEnv: false
  defaultMemory: 0
  defaultCores: 0
  defaultGPUNum: 1
cambricon:
  resourceCountName: "cambricon.com/vmlu"
  resourceMemoryName: "cambricon.com/mlu.smlu.vmemory"
  resourceCoreName: "cambricon.com/mlu.smlu.vcore"
hygon:
  resourceCountName: "hygon.com/dcunum"
  resourceMemoryName: "hygon.com/dcumem"
  resourceCoreName: "hygon.com/dcucores"
metax:
  resourceCountName: "metax-tech.com/gpu"
mthreads:
  resourceCountName: "mthreads.com/vgpu"
  resourceMemoryName: "mthreads.com/sgpu-memory"
  resourceCoreName: "mthreads.com/sgpu-core"
iluvatar: 
  resourceCountName: "iluvatar.ai/vgpu"
  resourceMemoryName: "iluvatar.ai/vcuda-memory"
  resourceCoreName: "iluvatar.ai/vcuda-core"
kunlun:
  resourceCountName: "kunlunxin.com/xpu"
vnpus:
  - chipName: "910B"
    commonWord: "Ascend910A"
    resourceName: "huawei.com/Ascend910A"
    resourceMemoryName: "huawei.com/Ascend910A-memory"
    memoryAllocatable: 32768
    memoryCapacity: 32768
    aiCore: 30
    templates:
      - name: "vir02"
        memory: 2184
        aiCore: 2
      - name: "vir04"
        memory: 4369
        aiCore: 4
      - name: "vir08"
        memory: 8738
        aiCore: 8
      - name: "vir16"
        memory: 17476
        aiCore: 16
  - chipName: 910B2
    commonWord: Ascend910B2
    resourceName: huawei.com/Ascend910B2
    resourceMemoryName: huawei.com/Ascend910B2-memory
    memoryAllocatable: 65536
    memoryCapacity: 65536
    aiCore: 24
    aiCPU: 6
    templates:
      - name: vir03_1c_8g
        memory: 8192
        aiCore: 3
        aiCPU: 1
      - name: vir06_1c_16g
        memory: 16384
        aiCore: 6
        aiCPU: 1
      - name: vir12_3c_32g
        memory: 32768
        aiCore: 12
        aiCPU: 3
  - chipName: "910B3"
    commonWord: "Ascend910B"
    resourceName: "huawei.com/Ascend910B"
    resourceMemoryName: "huawei.com/Ascend910B-memory"
    memoryAllocatable: 65536
    memoryCapacity: 65536
    aiCore: 20
    aiCPU: 7
    templates:
      - name: "vir05_1c_16g"
        memory: 16384
        aiCore: 5
        aiCPU: 1
      - name: "vir10_3c_32g"
        memory: 32768
        aiCore: 10
        aiCPU: 3
  - chipName: 910B4
    commonWord: Ascend910B4
    resourceName: huawei.com/Ascend910B4
    resourceMemoryName: huawei.com/Ascend910B4-memory
    memoryAllocatable: 32768
    memoryCapacity: 32768
    aiCore: 20
    aiCPU: 7
    templates:
      - name: vir05_1c_8g
        memory: 8192
        aiCore: 5
        aiCPU: 1
      - name: vir10_3c_16g
        memory: 16384
        aiCore: 10
        aiCPU: 3
  - chipName: "310P3"
    commonWord: "Ascend310P"
    resourceName: "huawei.com/Ascend310P"
    resourceMemoryName: "huawei.com/Ascend310P-memory"
    memoryAllocatable: 21527
    memoryCapacity: 24576
    aiCore: 8
    aiCPU: 7
    templates:
      - name: "vir01"
        memory: 3072
        aiCore: 1
        aiCPU: 1
      - name: "vir02"
        memory: 6144
        aiCore: 2
        aiCPU: 2
      - name: "vir04"
        memory: 12288
        aiCore: 4
        aiCPU: 4`

	var yamlData Config
	err := yaml.Unmarshal([]byte(configMapdata), &yamlData)
	if err != nil {
		klog.Fatalf("Failed to unmarshal default config: %v", err)
		return
	}

	// Initialize devices with configuration
	if err := InitDevicesWithConfig(&yamlData); err != nil {
		klog.Fatalf("Failed to initialize devices with default config: %v", err)
	}
}

// 当前Pod分配了
func PodAllocationTrySuccess(nodeName string, devName string, lockName string, pod *corev1.Pod) {
	refreshed, err := client.GetClient().CoreV1().Pods(pod.Namespace).Get(context.Background(), pod.Name, metav1.GetOptions{})
	if err != nil {
		klog.Errorf("Error getting pod %s/%s: %v", pod.Namespace, pod.Name, err)
		return
	}
	annos := refreshed.Annotations[util.InRequestDevices[devName]]
	klog.Infof("Trying allocation success: %s", annos)
	for _, val := range DevicesToHandle {
		if strings.Contains(annos, val) {
			return
		}
	}
	klog.Infof("All devices allocate success, releasing lock")
	PodAllocationSuccess(nodeName, pod, lockName)
}

func updatePodAnnotationsAndReleaseLock(nodeName string, pod *corev1.Pod, lockName string, deviceBindPhase string) {
	newAnnos := map[string]string{util.DeviceBindPhase: deviceBindPhase}
	if err := util.PatchPodAnnotations(pod, newAnnos); err != nil {
		klog.Errorf("Failed to patch pod annotations for pod %s/%s: %v", pod.Namespace, pod.Name, err)
		return
	}
	if err := nodelock.ReleaseNodeLock(nodeName, lockName, pod, false); err != nil {
		klog.Errorf("Failed to release node lock for node %s and lock %s: %v", nodeName, lockName, err)
	}
}

func PodAllocationSuccess(nodeName string, pod *corev1.Pod, lockName string) {
	klog.Infof("Pod allocation successful for pod %s/%s on node %s", pod.Namespace, pod.Name, nodeName)
	updatePodAnnotationsAndReleaseLock(nodeName, pod, lockName, util.DeviceBindSuccess)
}

func PodAllocationFailed(nodeName string, pod *corev1.Pod, lockName string) {
	klog.Infof("Pod allocation failed for pod %s/%s on node %s", pod.Namespace, pod.Name, nodeName)
	updatePodAnnotationsAndReleaseLock(nodeName, pod, lockName, util.DeviceBindFailed)
}

func GlobalFlagSet() *flag.FlagSet {
	fs := flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	ascend.ParseConfig(fs)
	cambricon.ParseConfig(fs)
	hygon.ParseConfig(fs)
	iluvatar.ParseConfig(fs)
	nvidia.ParseConfig(fs)
	mthreads.ParseConfig(fs)
	enflame.ParseConfig(fs)
	metax.ParseConfig(fs)
	kunlun.ParseConfig(fs)
	fs.BoolVar(&DebugMode, "debug", false, "Enable debug mode")
	// 设备配置文件
	fs.StringVar(&configFile, "device-config-file", "", "Path to the device config file")
	klog.InitFlags(fs)
	return fs
}

func LoadConfig(path string) (*Config, error) {
	klog.Infof("Reading config file from path: %s", path)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var yamlData Config
	if err := yaml.Unmarshal(data, &yamlData); err != nil {
		return nil, err
	}
	klog.Info("Successfully read and parsed config file")
	return &yamlData, nil
}

// validateConfig validates the configuration object to ensure it is complete.
func validateConfig(config *Config) error {
	var hasAnyConfig bool

	hasAnyConfig = hasAnyConfig || !reflect.DeepEqual(config.NvidiaConfig, nvidia.NvidiaConfig{})
	hasAnyConfig = hasAnyConfig || !reflect.DeepEqual(config.CambriconConfig, cambricon.CambriconConfig{})
	hasAnyConfig = hasAnyConfig || !reflect.DeepEqual(config.HygonConfig, hygon.HygonConfig{})
	hasAnyConfig = hasAnyConfig || !reflect.DeepEqual(config.IluvatarConfig, iluvatar.IluvatarConfig{})
	hasAnyConfig = hasAnyConfig || !reflect.DeepEqual(config.MthreadsConfig, mthreads.MthreadsConfig{})
	hasAnyConfig = hasAnyConfig || !reflect.DeepEqual(config.MetaxConfig, metax.MetaxConfig{})
	hasAnyConfig = hasAnyConfig || !reflect.DeepEqual(config.KunlunConfig, kunlun.KunlunConfig{})
	hasAnyConfig = hasAnyConfig || len(config.VNPUs) > 0

	if !hasAnyConfig {
		return fmt.Errorf("all configurations are empty")
	}
	return nil
}
