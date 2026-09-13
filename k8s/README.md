# Kubernetes + AI Infra lab

This directory is the learning surface for **how a Pod is scheduled onto a Node**, and how that is different from **how a chat request is routed to a model instance**.

Ordinary cluster bring-up does **not** need a GPU:

```bash
make k8s-up
make k8s-deploy
make k8s-status
```

GPU is a separate, optional module: `make gpu-check` / `make gpu-deploy`.

## Knowledge graph

```text
                       Kubernetes
                            │
          ┌─────────────────┴──────────────────┐
          │                                    │
     Control Plane                         Worker Node
          │                                    │
     ┌────┼────┐                    ┌──────────┼──────────┐
     │    │    │                    │          │          │
 API  Scheduler Controller       kubelet   runtime    kube-proxy
Server       │
             │
          Pod placement
             │
             ▼
          Node
             │
       ┌─────┴─────────┐
       │               │
     CPU/Memory       GPU
                       │
               NVIDIA Device Plugin
                       │
               nvidia.com/gpu
                       │
                    Pod
                       │
                 Model Server
```

```text
Deployment          →  ReplicaSet  →  Pod
DaemonSet           →  one Pod per eligible Node
Service             →  EndpointSlice  →  Pod IP
CoreDNS             →  Service discovery
Scheduler           →  Pod → Node
GPU Device Plugin   →  GPU → Kubernetes resource
HPA                 →  Pod replica count
Model Router        →  Request → Model Instance
```

These are not a single synchronous RPC chain. Components watch API Server / etcd state and reconcile.

## Request path in this lab

```text
Client
  ↓
AI Gateway Service
  ↓
Gateway Pod  (placed by Kubernetes Scheduler)
  ↓
Model Router  (picks mock-a / mock-b / model)
  ↓
Model Service
  ↓
Model Pod     (placed by Kubernetes Scheduler)
  ↓
GPU Node      (only if you run the GPU module)
```

Two different "schedulers":

```text
Kubernetes Scheduler          Model Router
Pod → Node                    Request → Model Instance
```

HPA is a third thing: it only changes **how many** Gateway Pods exist.

## Layout

```text
k8s/
├── namespace.yaml
├── gateway/          # Deployment × 2, Service, ConfigMap, Secret, HPA
├── model/            # mock-model-server + mock-a + mock-b
├── redis/
├── node-agent/       # DaemonSet
├── scheduling/       # requests, nodeSelector, affinity, taint
├── gpu/              # real nvidia.com/gpu + mock-gpu simulation
├── faults/           # Pending, ImagePull, Crash, Ready=False, GPU short
└── metrics-server/   # optional, for HPA only
```

Operational apply path is `kubectl apply -k k8s` (also `make k8s-deploy`). Experiment YAMLs are applied by the `make scheduling-*`, `make gpu-*`, and `make fault-*` targets.

## What is real vs mock

| Piece | Reality |
| --- | --- |
| kind cluster, Deployment, Service, EndpointSlice, CoreDNS, probes | Real Kubernetes |
| Gateway routing, Redis limiter, mockprovider HTTP | Real process behavior |
| `POST /v1/chat/completions` from mockprovider | Fake model, real HTTP |
| `example.com/mock-gpu` | Simulation of extended-resource scheduling only |
| `nvidia.com/gpu` | Real only if Device Plugin advertises it |

This is not a production GPU platform. There is no Volcano, Kueue, custom scheduler, or MIG planner.

## Observe the control loop

```bash
kubectl get deployment -n gateway-lab
kubectl get rs -n gateway-lab
kubectl get pods -n gateway-lab -o wide
kubectl get svc -n gateway-lab
kubectl get endpointslice -n gateway-lab
```

```text
Deployment  →  ReplicaSet  →  2 × Gateway Pod
```

Delete a Gateway Pod. The ReplicaSet creates a replacement because actual state drifted from desired state.

## Service + EndpointSlice + CoreDNS

