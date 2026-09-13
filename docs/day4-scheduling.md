# Day 4：Kubernetes Scheduler

目标不是写一个新的 scheduler，而是看见默认 kube-scheduler 如何根据 requests、Label 和 Taint 放置 Pod。

完整清单与命令见 [`k8s/scheduling/README.md`](../k8s/scheduling/README.md)。

## 启动

```bash
make k8s-up
make k8s-deploy
make scheduling-demo
```

## 练习 1：requests 是调度输入

`cpu-memory-demo` 声明：

```yaml
resources:
  requests:
    cpu: "2"
    memory: "4Gi"
```

- requests：Scheduler 问 Node“还剩这么多吗”
- limits：kubelet / cgroup 的运行时上限

如果 kind Node 更小，Pod 会 `Pending` 且 `NODE=<none>`。用 `kubectl describe pod` 看 Events。

## 练习 2：Label → nodeSelector → 放置

```text
Node Label
    ↓
nodeSelector
    ↓
Scheduler
    ↓
Pod placement
```

反例：`make fault-pending`（`workload: nonexistent`）。

## 练习 3：Taint 挡住普通 Pod

```bash
make scheduling-taint
```

带 toleration 的 Pod 能上 GPU 风格 Node；没有的会 Pending。

## 和 Model Router 的区别

```text
Scheduler:     这个 Model Pod 跑在哪台 Node？
Model Router:  这个 HTTP 请求发给哪个 model instance？
```

看网关侧：

```bash
curl http://127.0.0.1:8080/model-status
curl http://127.0.0.1:8080/node-status
```

清理：

```bash
make scheduling-cleanup
```
