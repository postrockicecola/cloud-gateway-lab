# GPU resource model

This directory is **optional**. `make k8s-up` / `make k8s-deploy` never require a GPU.

> `example.com/mock-gpu` only teaches Kubernetes extended-resource scheduling.
> It is **not** equivalent to real NVIDIA GPU scheduling.

## How Kubernetes learns a Node has GPUs

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

There is no second "GPU Scheduler" in this lab. The default Kubernetes Scheduler places the Pod. The device plugin advertises `nvidia.com/gpu` as an extended resource.

## Label vs GPU resource

| Control | Answers |
| --- | --- |
| Node label `gpu=true` | Which class of Node should this Pod go to? |
| `nvidia.com/gpu: 1` | Does that Node still have a free GPU? |

A Node can be labeled `gpu=true` and still have `nvidia.com/gpu: 0`. Both checks must pass.

A GPU-dedicated Node typically also has:

```text
taint: gpu=true:NoSchedule
```

Ordinary Pods without a matching toleration cannot land there.

## Mode A — real GPU

Needs all of:

- NVIDIA GPU
- NVIDIA driver
- NVIDIA Container Toolkit
- Kubernetes NVIDIA Device Plugin

```bash
make gpu-check
kubectl describe node
# look for: nvidia.com/gpu
make gpu-deploy
```

## Mode B — scheduler simulation (default on this laptop)

```bash
make gpu-deploy
kubectl describe node
# look for: example.com/mock-gpu
kubectl get pod mock-gpu-workload -n gateway-lab -o wide
```

`make gpu-deploy` patches one worker Node's status.capacity / allocatable. kubelet does not manage this custom resource, so the patch persists until you remove it.

```bash
make gpu-cleanup
```

## Insufficient GPU (fault)

[`../faults/gpu-insufficient.yaml`](../faults/gpu-insufficient.yaml) requests 2 mock GPUs after the Node advertises 1. The Pod stays Pending because `available < requested`.
