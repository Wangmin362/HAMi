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

package ascend

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Project-HAMi/HAMi/pkg/device/common"
	"github.com/Project-HAMi/HAMi/pkg/util"
	"github.com/Project-HAMi/HAMi/pkg/util/nodelock"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/klog/v2"
)

const (
	NodeLockAscend = "hami.io/mutex.lock"
)

type Devices struct {
	config           VNPUConfig
	nodeRegisterAnno string // 节点上注册设备注解的名称
	useUUIDAnno      string // 标记用户想要使用的显卡UUID的注解名称
	noUseUUIDAnno    string // 标记用户不想使用的显卡UUID的注解名称
	handshakeAnno    string // 标记节点握手完成的注解名称
}

type RuntimeInfo struct {
	UUID string `json:"UUID,omitempty"`
	Temp string `json:"temp,omitempty"`
}

var (
	enableAscend bool
	configFile   string
)

func (dev *Devices) trimMemory(m int64) (int64, string) {
	for i := range dev.config.Templates {
		// 如果能够找到，直接按照虚卡分配
		if m <= dev.config.Templates[i].Memory {
			return dev.config.Templates[i].Memory, dev.config.Templates[i].Name
		}
	}
	// 这种情况就是整卡分配
	if m <= dev.config.MemoryCapacity {
		return dev.config.MemoryAllocatable, ""
	}
	return 0, ""
}

func InitDevices(config []VNPUConfig) []*Devices {
	var devs []*Devices
	if !enableAscend {
		return devs
	}
	for _, vnpu := range config {
		commonWord := vnpu.CommonWord
		dev := &Devices{
			config:           vnpu,
			nodeRegisterAnno: fmt.Sprintf("hami.io/node-register-%s", commonWord),
			useUUIDAnno:      fmt.Sprintf("hami.io/use-%s-uuid", commonWord),
			noUseUUIDAnno:    fmt.Sprintf("hami.io/no-use-%s-uuid", commonWord),
			handshakeAnno:    fmt.Sprintf("hami.io/node-handshake-%s", commonWord),
		}
		// 模板按照从小到大排序
		sort.Slice(dev.config.Templates, func(i, j int) bool {
			return dev.config.Templates[i].Memory < dev.config.Templates[j].Memory
		})
		util.InRequestDevices[commonWord] = fmt.Sprintf("hami.io/%s-devices-to-allocate", commonWord)
		util.SupportDevices[commonWord] = fmt.Sprintf("hami.io/%s-devices-allocated", commonWord)
		util.HandshakeAnnos[commonWord] = dev.handshakeAnno
		devs = append(devs, dev)
		klog.Infof("load ascend vnpu config %s: %v", commonWord, dev.config)
	}
	return devs
}

func ParseConfig(fs *flag.FlagSet) {
	// TODO 如果为false，结果会怎样？
	fs.BoolVar(&enableAscend, "enable-ascend", false, "enable ascend device")
}

func (dev *Devices) CommonWord() string {
	return dev.config.CommonWord
}

func (dev *Devices) MutateAdmission(ctr *corev1.Container, p *corev1.Pod) (bool, error) {
	// ResourceName譬如是：huawei.com/Ascend310P， huawei.com/Ascend910
	count, ok := ctr.Resources.Limits[corev1.ResourceName(dev.config.ResourceName)]
	if !ok {
		return false, nil
	}
	trimMem := dev.config.MemoryAllocatable
	// ResourceMemoryName譬如是：huawei.com/Ascend310P-memory， huawei.com/Ascend910-memory
	memory, ok := ctr.Resources.Limits[corev1.ResourceName(dev.config.ResourceMemoryName)]
	if ok {
		// 找到一个最接近的模板的大小
		trimMem, _ = dev.trimMemory(memory.Value())
		// TODO 这里打印一下使用的模板会比较好，以及提示修改了显存大小
		if trimMem <= 0 {
			return false, fmt.Errorf("%s %d is invalid", dev.config.ResourceMemoryName, memory.Value())
		}
	}
	// 虚卡不支持同时分配多个设备
	if count.Value() > 1 {
		if trimMem != dev.config.MemoryAllocatable {
			return true, errors.New("vNPU nor supported for multiple devices")
		}
	}
	// 重新设置一个合适的显存
	ctr.Resources.Limits[corev1.ResourceName(dev.config.ResourceMemoryName)] = resource.MustParse(fmt.Sprint(trimMem))
	ctr.Resources.Requests[corev1.ResourceName(dev.config.ResourceMemoryName)] = resource.MustParse(fmt.Sprint(trimMem))
	return true, nil
}

