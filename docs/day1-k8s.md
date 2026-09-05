# 第一天练习：kubectl 命令与 YAML 字段对照

这份笔记把 README 里的 5 个练习，对齐到 `deploy/k8s/base/resources.yaml` 的具体字段。每节都按「要看什么 → 对应字段 → 预期现象 → 诊断 → 根因与修复」写，方便边敲边记。

做练习前先把集群拉起来，并保持一个端口转发：

```bash
make cluster && make images && make load && make deploy && make status
make port-forward   # 另开一个终端，不要关
```

之后业务请求都打本机 `http://localhost:8080`，它转发到 `gateway` Service。

---

## 对象关系总览

```text
Namespace: gateway-lab
│
├── ConfigMap  gateway-config
│     └── data.ROUTES ──envFrom──► gateway Pod
│
├── Deployment/gateway  replicas: 2
│     └── Pod (app=gateway) ◄── Service/gateway selector
│
├── Deployment/users    replicas: 2
│     └── Pod (app=users)    ◄── Service/users selector
│           └── env.POD_NAME = metadata.name
│
└── Deployment/products replicas: 2
      └── Pod (app=products) ◄── Service/products selector
```

请求实际走的是 Service DNS，不是直接打 Pod IP：

```text
curl :8080/api/users/42
  → Service/gateway:8080
  → 某个 gateway Pod
  → 按 ROUTES 反代到 http://users:8080
  → Service/users 再选一个就绪的 users Pod
```

---

## 练习 1：Deployment、Pod 与副本

**命令**

```bash
kubectl get pods -n gateway-lab -o wide
kubectl get deploy -n gateway-lab
```

**对照字段**（以 `users` 为例，`gateway` / `products` 同理）

| 看到的现象 | YAML 字段 | 含义 |
|---|---|---|
| Namespace 是 `gateway-lab` | `metadata.namespace` | 所有对象都隔离在这个命名空间 |
| 有 2 个 `users-xxxx` Pod | `Deployment.spec.replicas: 2` | 期望副本数 |
| Pod 名带随机后缀 | `spec.template` | Deployment 用 ReplicaSet 按模板造 Pod |
| `READY 1/1` | `readinessProbe` + 容器数 | 探针通过才算就绪 |
| `STATUS Running` | kubelet 把容器拉起来了 | 和「进不进 Service」不是一回事 |
| `NODE` 列是 kind 节点 | 调度结果 | `-o wide` 才会显示 |

关键三段必须对得上，否则 Deployment 不会管这些 Pod、Service 也选不中：

```yaml
# Deployment
spec:
  selector:
    matchLabels:
      app: users          # 控制器认谁
  template:
    metadata:
      labels:
        app: users        # Pod 身上的标签
---
# Service
spec:
  selector:
    app: users            # Service 选谁
```

**预期现象**

- 6 个 Pod：`gateway` ×2、`users` ×2、`products` ×2
- `kubectl get deploy -n gateway-lab` 里 `READY` 都是 `2/2`

**建议记下来**

| 项目 | 记录 |
|---|---|
| 现象 | |
| 诊断命令 | `kubectl get pods,deploy -n gateway-lab -o wide` |
| 根因 | 不是故障；用来建立「Deployment 声明副本，Pod 是实际实例」 |
| 修复 | 无需修复 |

**延伸**

```bash
kubectl get rs -n gateway-lab
kubectl describe deploy users -n gateway-lab
```

`Replicas: 2 desired | 2 updated | 2 total | 2 available` 就是控制器在对齐期望状态。

---

## 练习 2：连续访问 users，看响应里的 `pod`

**命令**

```bash
for i in $(seq 1 8); do curl -s http://localhost:8080/api/users/42; echo; done
```

**对照字段：流量怎么打到多个 Pod**

| 环节 | YAML / 代码 | 作用 |
|---|---|---|
| 本机进集群 | `make port-forward` → `service/gateway 8080:8080` | 把 ClusterIP Service 转到本机 |
| 网关选后端 | ConfigMap `ROUTES: users=http://users:8080` | `users` 是 **Service DNS**，不是某个 Pod 名 |
| Service 选 Pod | `Service/users.spec.selector.app: users` | 在就绪 Endpoints 里轮询 |
| 端口对齐 | 容器 `ports.name: http` + Service `targetPort: http` | 按端口名转发到 8080 |
| 响应里的 pod | `env.POD_NAME` ← `fieldRef.fieldPath: metadata.name` | Downward API，把 Pod 名注入进程 |
| 应用读出来 | `cmd/backend/main.go` 的 `"pod": env("POD_NAME", "local")` | 本地跑没有这个环境变量，会显示 `local` |

