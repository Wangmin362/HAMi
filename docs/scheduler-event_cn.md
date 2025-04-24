# Scheduler event
## 现状
### event事件描述不清晰，问题定位时无法知道具体的节点失败原因

用户提交一个任务，通过hami-scheduler调度之后，Pod一直处于Pending状态，无法调度成功。 Pod事件仅仅显示了当前Pod没有找到满足的
节点(no available node, all node scores do not meet)， 没有具体显示哪些类型的节点调度失败的数量，不方便用户概览全局信息。
（用户的K8S集群可能存在3个英伟达节点，2个摩尔线程节点，2个寒武纪节点，2个昇腾节点，1个中科海光节点， 在Pod失败的时候，用户需要关心不同类型节点
失败的数量，所以需要在event事件中显示出来）

于此同时，如果Pod调度成功，处于running状态，但是用户发现这个Pod调度的节点可能并非是自己预期的节点，此时用户需要知道每个节点的调度情况，譬如调度
失败的节点有几个（每种类型调度失败的节点有几个），调度成功的节点有几个（并且还想知道每个调度成功的节点的分数是多少，这样用户可以很方便的知道为什么
当前Pod调度到这个节点上）

下面是一个简单的用户样例，展示了用户提交的一个任务，以及Pod处于Pending状态的事件信息和hami-scheduler调度日志信息。

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
... (省略非关键信息)
Events:
  Type     Reason            Age   From            Message
  ----     ------            ----  ----            -------
  Warning  FailedScheduling  10s   hami-scheduler  0/1 nodes are available: 1 node unregistered. preemption: 0/1 nodes are available: 1 No preemption victims found for incoming pod..
  Warning  FilteringFailed   11s   hami-scheduler  no available node, all node scores do not meet
```

```log
$ kubectl logs -f  hami-scheduler-d69cb679b-9vtdg -c vgpu-scheduler-extender
I0422 13:42:30.272812       1 pod.go:44] "collect requestreqs" counts=[{"NVIDIA":{"Nums":2,"Type":"NVIDIA","Memreq":3000,"MemPercentagereq":101,"Coresreq":30}}]
I0422 13:42:30.272819       1 scheduler.go:352] "node unregistered" node="k8s-master1" error="node k8s-master1 not found"
I0422 13:42:30.272824       1 scheduler.go:490] "getNodesUsage failed nodes" nodes={"k8s-master1":"node unregistered"}
I0422 13:42:30.272827       1 scheduler.go:499] All node scores do not meet for pod gpu-pod
I0422 13:42:30.273047       1 event.go:307] "Event occurred" object="default/gpu-pod" fieldPath="" kind="Pod" apiVersion="v1" type="Warning" reason="FilteringFailed" message="no available node, all node scores do not meet"
```
### 用户提交多个任务时，节点打分日志交错，无法区分日志信息是哪个任务哪个节点的

由于节点打分是多协程处理的，因此多个节点之间的日志交叉打印，导致用户无法区分日志信息是哪个Pod在哪个节点上哪个设备的信息。 譬如用户提交了一个任务，用户
的K8S集群有10个节点，每个节点上有8张卡，那么此时如果这个任务调度失败，就会有80条日志信息，由于节点打分是多协程处理的，因此这些日志信息是交叉打印的。
在用户观察日志的时候无法区分当前v5级别的日志是哪个Pod在哪个节点上哪个设备的信息。


## 提案

采用pod event加调度器日志的方式，帮助用户快速定位节点问题。 pod event中显示不同类型节点调度失败的数量，若Pod成功调度，需要显示调度失败
的数量，与此同时显示所有调度成功节点的分数。 日志采用两级日志设计，v4级别的日志为当前节点调度失败或者成功的总览信息，若节点调度失败，
显示当前节点由于不同原因失败的卡的数量，若节点调度成功，显示当前节点调度成功的节点的分数。 v5级别的日志显示节点每张卡不合适的原因。

### event

event在保持精简的同时，需要让用户总体知晓当前集群不同类型节点调度失败的数量。在pod pending的时候，event显示每种类型节点的数量。
在pod running的时候, event需要显示不同类型失败的节点数量，以及成功节点的分数。

下面是Pod调度失败的event事件实例，仅需要展示不同类型节点的数量，不需要展示具体的节点名称，也不需要展示节点具体因为哪些设备调度失败的原因
```
Events:
  Type     Reason            Age    From            Message
  ----     ------            ----   ----            -------  TODO 如果申请的英伟达的卡，会传递其它类型节点么
  Warning  FilteringFailed   2m45s  hami-scheduler  no available node, 10 node not fit(ascend:3, nvidia:3, cambricon:2, metax:1, hygon:1)
```

下面是调度成功event事件实例，需要展示不同类型节点失败的数量，以及成功节点的数量,和成功节点的分数。
```
Events:
  Type     Reason            Age    From            Message
  ----     ------            ----   ----            -------
  Normal  FilteringSucceed   2m45s  hami-scheduler  find fit node, 7 node not fit(ascend:1, nvidia:4, cambricon:2),2 node fit(node3:0.98, node4:0.65)
