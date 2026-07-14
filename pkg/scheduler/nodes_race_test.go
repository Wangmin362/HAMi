package scheduler

import (
	"sync"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/Project-HAMi/HAMi/pkg/device"
	"github.com/Project-HAMi/HAMi/pkg/device/nvidia"
)

// Test_GetNode_ConcurrentMapRace reproduces the data race between the topology
// scoring path, which reads GetNode(...).Devices without holding the lock, and
// register()/addNode, which writes the same map in place. Before the fix
// (GetNode returning the live pointer) this trips the race detector and, in
// production, "fatal error: concurrent map iteration and map write". Run with
// -race.
func Test_GetNode_ConcurrentMapRace(t *testing.T) {
	m := newNodeManager()
	mk := func() *device.NodeInfo {
		return &device.NodeInfo{
			ID:   "race-node",
			Node: &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "race-node"}},
			Devices: map[string][]device.DeviceInfo{
				nvidia.NvidiaGPUDevice: {{ID: "GPU0"}, {ID: "GPU1"}, {ID: "GPU2"}, {ID: "GPU3"}},
			},
		}
	}
	m.addNode("race-node", mk())

	stop := make(chan struct{})
	var wg sync.WaitGroup

	// writer: register() -> addNode in-place map write.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				m.addNode("race-node", mk())
			}
		}
	}()

	// reader: topology scoring ranges GetNode(...).Devices without a lock.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				n, err := m.GetNode("race-node")
				if err != nil {
					continue
				}
				for range n.Devices[nvidia.NvidiaGPUDevice] {
				}
			}
		}
	}()

	time.Sleep(300 * time.Millisecond)
	close(stop)
	wg.Wait()
}