后端注入片段：

```yaml
env:
  - name: SERVICE_NAME
    value: users
  - name: POD_NAME
    valueFrom:
      fieldRef:
        fieldPath: metadata.name
```

**预期现象**

响应会在两个 users Pod 名之间切换，例如：

```json
{"path":"/api/users/42","pod":"users-7d8f9c4b6-abcde","service":"users"}
{"path":"/api/users/42","pod":"users-7d8f9c4b6-fghij","service":"users"}
```

这是 Service 的集群内负载均衡。不一定严格 1:1 交替，但连续打几次通常能看到两个名字。

**谁在轮询、谁不在**

- `http://users:8080` 由 kube-proxy / Endpoints 在 **users Pod** 之间分配
- 网关自己也是 2 副本；port-forward 到 Service 时，请求也可能打到不同的 gateway Pod，但不影响响应里的 `pod` 字段（那个字段来自后端）

**建议记下来**

| 项目 | 记录 |
|---|---|
| 现象 | 例如看到了哪两个 Pod 名 |
| 诊断命令 | `kubectl get endpoints users -n gateway-lab -o yaml` |
| 根因 | Service 把流量分到多个就绪 Pod；`POD_NAME` 让你看见被选中的是谁 |
| 修复 | 无需修复 |

---

## 练习 3：删一个 users Pod，看副本自动补齐

**命令**

```bash
kubectl get pods -n gateway-lab -l app=users -o wide
kubectl delete pod -n gateway-lab -l app=users --field-selector=status.phase=Running | head
# 更直观：删指定名字
kubectl delete pod -n gateway-lab <某个-users-pod名>
kubectl get pods -n gateway-lab -l app=users -w
```

`-w` 是 watch，看到新 Pod 变成 `Running` 后 `Ctrl+C`。

**对照字段：谁在补齐**

| 步骤 | 对应机制 | 字段 / 命令 |
|---|---|---|
| 你删的是 Pod，不是 Deployment | `kubectl delete pod` | Deployment 还在，`replicas` 仍是 2 |
| 控制器发现少了一个 | ReplicaSet 对齐 | `Deployment.spec.replicas: 2` |
| 按模板再造一个 | `spec.template` | 新 Pod 名后缀不同 |
| 旧连接尽量收尾 | 网关有 `terminationGracePeriodSeconds: 15` | users 未单独设，默认 30s |
| 进程收到 SIGTERM | `cmd/backend` 里 `signal.Notify(..., SIGTERM)` + `Shutdown` | 优雅退出，不是直接杀 |

**预期现象**

1. 被删 Pod 进入 `Terminating`
2. 几乎同时出现一个新的 `users-xxxxx`
3. 短暂可能 `READY 1/2`，新 Pod 过了 `readinessProbe`（`GET /healthz`）后回到 `2/2`
4. 再 curl users，响应里的 `pod` 会变成新名字；旧名字不再出现

**建议记下来**

| 项目 | 记录 |
|---|---|
| 现象 | 删的旧名、新补的名字、补齐耗时 |
| 诊断命令 | `kubectl get pods -l app=users -n gateway-lab -w` |
| 根因 | Deployment/ReplicaSet 持续把实际副本数拉回 `replicas` |
| 修复 | 无需修复。若要「真的少一个副本」，改的是 `replicas`，不是删 Pod |

对比：

```bash
# 删 Pod → 会被补回来
kubectl delete pod <name> -n gateway-lab

# 改期望副本 → 会稳定少一个（练完请改回 2）
kubectl scale deploy/users -n gateway-lab --replicas=1
```

---

## 练习 4：把 ConfigMap 里 users 地址改错，观察 502 和指标

这是本阶段最接近真实故障的一条：配置错了，进程还活着，探针也绿，但上游挂了。

**步骤**

1. 改 `deploy/k8s/base/resources.yaml` 里的路由，例如把 users 指到不存在的 Service：

   ```yaml
   ROUTES: users=http://users-wrong:8080,products=http://products:8080
   ```