func (dev *Devices) GetNodeDevices(n corev1.Node) ([]*util.DeviceInfo, error) {
	anno, ok := n.Annotations[dev.nodeRegisterAnno]
	if !ok {
		return []*util.DeviceInfo{}, fmt.Errorf("annos not found %s", dev.nodeRegisterAnno)
	}
	nodeDevices, err := util.UnMarshalNodeDevices(anno)
	if err != nil {
		klog.ErrorS(err, "failed to unmarshal node devices", "node", n.Name, "device annotation", anno)
		return []*util.DeviceInfo{}, err
	}
	if len(nodeDevices) == 0 {
		klog.InfoS("no gpu device found", "node", n.Name, "device annotation", anno)
		return []*util.DeviceInfo{}, errors.New("no device found on node")
	}
	return nodeDevices, nil
}

func (dev *Devices) PatchAnnotations(pod *corev1.Pod, annoInput *map[string]string, pd util.PodDevices) map[string]string {
	commonWord := dev.CommonWord()
	devList, ok := pd[commonWord]
	if ok && len(devList) > 0 {
		(*annoInput)[util.InRequestDevices[commonWord]] = util.EncodePodSingleDevice(devList)
		(*annoInput)[util.SupportDevices[commonWord]] = util.EncodePodSingleDevice(devList)
		(*annoInput)["predicate-time"] = strconv.FormatInt(time.Now().Unix(), 10)
		// 表明分配的设备已经模板
		allocateStr := fmt.Sprintf("huawei.com/%s", dev.CommonWord())
		var rtInfo []RuntimeInfo
		for _, dp := range devList {
			for _, val := range dp {
				_, temp := dev.trimMemory(int64(val.Usedmem))
				rtInfo = append(rtInfo, RuntimeInfo{
					UUID: val.UUID,
					Temp: temp,
				})
			}
		}
		s, err := json.Marshal(rtInfo)
		if err != nil {
			klog.ErrorS(err, "failed to marshal runtime info", "runtime info", rtInfo)
		}
		(*annoInput)[allocateStr] = string(s)
	}
	return *annoInput
}

func (dev *Devices) LockNode(n *corev1.Node, p *corev1.Pod) error {
	found := false
	for _, val := range p.Spec.Containers {
		// 解析容器申请的设备信息, 如果发现是昇腾设备了，就需要进行加锁
		if (dev.GenerateResourceRequests(&val).Nums) > 0 {
			found = true
			break
		}
	}
	if !found {
		return nil
	}

	return nodelock.LockNode(n.Name, NodeLockAscend, p)
}

func (dev *Devices) ReleaseNodeLock(n *corev1.Node, p *corev1.Pod) error {
	found := false
	for _, val := range p.Spec.Containers {
		if (dev.GenerateResourceRequests(&val).Nums) > 0 {
			found = true
			break
		}
	}
	if !found {
		return nil
	}

	return nodelock.ReleaseNodeLock(n.Name, NodeLockAscend, p, false)
}

// 节点的握手信息设置为Delete_xxx_xxx
func (dev *Devices) NodeCleanUp(nn string) error {
	return util.MarkAnnotationsToDelete(dev.handshakeAnno, nn)
}

func (dev *Devices) checkType(annos map[string]string, d util.DeviceUsage, n util.ContainerDeviceRequest) (bool, bool, bool) {
	if strings.Compare(n.Type, dev.CommonWord()) == 0 {
		return true, true, false
	}
	return false, false, false
}

