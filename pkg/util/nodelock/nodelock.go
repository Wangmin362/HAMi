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

package nodelock

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Project-HAMi/HAMi/pkg/util/client"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog/v2"
)

const (
	NodeLockKey  = "hami.io/mutex.lock"
	MaxLockRetry = 5
	NodeLockSep  = ","
)

// SetNodeLock 本质上就是给Node资源添加一个hami.io/mutex.lock注解，注解的值类似：2025-04-19T10:30:00Z,my-namespace,my-pod
func SetNodeLock(nodeName string, lockname string, pods *corev1.Pod) error {
	ctx := context.Background()
	node, err := client.GetClient().CoreV1().Nodes().Get(ctx, nodeName, metav1.GetOptions{})
	if err != nil {
		return err
	}
	if _, ok := node.ObjectMeta.Annotations[NodeLockKey]; ok {
		return fmt.Errorf("node %s is locked", nodeName)
	}
	newNode := node.DeepCopy()
	// 返回值格式为：2025-04-19T10:30:00Z,my-namespace,my-pod
	newNode.ObjectMeta.Annotations[NodeLockKey] = GenerateNodeLockKeyByPod(pods)
	_, err = client.GetClient().CoreV1().Nodes().Update(ctx, newNode, metav1.UpdateOptions{})
	for i := 0; i < MaxLockRetry && err != nil; i++ {
		klog.ErrorS(err, "Failed to update node", "node", nodeName, "retry", i)
		time.Sleep(100 * time.Millisecond)
		node, err = client.GetClient().CoreV1().Nodes().Get(ctx, nodeName, metav1.GetOptions{})
		if err != nil {
			klog.ErrorS(err, "Failed to get node when retry to update", "node", nodeName)
			continue
		}
		newNode := node.DeepCopy()
		newNode.ObjectMeta.Annotations[NodeLockKey] = GenerateNodeLockKeyByPod(pods)
		_, err = client.GetClient().CoreV1().Nodes().Update(ctx, newNode, metav1.UpdateOptions{})
	}
	if err != nil {
		return fmt.Errorf("setNodeLock exceeds retry count %d", MaxLockRetry)
	}
	klog.InfoS("Node lock set", "node", nodeName)
	return nil
}

// ReleaseNodeLock 释放锁本质上就是删除node节点的hami.io/mutex.lock注解
func ReleaseNodeLock(nodeName string, lockname string) error {
	ctx := context.Background()
	node, err := client.GetClient().CoreV1().Nodes().Get(ctx, nodeName, metav1.GetOptions{})
	if err != nil {
		return err
	}
	// 查询node节点的hami.io/mutex.lock注解
	if _, ok := node.ObjectMeta.Annotations[NodeLockKey]; !ok {
		klog.InfoS("Node lock not set", "node", nodeName)
		return nil
	}
	// 这里进行深拷贝是在K8S中修改资源对象的最佳实践，获取->深拷贝->修改->更新，这是K8S官方推荐的操作方式
	newNode := node.DeepCopy()
	delete(newNode.ObjectMeta.Annotations, NodeLockKey)
	_, err = client.GetClient().CoreV1().Nodes().Update(ctx, newNode, metav1.UpdateOptions{})
	for i := 0; i < MaxLockRetry && err != nil; i++ {
		klog.ErrorS(err, "Failed to update node", "node", nodeName, "retry", i)
		time.Sleep(100 * time.Millisecond)
		node, err = client.GetClient().CoreV1().Nodes().Get(ctx, nodeName, metav1.GetOptions{})
		if err != nil {
			klog.ErrorS(err, "Failed to get node when retry to update", "node", nodeName)
			continue
		}
		newNode := node.DeepCopy()
		delete(newNode.ObjectMeta.Annotations, NodeLockKey)
		_, err = client.GetClient().CoreV1().Nodes().Update(ctx, newNode, metav1.UpdateOptions{})
	}
	if err != nil {
		return fmt.Errorf("releaseNodeLock exceeds retry count %d", MaxLockRetry)
	}
	klog.InfoS("Node lock released", "node", nodeName)
	return nil
}

func LockNode(nodeName string, lockname string, pods *corev1.Pod) error {
	ctx := context.Background()
	node, err := client.GetClient().CoreV1().Nodes().Get(ctx, nodeName, metav1.GetOptions{})
	if err != nil {
		return err
	}
	if _, ok := node.ObjectMeta.Annotations[NodeLockKey]; !ok {
		return SetNodeLock(nodeName, lockname, pods)
	}
	lockTime, _, _, err := ParseNodeLock(node.ObjectMeta.Annotations[NodeLockKey])
	if err != nil {
		return err
	}
	if time.Since(lockTime) > time.Minute*5 {
		klog.InfoS("Node lock expired", "node", nodeName, "lockTime", lockTime)
		err = ReleaseNodeLock(nodeName, lockname)
		if err != nil {
			klog.ErrorS(err, "Failed to release node lock", "node", nodeName)
			return err
		}
		return SetNodeLock(nodeName, lockname, pods)
	}
	return fmt.Errorf("node %s has been locked within 5 minutes", nodeName)
}

func ParseNodeLock(value string) (lockTime time.Time, ns, name string, err error) {
	if !strings.Contains(value, NodeLockSep) {
		lockTime, err = time.Parse(time.RFC3339, value)
		return lockTime, "", "", err
	}
	s := strings.Split(value, NodeLockSep)
	if len(s) != 3 {
		lockTime, err = time.Parse(time.RFC3339, value)
		return lockTime, "", "", err
	}
	lockTime, err = time.Parse(time.RFC3339, s[0])
	return lockTime, s[1], s[2], err
}

// GenerateNodeLockKeyByPod 返回值格式为：2025-04-19T10:30:00Z,my-namespace,my-pod
func GenerateNodeLockKeyByPod(pods *corev1.Pod) string {
	if pods == nil {
		return time.Now().Format(time.RFC3339)
	}
	ns, name := pods.Namespace, pods.Name
	// 2025-04-19T10:30:00Z,my-namespace,my-pod
	return fmt.Sprintf("%s%s%s%s%s", time.Now().Format(time.RFC3339), NodeLockSep, ns, NodeLockSep, name)
}
