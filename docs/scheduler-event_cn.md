# Scheduler event
## 现状
### event事件描述不清晰，问题定位时无法知道具体的节点失败原因

用户提交一个任务，通过hami-scheduler调度之后，Pod一直处于Pending状态，无法调度成功。 Pod事件仅仅显示了当前Pod没有找到满足的节点，没有具体显示
某个节点是因为某种原因不合适，不方便排查问题。

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: gpu-pod
spec:
  containers:
    - name: ubuntu-container
      image: ubuntu:22.04
      command: ["bash", "-c", "sleep 86400"]
      resources:
        limits:
          nvidia.com/gpu: "1" # declare how many physical GPUs the pod needs
          nvidia.com/gpumem: "3000" # identifies 3000M GPU memory each physical GPU allocates to the pod （Optional,Integer）
          nvidia.com/gpucores: "30" # identifies 30% GPU GPU core each physical GPU allocates to the pod （Optional,Integer)
```

```event
$ kubectl describe pod gpu-pod
Name:             gpu-pod
Namespace:        default
Priority:         0
Service Account:  default
Node:             <none>
Labels:           <none>
Annotations:      <none>
Status:           Pending
IP:               
IPs:              <none>
Containers:
  ubuntu-container:
    Image:      ubuntu:22.04
    Port:       <none>
    Host Port:  <none>
    Command:
      bash
      -c
      sleep 86400
    Limits:
      nvidia.com/gpu:       2
      nvidia.com/gpucores:  30
      nvidia.com/gpumem:    3k
    Requests:
      nvidia.com/gpu:       2
      nvidia.com/gpucores:  30
      nvidia.com/gpumem:    3k
    Environment:            <none>
    Mounts:
      /var/run/secrets/kubernetes.io/serviceaccount from kube-api-access-57256 (ro)
Conditions:
  Type           Status
  PodScheduled   False 
Volumes:
  kube-api-access-57256:
    Type:                    Projected (a volume that contains injected data from multiple sources)
    TokenExpirationSeconds:  3607
    ConfigMapName:           kube-root-ca.crt
    ConfigMapOptional:       <nil>
    DownwardAPI:             true
QoS Class:                   BestEffort
Node-Selectors:              <none>
Tolerations:                 node.kubernetes.io/not-ready:NoExecute op=Exists for 300s
                             node.kubernetes.io/unreachable:NoExecute op=Exists for 300s
Events:
  Type     Reason            Age   From            Message
  ----     ------            ----  ----            -------
  Warning  FailedScheduling  10s   hami-scheduler  0/1 nodes are available: 1 node unregistered. preemption: 0/1 nodes are available: 1 No preemption victims found for incoming pod..
  Warning  FilteringFailed   11s   hami-scheduler  no available node, all node scores do not meet
```

```log
$ kubectl logs -f  hami-scheduler-d69cb679b-9vtdg -c vgpu-scheduler-extender
I0422 10:46:03.163258       1 pod.go:44] "collect requestreqs" counts=[{"NVIDIA":{"Nums":2,"Type":"NVIDIA","Memreq":3000,"MemPercentagereq":101,"Coresreq":30}}]
I0422 10:46:03.163267       1 scheduler.go:499] All node scores do not meet for pod gpu-pod
I0422 10:46:03.163476       1 event.go:307] "Event occurred" object="default/gpu-pod" fieldPath="" kind="Pod" apiVersion="v1" type="Warning" reason="FilteringFailed" message="no available node, all node scores do not meet"
```
### 用户提交多个任务时，节点打分日志交错，无法快速定位问题



## 目标

1. 针对于Pod调度进入hami-scheduler的场景，优化Pod event日志信息，输出可能的每种原因，以及对应原因的Node数量。
2. hami-scheduler在调度Pod过程中，需要详细打印每个Node失败的原因，和事件中的失败原因相对应，方便用户根据失败原因排查对应的节点。

## 非目标

1. 针对于没有进入hami-scheduler的场景，显示详细的错误原因

## 提案

```yaml
resources:
limits:
  nvidia.com/vgpu: 2
  nvidia.com/gpumem: 2000
  nvidia.com/gpucores: 90
```

event：
```log
Events:
  Type     Reason            Age    From            Message
  ----     ------            ----   ----            -------
  Warning  FilteringFailed   2m45s  hami-scheduler  no available node, 2 node [lisense expire], 2 node [no lisense], 1 node [device not enough]
```

日志：
```
I0422 02:15:42.349712  1 scheduler.go:499] shceduler filter pod[deepseek-5996b8569d-kgwgx] node1 lisense expire
I0422 02:15:42.349712  1 scheduler.go:499] shceduler filter pod[pytorch-78sxb4s542-spox5] node1 lisense expire
I0422 02:15:42.349712  1 scheduler.go:499] shceduler filter pod[deepseek-5996b8569d-kgwgx] node3 lisense expire
I0422 02:15:42.349712  1 scheduler.go:499] shceduler filter pod[pytorch-78sxb4s542-spox5] node3 lisense expire
I0422 02:15:42.349712  1 scheduler.go:499] shceduler filter pod[deepseek-5996b8569d-kgwgx] node7 no lisense
I0422 02:15:42.349712  1 scheduler.go:499] shceduler filter pod[tensorflow-594ex5s6es-pglhe] node3 lisense expire
I0422 02:15:42.349712  1 scheduler.go:499] shceduler filter pod[pytorch-78sxb4s542-spox5] node6 device not enough
I0422 02:15:42.349712  1 scheduler.go:499] shceduler filter pod[tensorflow-594ex5s6es-pglhe] node7 no lisense
I0422 02:15:42.349712  1 scheduler.go:499] shceduler filter pod[deepseek-5996b8569d-kgwgx] node4 no lisense
I0422 02:15:42.349712  1 scheduler.go:499] shceduler filter pod[pytorch-78sxb4s542-spox5] node4 no lisense
I0422 02:15:42.349712  1 scheduler.go:499] shceduler filter pod[deepseek-5996b8569d-kgwgx] node6 device not enough
I0422 02:15:42.349712  1 scheduler.go:499] shceduler filter pod[tensorflow-594ex5s6es-pglhe] node4 no lisense
I0422 02:15:42.349712  1 scheduler.go:499] shceduler filter pod[deepseek-5996b8569d-kgwgx] filed
I0422 02:15:42.349712  1 scheduler.go:499] shceduler filter pod[tensorflow-594ex5s6es-pglhe] node6 device not enough
I0422 02:15:42.349712  1 scheduler.go:499] shceduler filter pod[pytorch-78sxb4s542-spox5] node7 no lisense
I0422 02:15:42.349712  1 scheduler.go:499] shceduler filter pod[pytorch-78sxb4s542-spox5] successful
I0422 02:15:42.349712  1 scheduler.go:499] shceduler filter pod[tensorflow-594ex5s6es-pglhe] node1 lisense expire
I0422 02:15:42.349712  1 scheduler.go:499] shceduler filter pod[tensorflow-594ex5s6es-pglhe] failed
```