2. 应用并**重启网关**（这一步不能省）：

   ```bash
   kubectl apply -k deploy/k8s/base
   kubectl rollout restart deploy/gateway -n gateway-lab
   kubectl rollout status deploy/gateway -n gateway-lab
   ```

3. 打接口和指标：

   ```bash
   curl -i http://localhost:8080/api/users/42
   curl -s http://localhost:8080/api/products/7; echo
   curl -s http://localhost:8080/metrics; echo
   ```

**为什么必须重启网关**

| 字段 | 行为 |
|---|---|
| `envFrom.configMapRef.name: gateway-config` | 只在 **Pod 创建时** 把 ConfigMap 展开成环境变量 |
| 只 `kubectl apply` ConfigMap | 集群里的 ConfigMap 对象变了，**已有 Pod 的环境变量不变** |
| `rollout restart` | 按新 ConfigMap 再起 Pod，进程才能读到错误的 `ROUTES` |

`cmd/gateway/main.go` 启动时调用 `gateway.ParseRoutes(env("ROUTES", ...))`，路由表不会热更新。

**对照字段与代码**

| 现象 | 来源 |
|---|---|
| `502 Bad Gateway` + `bad gateway` | `internal/gateway/gateway.go` 的 `ErrorHandler`，上游拨号失败 |
| `products` 仍 200 | `ROUTES` 里 products 没改，Service DNS 仍有效 |
| `gateway_upstream_errors_total` 增加 | 同上，每次上游失败 `errors.Add(1)` |
| `gateway_requests_total` 也增加 | 请求先计数，再反代 |
| 网关 Pod 仍 `Ready` | 探针打的是自己的 `/readyz`、`/healthz`，不检查上游 |

**预期现象**

```text
HTTP/1.1 502 Bad Gateway
bad gateway

# /metrics
gateway_requests_total 1
gateway_upstream_errors_total 1
```

products 正常；users 持续 502。

**建议记下来**

| 项目 | 记录 |
|---|---|
| 现象 | users 502、products 200、errors 计数增加 |
| 诊断命令 | 见练习 5 |
| 根因 | ConfigMap 把 `users` 指到无法解析/无法连接的地址；网关拨号失败 |
| 修复 | 把 `ROUTES` 改回 `users=http://users:8080`，apply 后再 `rollout restart deploy/gateway` |

---

## 练习 5：用 logs、describe、Endpoints 定位

接练习 4 的故障状态来做。三个命令看的层不一样。

### 5.1 `kubectl logs`：进程自己怎么说

```bash
kubectl logs -n gateway-lab -l app=gateway --tail=50
```

**对照**

- 网关用 JSON 打日志；上游失败时有 `upstream request failed`
- 对应代码：`ErrorHandler` 里的 `g.logger.Error("upstream request failed", ...)`
- 错误信息通常是 DNS 找不到 `users-wrong`，或连接被拒

如果要跟一次请求：

```bash
kubectl logs -n gateway-lab -l app=gateway --follow
```

另开终端再 `curl` users，能对上 `path` 和 `error`。

### 5.2 `kubectl describe pod`：kubelet / 探针 / 事件

```bash
kubectl get pods -n gateway-lab -l app=gateway
kubectl describe pod -n gateway-lab <某个-gateway-pod名>
```

重点看这几块：

| describe 段落 | 对应 YAML | 练习 4 里通常怎样 |
|---|---|---|
| `Labels: app=gateway` | `template.metadata.labels` | 正常 |
| `Status: Running` / `Ready True` | `readinessProbe.httpGet.path: /readyz` | **仍然绿**，容易误导 |
| `Liveness: http-get http://:8080/healthz` | `livenessProbe` | 不检查上游，所以不会重启 |
| `Environment Variables from: gateway-config` | `envFrom` | 确认挂上了 ConfigMap |
| `Events` | 调度、拉镜像、探针失败 | 练习 4 **往往没有异常事件** |

这一步的教学点：Pod 健康 ≠ 业务健康。配置错误走的是应用日志和 502，不是 CrashLoop。

再看一个「探针真的会红」的对照（可选，不必改清单）：

- 若 `/readyz` 失败：Pod `READY 0/1`，Service Endpoints 里会拿掉它
- 若 `/healthz` 失败：过了 `periodSeconds` 后 kubelet 重启容器

网关探针字段：

