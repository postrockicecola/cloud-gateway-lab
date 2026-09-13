# Day 5：GPU 资源模型

本实验是可选模块。`make k8s-up` / `make k8s-deploy` 不依赖 NVIDIA GPU。

```text
GPU
 ↓
NVIDIA Driver
 ↓
NVIDIA Device Plugin
 ↓
Node advertised resource
 ↓
nvidia.com/gpu
 ↓
Pod resource request
 ↓
Kubernetes Scheduler
 ↓
GPU Node
```

没有第二个 GPU Scheduler。Device Plugin 只是让 Node 多了一种可调度资源。

## 先检查

```bash
make gpu-check
```

没有 GPU 时脚本会说明依赖，并以 0 退出。然后：

```bash
make gpu-deploy
```

会走 **Mode B**：给一个 worker 打上 `gpu=true`，PATCH `example.com/mock-gpu=1`，再启动请求 1 个 mock-gpu 的 Pod。

> mock-gpu 只用于学习 extended resource / scheduling，不等价于真实 NVIDIA GPU 调度。

## Label 和 GPU 资源是两件事

```text
GPU Node
   │
   ├── Label: gpu=true          ← 去哪一类 Node？
   │
   ├── Device Plugin
   │      ↓
   │   nvidia.com/gpu: 8        ← 还有没有 GPU？
   │
   └── taint gpu=true:NoSchedule ← 谁被允许进来？
```

## 资源不够

```bash
make fault-gpu
```

Node 有 1 个 mock-gpu，Pod 要 2 个，结果 Pending。

有真实 GPU 且 Device Plugin 已上报 `nvidia.com/gpu` 时，`make gpu-deploy` 会走 Mode A，应用 [`k8s/gpu/gpu-workload.yaml`](../k8s/gpu/gpu-workload.yaml)。

清理：

```bash
make gpu-cleanup
```
