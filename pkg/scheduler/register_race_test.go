package scheduler

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/Project-HAMi/HAMi/pkg/device/nvidia"
	"github.com/Project-HAMi/HAMi/pkg/scheduler/config"
	"github.com/Project-HAMi/HAMi/pkg/util/client"
)

// Test_register_NodesMapRace runs register() and rmNode() concurrently to surface the s.nodes data race; run with -race.
func Test_register_NodesMapRace(t *testing.T) {
	s := NewScheduler()
	s.stopCh = make(chan struct{})
	s.nodeNotify = make(chan struct{}, 1)
	client.KubeClient = fake.NewClientset()
	s.kubeClient = client.KubeClient
	informerFactory := informers.NewSharedInformerFactoryWithOptions(client.KubeClient, time.Hour)
	s.nodeLister = informerFactory.Core().V1().Nodes().Lister()
	s.podLister = informerFactory.Core().V1().Pods().Lister()
	indexer := informerFactory.Core().V1().Nodes().Informer().GetIndexer()

	require.NoError(t, config.InitDevicesWithConfig(&config.Config{
		NvidiaConfig: nvidia.NvidiaConfig{
			ResourceCountName:            "hami.io/gpu",
			ResourceMemoryName:           "hami.io/gpumem",
			ResourceMemoryPercentageName: "hami.io/gpumem-percentage",
			ResourceCoreName:             "hami.io/gpucores",
			DefaultGPUNum:                1,
		},
	}))

	mkNode := func(v int) *corev1.Node {
		reg := fmt.Sprintf(`[{"id":"GPU-0","count":10,"devmem":%d,"devcore":100,"type":"NVIDIA","health":true,"mode":"hami-core","numa":0,"index":0,"devicevendor":"NVIDIA"}]`, 1024+v)
		n := &corev1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name: "race-node",
				Annotations: map[string]string{
					nvidia.RegisterAnnos:     reg,
					"hami.io/node-handshake": "Requesting_2999-01-01 00:00:00",
				},
			},
		}
		n.Status.Allocatable = corev1.ResourceList{"hami.io/gpu": resource.MustParse("1")}
		return n
	}
	require.NoError(t, indexer.Add(mkNode(0)))
	informerFactory.Start(s.stopCh)
	informerFactory.WaitForCacheSync(s.stopCh)

	stop := make(chan struct{})
	var wg sync.WaitGroup

	// writer: onDelNode -> rmNode deletes from s.nodes under the lock.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				s.rmNode("race-node")
			}
		}
	}()

	// reader: bump the annotation each round so register() re-processes the node and reaches the s.nodes read.
	wg.Add(1)
	go func() {
		defer wg.Done()
		printed := map[string]bool{}
		sel := labels.Everything()
		v := 0
		for {
			select {
			case <-stop:
				return
			default:
				v++
				_ = indexer.Update(mkNode(v))
				s.register(sel, printed)
			}
		}
	}()

	time.Sleep(300 * time.Millisecond)
	close(stop)
	wg.Wait()
}
