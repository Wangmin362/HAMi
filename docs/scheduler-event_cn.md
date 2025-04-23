# Scheduler event
## 现状

- running pod 节点 卡的类型

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
    - name: worker01
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
I0422 13:42:30.272796       1 device.go:261] idx= nvidia.com/gpu val= {{2 0} {<nil>} 2 DecimalSI} {{0 0} {<nil>}  }
I0422 13:42:30.272802       1 device.go:261] idx= nvidia.com/gpucores val= {{30 0} {<nil>} 30 DecimalSI} {{0 0} {<nil>}  }
I0422 13:42:30.272804       1 device.go:261] idx= nvidia.com/gpumem val= {{3 3} {<nil>} 3k DecimalSI} {{0 0} {<nil>}  }
I0422 13:42:30.272812       1 pod.go:44] "collect requestreqs" counts=[{"NVIDIA":{"Nums":2,"Type":"NVIDIA","Memreq":3000,"MemPercentagereq":101,"Coresreq":30}}]
I0422 13:42:30.272819       1 scheduler.go:352] "node unregistered" node="k8s-master1" error="node k8s-master1 not found"
I0422 13:42:30.272824       1 scheduler.go:490] "getNodesUsage failed nodes" nodes={"k8s-master1":"node unregistered"}
I0422 13:42:30.272827       1 scheduler.go:499] All node scores do not meet for pod gpu-pod
I0422 13:42:30.273047       1 event.go:307] "Event occurred" object="default/gpu-pod" fieldPath="" kind="Pod" apiVersion="v1" type="Warning" reason="FilteringFailed" message="no available node, all node scores do not meet"
```
### 用户提交多个任务时，节点打分日志交错，无法区分日志信息是哪个任务哪个节点的

由于节点打分是多协程处理的，因此多个节点之间的日志交叉打印，导致用户无法区分日志信息是哪个任务哪个节点哪个设备的。


## 目标

1. 针对于Pod调度进入hami-scheduler的场景，优化Pod event日志信息，输出可能的每种原因，以及对应原因的Node数量。
2. hami-scheduler在调度Pod过程中，需要详细打印每个Node失败的原因，和事件中的失败原因相对应，方便用户根据失败原因排查对应的节点。

## 日志格式：

失败原因 Pod名称 Node名称 容器名称 设备ID/UUID [内存、算力、类型、数量]

失败原因需要规范，尽可能的精简，不要过长。规范后，event message会比较清晰精简。

request devices nums cannot exceed the total number of devices on the node -> device request exceeds node capacity
card type mismatch,continuing... -> card type mismatch
the container wants exclusive access to an entire card, but the card is already in use pod -> exclusive device allocation conflicts with active pod

## 提案一

采用pod event组合调度器日志的方式，帮助用户快速定位节点问题。 pod event中汇总所有节点的失败原因，仅显示每种原因失败节点的数量。由于节点不可用
都是因为设备不满足，但是一个节点通常存在多张卡，因此需要将所有设备不满足的原因全部罗列出来。

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
  ----     ------            ----   ----            ------- （取最后一个容器所有卡失败的原因作为当前节点不合适的原因）
  Warning  FilteringFailed   2m45s  hami-scheduler  no available node, node1[2 device card type mismatch, 1 device Insufficient remaining memory],
   node2[8 device request devices nums cannot exceed the total number of devices on the node], node3[2 node the container wants exclusive access to an entire card, but the card is already in use pod，
    4 node card Insufficient remaining cores, 1 node card Insufficient remaining memory, 1 node card uuid mismatch],
    node4[1 node card type mismatch, 1 node card Insufficient remaining memory, 1 node card Insufficient remaining cores,
    1 node the container wants exclusive access to an entire card, but the card is already in use pod 2 node card uuid mismatch]
```

日志：
```
I0422 02:15:42.349712  1 scheduler.go:499] request devices nums cannot exceed the total number of devices on the node.  pod="deepseek-5996b8569d-kgwgx" node="node1" container="worke1"
I0422 02:15:42.349712  1 scheduler.go:499] card type mismatch,continuing...  pod="deepseek-5996b8569d-kgwgx" node="node1" container="worke1"
I0422 02:15:42.349712  1 scheduler.go:499] card uuid mismatch pod="deepseek-5996b8569d-kgwgx" node="node1" container="worke1"
I0422 02:15:42.349712  1 scheduler.go:499] card Insufficient remaining memory pod="deepseek-5996b8569d-kgwgx" node="node1" container="worke1" 
I0422 02:15:42.349712  1 scheduler.go:499] card Insufficient remaining cores pod="deepseek-5996b8569d-kgwgx" node="node1" container="worke1"
I0422 02:15:42.349712  1 scheduler.go:499] the container wants exclusive access to an entire card, but the card is already in use pod="deepseek-5996b8569d-kgwgx" node="node1" container="worke1"
I0422 02:15:42.349712  1 scheduler.go:499] calcScore:node not fit  pod="deepseek-5996b8569d-kgwgx]" node="node1"

```

该方案，event事件清晰过于详细，event message会被拦截，用户查看事件时不会太方便。


## 提案二