// 用于判断当前Pod是否有指定需要使用卡的UUID或者不想使用的卡的UUID
func (dev *Devices) checkUUID(annos map[string]string, d util.DeviceUsage) bool {
	userUUID, ok := annos[dev.useUUIDAnno]
	if ok {
		klog.V(5).Infof("check uuid for ascend user uuid [%s], device id is %s", userUUID, d.ID)
		// use , symbol to connect multiple uuid
		userUUIDs := strings.Split(userUUID, ",")
		return slices.Contains(userUUIDs, d.ID)
	}

	noUserUUID, ok := annos[dev.noUseUUIDAnno]
	if ok {
		klog.V(5).Infof("check uuid for ascend not user uuid [%s], device id is %s", noUserUUID, d.ID)
		// use , symbol to connect multiple uuid
		noUserUUIDs := strings.Split(noUserUUID, ",")
		return !slices.Contains(noUserUUIDs, d.ID)
	}
	return true
}

func (dev *Devices) CheckHealth(devType string, n *corev1.Node) (bool, bool) {
	return util.CheckHealth(devType, n)
}

// 解析容器申请的设备信息
func (dev *Devices) GenerateResourceRequests(ctr *corev1.Container) util.ContainerDeviceRequest {
	klog.Infof("Counting %s devices", dev.config.CommonWord)
	// 类似huawei.com/Ascend310P: 2
	ascendResourceCount := corev1.ResourceName(dev.config.ResourceName)
	// 类似huawei.com/Ascend310P-memory: 16384
	ascendResourceMem := corev1.ResourceName(dev.config.ResourceMemoryName)
	v, ok := ctr.Resources.Limits[ascendResourceCount]
	if !ok {
		v, ok = ctr.Resources.Requests[ascendResourceCount]
	}
	if ok {
		if n, ok := v.AsInt64(); ok {
			klog.Info("Found AscendDevices devices")
			memnum := 0
			mem, ok := ctr.Resources.Limits[ascendResourceMem]
			if !ok {
				mem, ok = ctr.Resources.Requests[ascendResourceMem]
			}
			if ok {
				memnums, ok := mem.AsInt64()
				if ok {
					m, _ := dev.trimMemory(memnums)
					memnum = int(m)
				}
			}
			corenum := int32(0)

			mempnum := 0
			if memnum == 0 {
				mempnum = 100
			}

			return util.ContainerDeviceRequest{
				Nums:             int32(n),
				Type:             dev.CommonWord(),
				Memreq:           int32(memnum),
				MemPercentagereq: int32(mempnum),
				Coresreq:         corenum,
			}
		}
	}
	return util.ContainerDeviceRequest{}
}

// TODO 为什么这里不需要实现节点打分？
func (dev *Devices) ScoreNode(node *corev1.Node, podDevices util.PodSingleDevice, previous []*util.DeviceUsage, policy string) float32 {
	return 0
}

// 记录当前设备的使用情况，譬如内存使用情况，core使用情况
func (dev *Devices) AddResourceUsage(pod *corev1.Pod, n *util.DeviceUsage, ctr *util.ContainerDevice) error {
	n.Used++
	n.Usedcores += ctr.Usedcores
	n.Usedmem += ctr.Usedmem
	return nil
}

