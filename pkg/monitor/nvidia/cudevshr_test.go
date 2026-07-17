/*
Copyright 2026 The HAMi Authors.

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

package nvidia

import (
	"sync"
	"testing"
)

// Test_RangeContainersLocked_RaceWithUpdate guards the lock that collectPodAndContainerInfo
// relies on: it reads the container map through RangeContainersLocked (the production path)
// while another goroutine mutates the map the way Update() does. Run with -race it passes;
// if RangeContainersLocked stopped taking the lock, the detector would report a concurrent
// map iteration and map write.
func Test_RangeContainersLocked_RaceWithUpdate(t *testing.T) {
	l := &ContainerLister{containers: map[string]*ContainerUsage{}}

	var wg sync.WaitGroup
	stop := make(chan struct{})

	// writer mimics Update(): add/delete entries under the lister lock.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			l.mutex.Lock()
			l.containers["c"] = &ContainerUsage{}
			delete(l.containers, "c")
			l.mutex.Unlock()
		}
	}()

	// reader iterates the map through the production locking method.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 20000; i++ {
			l.RangeContainersLocked(func(containers map[string]*ContainerUsage) {
				for _, c := range containers {
					_ = c.PodUID
				}
			})
		}
		close(stop)
	}()

	wg.Wait()
}