采用pod event组合调度器日志的方式，帮助用户快速定位节点问题。 pod event中汇总所有节点的失败原因，仅显示每种原因失败节点的数量。由于节点不可用
都是因为设备不满足，一个节点通常存在多张卡，若把所有设备不满足的原因全部罗列出来，event消息过于冗长，k8s本身也会对event消息进行截断，因此需要
把节点不满足的原因收敛，使用最后一个设备不满足的原因作为节点失败原因。

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
  ----     ------            ----   ----            ------- （最后一个容器，最后一个卡不满足的原因作为节点失败的原因）
  Warning  FilteringFailed   2m45s  hami-scheduler  no available node, 2 node request devices nums cannot exceed the total number of devices on the node.
   , 2 node card type mismatch, 1 node card Insufficient remaining memory, 1 node card Insufficient remaining cores, 
   1 node the container wants exclusive access to an entire card, but the card is already in use
```

日志：
```
I0422 02:15:42.349712  1 scheduler.go:499] request devices nums cannot exceed the total number of devices on the node.  pod="deepseek-5996b8569d-kgwgx" node="node1" container="worke1"
I0422 02:15:42.349712  1 scheduler.go:499] card type mismatch,continuing...  pod="deepseek-5996b8569d-kgwgx" node="node1" container="worke1"
I0422 02:15:42.349712  1 scheduler.go:499] card uuid mismatch pod="deepseek-5996b8569d-kgwgx" node="node1" container="worke1"
I0422 02:15:42.349712  1 scheduler.go:499] card Insufficient remaining memory pod="deepseek-5996b8569d-kgwgx" node="node1" container="worke1" 
I0422 02:15:42.349712  1 scheduler.go:499] card Insufficient remaining cores pod="deepseek-5996b8569d-kgwgx" node="node1" container="worke1"
I0422 02:15:42.349712  1 scheduler.go:499] the container wants exclusive access to an entire card, but the card is already in use pod="deepseek-5996b8569d-kgwgx" node="node1" container="worke1"
I0422 02:15:42.349712  1 scheduler.go:499] calcScore:node not fit  pod="deepseek-5996b8569d-kgwgx]" node="node1"

```
这种方案，相对来说，会省略一些节点不满足的具体原因，但是可以根据日志信息，通过pod名称，节点名称快速找到节点不满足的原因。于此同时，由于event日志信息
只有一条，不会对apiserver有太多压力

## 提案三

采用pod event组合调度器日志的方式，帮助用户快速定位节点问题。 pod event中汇总所有节点的失败原因，由于节点不可用都是因为设备不满足，一个节点通常存在多张卡，
因此把每一个不满足的节点以单独的事件输出，并且描述该节点不满足的原因，即详细罗列每个设备不满足的原因并汇总。

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
  ----     ------            ----   ----            ------- （收集最后一个容器失败的原因）
  Warning  FilteringFailed   2m45s  hami-scheduler  node1 2 device card type mismatch, 5 device Insufficient remaining memory, 
      1 device Insufficient remaining cores
  Warning  FilteringFailed   2m45s  hami-scheduler  node2 request devices nums cannot exceed the total number of devices on the node
  Warning  FilteringFailed   2m45s  hami-scheduler  node3 2 node the container wants exclusive access to an entire card, but the card is already in use pod
    4 node card Insufficient remaining cores, 1 node card Insufficient remaining memory, 1 node card uuid mismatch
  Warning  FilteringFailed   2m45s  hami-scheduler  node4 1 card type mismatch, 4 node card Insufficient remaining cores, 
     1 node card Insufficient remaining memory, 2 node card uuid mismatch
  Warning  FilteringFailed   2m45s  hami-scheduler  no available node
```

日志：
```
I0422 02:15:42.349712  1 scheduler.go:499] request devices nums cannot exceed the total number of devices on the node.  pod="deepseek-5996b8569d-kgwgx" node="node2" container="worke1"
I0422 02:15:42.349712  1 scheduler.go:499] card type mismatch,continuing...  pod="deepseek-5996b8569d-kgwgx" node="node1" container="worke1"
I0422 02:15:42.349712  1 scheduler.go:499] card uuid mismatch pod="deepseek-5996b8569d-kgwgx" node="node1" container="worke1"
I0422 02:15:42.349712  1 scheduler.go:499] card Insufficient remaining memory pod="deepseek-5996b8569d-kgwgx" node="node1" deviceid=7, container="worke1" 
I0422 02:15:42.349712  1 scheduler.go:499] card Insufficient remaining memory pod="deepseek-5996b8569d-kgwgx" node="node1" deviceid=6, container="worke1" 
I0422 02:15:42.349712  1 scheduler.go:499] card Insufficient remaining memory pod="deepseek-5996b8569d-kgwgx" node="node1" deviceid=4, container="worke1" 
I0422 02:15:42.349712  1 scheduler.go:499] card Insufficient remaining memory pod="deepseek-5996b8569d-kgwgx" node="node1" deviceid=3, container="worke1" 
I0422 02:15:42.349712  1 scheduler.go:499] card Insufficient remaining memory pod="deepseek-5996b8569d-kgwgx" node="node1" deviceid=1, container="worke1" 
I0422 02:15:42.349712  1 scheduler.go:499] card Insufficient remaining cores pod="deepseek-5996b8569d-kgwgx" node="node1" container="worke1"
I0422 02:15:42.349712  1 scheduler.go:499] the container wants exclusive access to an entire card, but the card is already in use pod="deepseek-5996b8569d-kgwgx" node="node1" container="worke1"
I0422 02:15:42.349712  1 scheduler.go:499] calcScore:node not fit  pod="deepseek-5996b8569d-kgwgx]" node="node1"

```

这种方案的事件非常清晰，若当前集群节点数较多，那么势必造成Pod event日志信息过多，可能会对于apiserver性能有影响。 所以需要搞清楚目前用户集群一般有多少个节点。