func (npu *Devices) Fit(devices []*util.DeviceUsage, request util.ContainerDeviceRequest, annos map[string]string, pod *corev1.Pod, allocated *util.PodDevices) (bool, map[string]util.ContainerDevices, string) {
	k := request
	originReq := k.Nums
	prevnuma := -1
	klog.InfoS("Allocating device for container request", "pod", klog.KObj(pod), "card request", k)
	var tmpDevs map[string]util.ContainerDevices
	tmpDevs = make(map[string]util.ContainerDevices)
	reason := make(map[string]int)
	for i := len(devices) - 1; i >= 0; i-- {
		dev := devices[i]
		klog.V(4).InfoS("scoring pod", "pod", klog.KObj(pod), "device", dev.ID, "Memreq", k.Memreq, "MemPercentagereq", k.MemPercentagereq, "Coresreq", k.Coresreq, "Nums", k.Nums, "device index", i)

		_, found, numa := npu.checkType(annos, *dev, k)
		if !found {
			reason[common.CardTypeMismatch]++
			klog.V(5).InfoS(common.CardTypeMismatch, "pod", klog.KObj(pod), "device", dev.ID, dev.Type, k.Type)
			continue
		}
		if numa && prevnuma != dev.Numa {
			if k.Nums != originReq {
				reason[common.NumaNotFit] += len(tmpDevs)
				klog.V(5).InfoS(common.NumaNotFit, "pod", klog.KObj(pod), "device", dev.ID, "k.nums", k.Nums, "numa", numa, "prevnuma", prevnuma, "device numa", dev.Numa)
			}
			k.Nums = originReq
			prevnuma = dev.Numa
			tmpDevs = make(map[string]util.ContainerDevices)
		}
		if !npu.checkUUID(annos, *dev) {
			reason[common.CardUUIDMismatch]++
			klog.V(5).InfoS(common.CardUUIDMismatch, "pod", klog.KObj(pod), "device", dev.ID, "current device info is:", *dev)
			continue
		}

		memreq := int32(0)
		if dev.Count <= dev.Used {
			reason[common.CardTimeSlicingExhausted]++
			klog.V(5).InfoS(common.CardTimeSlicingExhausted, "pod", klog.KObj(pod), "device", dev.ID, "count", dev.Count, "used", dev.Used)
			continue
		}
		if k.Coresreq > 100 {
			klog.ErrorS(nil, "core limit can't exceed 100", "pod", klog.KObj(pod), "device", dev.ID)
			k.Coresreq = 100
			//return false, tmpDevs
		}
		if k.Memreq > 0 {
			memreq = k.Memreq
		}
		if k.MemPercentagereq != 101 && k.Memreq == 0 {
			//This incurs an issue
			memreq = dev.Totalmem * k.MemPercentagereq / 100
		}
		if dev.Totalmem-dev.Usedmem < memreq {
			reason[common.CardInsufficientMemory]++
			klog.V(5).InfoS(common.CardInsufficientMemory, "pod", klog.KObj(pod), "device", dev.ID, "device index", i, "device total memory", dev.Totalmem, "device used memory", dev.Usedmem, "request memory", memreq)
			continue
		}
		if dev.Totalcore-dev.Usedcores < k.Coresreq {
			reason[common.CardInsufficientCore]++
			klog.V(5).InfoS(common.CardInsufficientCore, "pod", klog.KObj(pod), "device", dev.ID, "device index", i, "device total core", dev.Totalcore, "device used core", dev.Usedcores, "request cores", k.Coresreq)
			continue
		}
		// Coresreq=100 indicates it want this card exclusively
		if dev.Totalcore == 100 && k.Coresreq == 100 && dev.Used > 0 {
			reason[common.ExclusiveDeviceAllocateConflict]++
			klog.V(5).InfoS(common.ExclusiveDeviceAllocateConflict, "pod", klog.KObj(pod), "device", dev.ID, "device index", i, "used", dev.Used)
			continue
		}
		// You can't allocate core=0 job to an already full GPU
		if dev.Totalcore != 0 && dev.Usedcores == dev.Totalcore && k.Coresreq == 0 {
			reason[common.CardComputeUnitsExhausted]++
			klog.V(5).InfoS(common.CardComputeUnitsExhausted, "pod", klog.KObj(pod), "device", dev.ID, "device index", i)
			continue
		}
		if k.Nums > 0 {
			klog.V(5).InfoS("find fit device", "pod", klog.KObj(pod), "device", dev.ID)
			k.Nums--
			tmpDevs[k.Type] = append(tmpDevs[k.Type], util.ContainerDevice{
				Idx:       int(dev.Index),
				UUID:      dev.ID,
				Type:      k.Type,
				Usedmem:   memreq,
				Usedcores: k.Coresreq,
			})
		}
		if k.Nums == 0 {
			klog.V(4).InfoS("device allocate success", "pod", klog.KObj(pod), "allocate device", tmpDevs)
			return true, tmpDevs, ""
		}
	}
	if len(tmpDevs) > 0 {
		reason[common.AllocatedCardsInsufficientRequest] = len(tmpDevs)
		klog.V(5).InfoS(common.AllocatedCardsInsufficientRequest, "pod", klog.KObj(pod), "request", originReq, "allocated", len(tmpDevs))
	}
	return false, tmpDevs, common.GenReason(reason, len(devices))
}