```yaml
readinessProbe:
  httpGet: { path: /readyz, port: http }
  periodSeconds: 3
livenessProbe:
  httpGet: { path: /healthz, port: http }
  initialDelaySeconds: 3
  periodSeconds: 10
```

### 5.3 Endpoints：Service 到底选了谁

```bash
kubectl get endpoints -n gateway-lab
kubectl get endpoints users -n gateway-lab -o yaml
kubectl get endpoints gateway -n gateway-lab -o yaml
```

**对照**

| 对象 | selector | 就绪条件 |
|---|---|---|
| `endpoints/users` | `app: users` | users Pod 的 `readinessProbe`（`/healthz`）通过 |
| `endpoints/gateway` | `app: gateway` | gateway Pod 的 `/readyz` 通过 |

练习 4 时：

- `users` 的 Endpoints **仍然有两个就绪 IP**——后端没坏
- `gateway` 的 Endpoints 也正常——网关没坏
- 坏在网关配置的 **目标 URL**，Endpoints 看不出来

所以正确结论是：Service 发现没问题，问题在 ConfigMap 里的上游地址。

再确认配置（改错后、重启后）：

```bash
kubectl get configmap gateway-config -n gateway-lab -o yaml
kubectl exec -n gateway-lab deploy/gateway -- printenv ROUTES
```

`printenv` 应和 ConfigMap 一致。如果 apply 了但没 restart，这里仍是旧值。

**建议记下来**

| 项目 | 记录 |
|---|---|
| 现象 | logs 有 upstream error；describe 探针绿；users Endpoints 仍在 |
| 诊断命令 | 上面三条 |
| 根因 | 故障在「网关 → 错误的 Service DNS」，不在 Pod 调度或探针 |
| 修复 | 恢复 `ROUTES`，apply + restart gateway |

---

## 练完恢复

```bash
# 确认 ROUTES 已改回
# ROUTES: users=http://users:8080,products=http://products:8080
kubectl apply -k deploy/k8s/base
kubectl rollout restart deploy/gateway -n gateway-lab
kubectl rollout status deploy/gateway -n gateway-lab
curl -s http://localhost:8080/api/users/42; echo
```

不需要集群时：`make clean`。

---

## 附录 A：字段速查

| 知识点 | 出现位置 |
|---|---|
| Namespace | 各资源 `metadata.namespace: gateway-lab` |
| 副本 | 三个 Deployment 的 `spec.replicas: 2` |
| label / selector | Deployment `matchLabels` = Pod `labels` = Service `selector` |
| ClusterIP Service | `gateway` / `users` / `products`，未写 `type` 即默认 |
| Service DNS | ConfigMap 里的 `http://users:8080` |
| 命名端口 | 容器 `ports.name: http`，探针和 Service 都引用这个名字 |
| ConfigMap → 环境变量 | gateway 的 `envFrom.configMapRef` |
| Downward API | users/products 的 `fieldRef: metadata.name` |
| 就绪探针 | gateway `/readyz`；后端 `/healthz` |
| 存活探针 | 仅 gateway `/healthz` |
| 资源请求/限制 | 各容器 `resources.requests` / `limits` |
| 优雅终止 | gateway `terminationGracePeriodSeconds: 15` + 进程处理 SIGTERM |
| 本地镜像 | `imagePullPolicy: Never` + `make load` |
| Kustomize | `deploy/k8s/base/kustomization.yaml`，`kubectl apply -k` |

---

## 附录 B：常用诊断命令

```bash
# 工作负载
kubectl get pods,deploy,rs,svc,ep -n gateway-lab -o wide
kubectl describe deploy/users -n gateway-lab
kubectl describe pod <pod> -n gateway-lab

# 流量与发现
kubectl get endpoints -n gateway-lab
kubectl get endpoints users -n gateway-lab -o yaml

# 配置是否进了进程
kubectl get cm gateway-config -n gateway-lab -o yaml
kubectl exec -n gateway-lab deploy/gateway -- printenv ROUTES

# 应用日志
kubectl logs -n gateway-lab -l app=gateway --tail=100
kubectl logs -n gateway-lab -l app=users --tail=20

# 滚动发布
kubectl rollout status deploy/gateway -n gateway-lab
kubectl rollout history deploy/gateway -n gateway-lab
```

---

## 附录 C：实验记录模板

每做完一题复制一块：

```text
练习编号：
操作：
现象：
诊断命令：
根因：
修复：
还想再看的字段：
```