```text
model
  ↓
model.gateway-lab.svc.cluster.local
  ↓
Service ClusterIP
  ↓
EndpointSlice
  ↓
Model Pod IP
```

From **inside** a Pod in `gateway-lab`:

```text
http://gateway:8080
http://model:8080
http://mock-a:8080
http://mock-b:8080
http://redis:6379
```

The host machine does not resolve those names. Use `make port-forward` from the laptop.

```bash
kubectl exec -n gateway-lab deploy/gateway -- wget -qO- http://model:8080/healthz
```

The Gateway image is distroless, so that wget may fail. Use a debug Pod instead:

```bash
kubectl run dns-check -n gateway-lab --rm -it --image=curlimages/curl -- \
  curl -s http://model:8080/healthz
```

## DaemonSet vs Deployment

```text
Deployment:  I want N Pods
DaemonSet:   I want one Pod on every eligible Node
```

```bash
kubectl get ds node-agent -n gateway-lab
kubectl get pods -n gateway-lab -l app=node-agent -o wide
kubectl logs -n gateway-lab -l app=node-agent --tail=20
```

Each Node should show a `node-agent-*` Pod printing hostname, loadavg, memory, and disk.

## Scheduler experiments

See [scheduling/README.md](scheduling/README.md).

```bash
make scheduling-demo
make scheduling-taint
make scheduling-cleanup
```

`Pending` + `NODE=<none>` is usually the Scheduler, not the application.

## GPU experiments (optional)

See [gpu/README.md](gpu/README.md).

```bash
make gpu-check
make gpu-deploy
```

`gpu-check` never aborts the rest of the project. Without an NVIDIA stack it prints:

```text
GPU experiment requires:
- NVIDIA GPU
- NVIDIA driver
- NVIDIA Container Toolkit
- Kubernetes NVIDIA Device Plugin
```

and `gpu-deploy` falls back to `example.com/mock-gpu`.

## Faults

See [faults/README.md](faults/README.md).

```bash
make fault-pending
make fault-image
make fault-crash
make fault-readiness
make fault-gpu
make fault-cleanup
```

## Observability

Gateway already exposes Prometheus text at `/metrics`. Two JSON views were added for this lab:

```bash
curl http://127.0.0.1:8080/model-status
curl http://127.0.0.1:8080/node-status
curl http://127.0.0.1:8080/metrics
```

`/model-status` is Model Router state (instances, health, latency, active requests).
`/node-status` is **this Gateway Pod**, not a cluster node inventory.

There is no Prometheus / Grafana stack on purpose.

## HPA

```bash
make hpa-setup
make hpa-apply
make port-forward   # other terminal
make load-test
kubectl get hpa,deploy,pods -n gateway-lab
```

```text
Load → CPU → HPA → Deployment replicas → Gateway Pods
```

HPA is not a GPU scheduler.

## Interview checkpoints

**Q1** Creating a Pod is watch + reconcile, not one RPC:

```text
kubectl → API Server → etcd → controllers → Scheduler → kubelet → runtime → container
```

**Q2–Q3** Deployment owns ReplicaSet owns Pods. Delete a Pod and the controller restores desired replicas.

**Q4–Q5** Service → selector → EndpointSlice → Pod IP. CoreDNS turns `model` into `model.gateway-lab.svc.cluster.local`.

**Q6** Pending + `NODE=<none>` is scheduling. Pending + a Node name is kubelet.

**Q7–Q9** GPU appears because the Device Plugin advertises `nvidia.com/gpu`. The default Scheduler still places the Pod.

**Q10** Dedicated GPU Nodes: label + selector, taint + toleration, and GPU resource request. Three different questions.

**Q11** Scheduler places Pods. Model Router places requests.

**Q12** A GPU Pod can be Pending for label, taint, or `nvidia.com/gpu` / CPU / memory shortage.

**Q13** Kubernetes is infrastructure scheduling. Serving, gateway routing, and the inference engine are additional layers.
