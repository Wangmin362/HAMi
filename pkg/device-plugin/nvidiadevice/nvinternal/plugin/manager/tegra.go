/**
# Copyright (c) NVIDIA CORPORATION.  All rights reserved.
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

package manager

import (
	"fmt"

	"github.com/Project-HAMi/HAMi/pkg/device-plugin/nvidiadevice/nvinternal/plugin"
	"github.com/Project-HAMi/HAMi/pkg/device-plugin/nvidiadevice/nvinternal/rm"
)

type tegramanager manager

// GetPlugins returns the plugins associated with the NVML resources available on the node
func (m *tegramanager) GetPlugins() ([]plugin.Interface, error) {
	rms, err := rm.NewTegraResourceManagers(m.config)
	if err != nil {
		return nil, fmt.Errorf("failed to construct NVML resource managers: %v", err)
	}

	var plugins []plugin.Interface
	for _, r := range rms {
		// 简单来说，每一种资源都会有一个对应的device-plugin，
		// 对于gpu single这种我觉得比较简单，只需要一个device-plugin，但是对于gpu mixed就需要启用多个。
		// TODO mig这种动态划分特性如何支持的？ 在容器真正使用之前应该已经确定好了资源吧
		plugins = append(plugins, plugin.NewNvidiaDevicePlugin(m.config, r, m.cdiHandler, m.cdiEnabled))
	}
	return plugins, nil
}

// CreateCDISpecFile creates the spec is a no-op for the tegra plugin
func (m *tegramanager) CreateCDISpecFile() error {
	return nil
}