```

### log

每条日志格式规范为：失败原因 Pod名称 Node名称 容器名称 设备ID或者设备UUID

失败原因需要规范，尽可能的精简，不要过长，如下是一些简化的失败原因：

request devices nums cannot exceed the total number of devices on the node -> device request exceeds node capacity
card type mismatch,continuing... -> card type mismatch
the container wants exclusive access to an entire card, but the card is already in use pod -> exclusive device allocation conflicts with active pod

下面是调度失败的日志示例，v4级别的日志为节点级别的日志，每个节点只有一条; v5为节点设备级别的日志，每个节点的每个设备可能有一条
```
(v=5)I0422 02:15:42.349712  1 scheduler.go:499] device request exceeds node capacity  pod="deepseek-5996b8569d-kgwgx" node="node2" container="worke1"
(v=5)I0422 02:15:42.349712  1 scheduler.go:499] card type mismatch pod="deepseek-5996b8569d-kgwgx" node="node1" container="worke1" device=7,
(v=5)I0422 02:15:42.349712  1 scheduler.go:499] card Insufficient remaining memory pod="deepseek-5996b8569d-kgwgx" node="node3" container="worke1" device="7" 
(v=5)I0422 02:15:42.349712  1 scheduler.go:499] card type mismatch pod="deepseek-5996b8569d-kgwgx" node="node2" container="worke1" device=7,
(v=5)I0422 02:15:42.349712  1 scheduler.go:499] card uuid mismatch pod="deepseek-5996b8569d-kgwgx" node="node1" container="worke1" device=6,
(v=5)I0422 02:15:42.349712  1 scheduler.go:499] card Insufficient remaining memory pod="deepseek-5996b8569d-kgwgx" node="node1" container="worke1" device="5" 
(v=5)I0422 02:15:42.349712  1 scheduler.go:499] card Insufficient remaining memory pod="deepseek-5996b8569d-kgwgx" node="node2" container="worke1" device="4" 
(v=5)I0422 02:15:42.349712  1 scheduler.go:499] card Insufficient remaining memory pod="deepseek-5996b8569d-kgwgx" node="node1" container="worke1" device="3" 
(v=5)I0422 02:15:42.349712  1 scheduler.go:499] card Insufficient remaining memory pod="deepseek-5996b8569d-kgwgx" node="node3" container="worke1" device="5" 
(v=5)I0422 02:15:42.349712  1 scheduler.go:499] card Insufficient remaining memory pod="deepseek-5996b8569d-kgwgx" node="node4" container="worke1" device="7" 
(v=5)I0422 02:15:42.349712  1 scheduler.go:499] card Insufficient remaining memory pod="deepseek-5996b8569d-kgwgx" node="node1" container="worke1" device="4" 
(v=5)I0422 02:15:42.349712  1 scheduler.go:499] card Insufficient remaining memory pod="deepseek-5996b8569d-kgwgx" node="node2" container="worke1" device="3" 
(v=5)I0422 02:15:42.349712  1 scheduler.go:499] card Insufficient remaining memory pod="deepseek-5996b8569d-kgwgx" node="node4" container="worke1" device="5" 
(v=5)I0422 02:15:42.349712  1 scheduler.go:499] card Insufficient remaining cores pod="deepseek-5996b8569d-kgwgx" node="node3" container="worke1"  device="0"
(v=5)I0422 02:15:42.349712  1 scheduler.go:499] card Insufficient remaining cores pod="deepseek-5996b8569d-kgwgx" node="node2" container="worke1"  device="2"
(v=5)I0422 02:15:42.349712  1 scheduler.go:499] card Insufficient remaining cores pod="deepseek-5996b8569d-kgwgx" node="node1" container="worke1"  device="2"
(v=5)I0422 02:15:42.349712  1 scheduler.go:499] card Insufficient remaining cores pod="deepseek-5996b8569d-kgwgx" node="node1" container="worke1"  device="1"
(v=5)I0422 02:15:42.349712  1 scheduler.go:499] exclusive device allocation conflicts with active pod pod="deepseek-5996b8569d-kgwgx" node="node1" container="worke1" device="0"
(v=4)I0422 02:15:42.349712  1 scheduler.go:499] node unfit pod="deepseek-5996b8569d-kgwgx" node="node1" reason="2 card type mismatch, 3 card Insufficient remaining memory, 2 card Insufficient remaining cores, 1 exclusive device allocation conflicts with active pod"
(v=5)I0422 02:15:42.349712  1 scheduler.go:499] card uuid mismatch pod="deepseek-5996b8569d-kgwgx" node="node2" container="worke1" device=6,
(v=5)I0422 02:15:42.349712  1 scheduler.go:499] card Insufficient remaining memory pod="deepseek-5996b8569d-kgwgx" node="node2" container="worke1" device="5" 
(v=4)I0422 02:15:42.349712  1 scheduler.go:499] node fit pod="deepseek-5996b8569d-kgwgx" node="node3" score="0.65"
(v=5)I0422 02:15:42.349712  1 scheduler.go:499] card Insufficient remaining cores pod="deepseek-5996b8569d-kgwgx" node="node4" container="worke1"  device="0"
(v=4)I0422 02:15:42.349712  1 scheduler.go:499] node fit pod="deepseek-5996b8569d-kgwgx" node="node4" score="0.26"
(v=5)I0422 02:15:42.349712  1 scheduler.go:499] card Insufficient remaining cores pod="deepseek-5996b8569d-kgwgx" node="node2" container="worke1"  device="1"
(v=5)I0422 02:15:42.349712  1 scheduler.go:499] exclusive device allocation conflicts with active pod pod="deepseek-5996b8569d-kgwgx" node="node2" container="worke1" device="0"
(v=4)I0422 02:15:42.349712  1 scheduler.go:499] node unfit pod="deepseek-5996b8569d-kgwgx" node="node2" reason="2 card type mismatch, 3 card Insufficient remaining memory, 2 card Insufficient remaining cores, 1 exclusive device allocation conflicts with active pod"
(v=4)I0422 02:15:42.349712  1 scheduler.go:499] node fit pod="deepseek-5996b8569d-kgwgx" node="node5" score="0.85"
```