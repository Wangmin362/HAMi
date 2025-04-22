# Scheduler event
## 现状
用户提交一个任务，通过hami-scheduler调度之后，Pod一直处于Pending状态，无法调度成功。

Pod的event日志报错：
```log
Events:
  Type     Reason            Age    From            Message
  ----     ------            ----   ----            -------
  Warning  FailedScheduling  2m45s  hami-scheduler  0/1 nodes are available: 1 node unregistered. preemption: 0/1 nodes are available: 1 No preemption victims found for incoming pod..
  Warning  FilteringFailed   2m45s  hami-scheduler  no available node, all node scores do not meet
```

hami-scheduler-extender日志报错：
`I0422 02:15:42.349712       1 scheduler.go:499] All node scores do not meet for pod gpu-pod`


没有更加详细的日志信息帮助用户定位Pod调度失败的原因，只知道调度失败了，并不知道哪些Node是因为何种原因调度失败的。

## 目标

1. 针对于Pod调度进入hami-scheduler的场景，优化Pod event日志信息，输出可能的每种原因，以及对应原因的Node数量。
2. hami-scheduler在调度Pod过程中，需要详细打印每个Node失败的原因，和第一点中的失败原因相对应，方便用户根据失败原因排查对应的节点。

## 非目标

1. 针对于没有进入hami-scheduler的场景，显示详细的错误原因

## 疑问

1. hami-scheduler是通过K8S extender机制扩展的，调用filter的时候，已经经过了scheduler framework prefileter/filter阶段 ，所以filter中拿不到所有的节点，
   因此无法上报哪些K8S没有传递过来的节点，因为K8S传递的是候选节点。

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