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

package scheduler

import (
	"fmt"
	"strings"
	"sync"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/klog/v2"

	"github.com/Project-HAMi/HAMi/pkg/device"
	"github.com/Project-HAMi/HAMi/pkg/scheduler/policy"
	"github.com/Project-HAMi/HAMi/pkg/util"
)

type NodeUsage struct {
	Node    *corev1.Node
	Devices policy.DeviceUsageList
}

type nodeManager struct {
	// key为NodeID, value为NodeInfo信息
	nodes map[string]*util.NodeInfo
	mutex sync.RWMutex
}

// 与其说是一个nodeManager, 其实倒不如说是个nodeCache， 这个nodeCache主要是用来保存节点信息的，
func newNodeManager() *nodeManager {
	return &nodeManager{
		nodes: make(map[string]*util.NodeInfo),
	}
}

// 外面通过SharedInformer机制监听到节点变化之后，会通过这个方法把节点信息保存在nodeManager中，
func (m *nodeManager) addNode(nodeID string, nodeInfo *util.NodeInfo) {
	// 如果一个节点没有发现任何设备，将会被直接忽略
	// fixme 这里是否应该增加日志，方便排查问题
	if nodeInfo == nil || len(nodeInfo.Devices) == 0 {
		return
	}
	m.mutex.Lock()
	defer m.mutex.Unlock()
	_, ok := m.nodes[nodeID]
	if ok {
		// TODO 之所以存在下面的逻辑，我估计更多的处于一个节点存在多种不同类型的设备的情况
		if len(nodeInfo.Devices) > 0 {
			tmp := make([]util.DeviceInfo, 0, len(nodeInfo.Devices))
			devices := device.GetDevices()
			deviceType := ""
			// TODO 下面这一块逻辑是在干嘛？ 没有看的很懂
			for _, val := range devices {
				if strings.Contains(nodeInfo.Devices[0].Type, val.CommonWord()) {
					deviceType = val.CommonWord()
					// fixme 这里是否能够直接跳出？
				}
			}
			// TODO 这里似乎是为了解决一个节点上有多种不同类型的设备的情况
			for _, val := range m.nodes[nodeID].Devices {
				if !strings.Contains(val.Type, deviceType) {
					tmp = append(tmp, val)
				}
			}
			m.nodes[nodeID].Devices = tmp
			m.nodes[nodeID].Devices = append(m.nodes[nodeID].Devices, nodeInfo.Devices...)
		}
		m.nodes[nodeID].Node = nodeInfo.Node
	} else {
		m.nodes[nodeID] = nodeInfo
	}
}

// 当前节点移除指定类型的设备，保留其它类型的设备，一般是节点处于不健康的情况下会有这样的需求
func (m *nodeManager) rmNodeDevices(nodeID string, deviceVendor string) {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	nodeInfo := m.nodes[nodeID]
	if nodeInfo == nil {
		return
	}

	// 删除指定设备之后，剩余的设备
	devices := make([]util.DeviceInfo, 0)
	for _, val := range nodeInfo.Devices {
		if val.DeviceVendor != deviceVendor { // 删除指定设备类型的设备，保留其它类型的设备
			devices = append(devices, val)
		}
	}

	if len(devices) == 0 {
		delete(m.nodes, nodeID)
	} else {
		nodeInfo.Devices = devices
	}
	klog.InfoS("Removing device from node", "nodeName", nodeID, "deviceVendor", deviceVendor, "remainingDevices", devices)
}

func (m *nodeManager) GetNode(nodeID string) (*util.NodeInfo, error) {
	m.mutex.RLock()
	defer m.mutex.RUnlock()
	if n, ok := m.nodes[nodeID]; ok {
		return n, nil
	}
	return &util.NodeInfo{}, fmt.Errorf("node %v not found", nodeID)
}

func (m *nodeManager) ListNodes() (map[string]*util.NodeInfo, error) {
	m.mutex.RLock()
	defer m.mutex.RUnlock()
	return m.nodes, nil
}
