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

package policy

type SchedulerPolicyName string

const (
	// NodeSchedulerPolicyBinpack is node use binpack scheduler policy.
	NodeSchedulerPolicyBinpack SchedulerPolicyName = "binpack"
	// NodeSchedulerPolicySpread is node use spread scheduler policy.
	NodeSchedulerPolicySpread SchedulerPolicyName = "spread"
	// GPUSchedulerPolicyBinpack is GPU use binpack scheduler.
	GPUSchedulerPolicyBinpack SchedulerPolicyName = "binpack"
	// GPUSchedulerPolicySpread is GPU use spread scheduler.
	GPUSchedulerPolicySpread SchedulerPolicyName = "spread"
)

func (s SchedulerPolicyName) String() string {
	return string(s)
}

const (
	// NodeSchedulerPolicyAnnotationKey is user set Pod annotation to change this default node policy.
	// 用于设置节点的调度策略，目前支持binpack, spread两种策略，默认为binpack，即默认让集群中的碎片资源最小，以预留足够的资源部署大任务
	NodeSchedulerPolicyAnnotationKey = "hami.io/node-scheduler-policy"
	// GPUSchedulerPolicyAnnotationKey is user set Pod annotation to change this default GPU policy.
	// 用于设置GPU的调度策略，目前支持binpack, spread两种策略，默认为spread策略，这样可以尽可能充分的使用GPU资源
	GPUSchedulerPolicyAnnotationKey = "hami.io/gpu-scheduler-policy"
)

const (
	Weight int = 10
)
