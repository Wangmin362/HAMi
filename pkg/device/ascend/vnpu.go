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

type Template struct {
	Name   string `yaml:"name"`
	Memory int64  `yaml:"memory"`
	AICore int32  `yaml:"aiCore,omitempty"`
	AICPU  int32  `yaml:"aiCPU,omitempty"`
}

/* config-file配置文件如下
apiVersion: v1
data:
  ascend-config.yaml: |-
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
      topologyPairs:
        - 1,2,3,4,5,6,7
        - 0,2,3,4,5,6,7
        - 0,1,3,4,5,6,7
        - 0,1,2,4,5,6,7
        - 0,1,2,3,5,6,7
        - 0,1,2,3,4,6,7
        - 0,1,2,3,4,5,7
        - 0,1,2,3,4,5,6
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
      topologyPairs:
        - 1,2,3,4,5,6,7
        - 0,2,3,4,5,6,7
        - 0,1,3,4,5,6,7
        - 0,1,2,4,5,6,7
        - 0,1,2,3,5,6,7
        - 0,1,2,3,4,6,7
        - 0,1,2,3,4,5,7
        - 0,1,2,3,4,5,6
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
    - chipName: 910B4-1
      commonWord: Ascend910B4
      resourceName: huawei.com/Ascend910B4
      resourceMemoryName: huawei.com/Ascend910B4-memory
      memoryAllocatable: 65536
      memoryCapacity: 65536
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
kind: ConfigMap
metadata:
  name: hami-device-plugin-ascend
  namespace: rise-vast-system
*/

type VNPUConfig struct {
	CommonWord         string     `yaml:"commonWord"`
	ChipName           string     `yaml:"chipName"`
	ResourceName       string     `yaml:"resourceName"`
	ResourceMemoryName string     `yaml:"resourceMemoryName"`
	MemoryAllocatable  int64      `yaml:"memoryAllocatable"`
	MemoryCapacity     int64      `yaml:"memoryCapacity"`
	AICore             int32      `yaml:"aiCore"`
	AICPU              int32      `yaml:"aiCPU"`
	Templates          []Template `yaml:"templates"`
}
