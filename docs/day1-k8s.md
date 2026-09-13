# Day 1：部署 AI Gateway 到 Kubernetes

目标是把本地运行的 AI Gateway 原样部署到 kind，而不是运行另一套教学网关。完成后，客户端通过一个 Service 访问两个 Gateway Pod，Gateway 再调用集群内的 mock provider。

## 启动实验

```bash
make k8s-up
make k8s-deploy
make k8s-status
```

另开一个终端：

```bash
make port-forward
```

集群对象关系：

```text
Namespace/gateway-lab
├── ConfigMap/gateway-config
├── ConfigMap/gateway-endpoints
├── Secret/gateway-secrets
├── Deployment/gateway × 2  → Service/gateway
├── Deployment/redis × 1    → Service/redis
├── Deployment/model × 1    → Service/model
├── Deployment/mock-a × 1   → Service/mock-a
├── Deployment/mock-b × 1   → Service/mock-b
└── DaemonSet/node-agent    → 每个 Node 一个 Pod
```

清单拆在 [`k8s/`](../k8s/README.md)。Scheduler / GPU / 故障实验见 Day 4、Day 5。

## 练习 1：观察 Deployment、Pod 和 Service

```bash
kubectl get deployment,rs,pods,svc,endpointslice -n gateway-lab -o wide
kubectl describe deployment gateway -n gateway-lab
```

重点观察：

- `Deployment.spec.replicas: 2` 让控制器维持两个 Gateway Pod。
- Service 通过 `app: gateway` 标签选择 Pod。
- `mock-a`、`mock-b` 和 Redis 都通过 Service DNS 被访问，不依赖 Pod IP。
- Pod 被删除后，Deployment 会创建替代实例。

删除一个网关 Pod 并观察恢复：

```bash
kubectl delete pod -n gateway-lab \
  "$(kubectl get pod -n gateway-lab -l app=gateway \
    -o jsonpath='{.items[0].metadata.name}')"
kubectl get pods -n gateway-lab -w
```

## 练习 2：发送真实 Chat Completion 请求

```bash
curl http://localhost:8080/v1/chat/completions \
  -H "Authorization: Bearer sk-lab" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "gpt-5",
    "messages": [{"role": "user", "content": "hello"}]
  }'
```

响应中的内容会是 `response from mock-a` 或 `response from mock-b`。这说明请求完整经过了鉴权、限流、endpoint 选择、provider adapter 和上游调用。

查看两个网关副本的日志：

```bash
kubectl logs -n gateway-lab -l app=gateway --prefix --tail=30
```

## 练习 3：理解 ConfigMap 文件挂载

查看网关配置：

```bash
kubectl get configmap gateway-config -n gateway-lab -o yaml
kubectl get configmap gateway-endpoints -n gateway-lab -o yaml
kubectl exec -n gateway-lab deploy/gateway -- \
  /busybox/cat /etc/gateway/endpoints.yaml
```

基础镜像是 distroless，不包含 shell 和 `cat`，因此第二条命令预期失败。这是正常现象。可以直接从 ConfigMap 查看文件内容，或临时使用 debug container：

```bash
kubectl debug -n gateway-lab deploy/gateway -it \
  --image=busybox:1.36 --target=gateway
```

`ENDPOINTS_FILE=/etc/gateway/endpoints.yaml` 告诉程序读取挂载文件。YAML 中的 `${MOCK_PROVIDER_API_KEY}` 会在程序启动时从 Secret 注入的环境变量展开。

修改 ConfigMap 后，文件会更新，但当前程序不会热加载文件配置，需要重启 Deployment：

```bash
kubectl rollout restart deployment/gateway -n gateway-lab
kubectl rollout status deployment/gateway -n gateway-lab
```

## 练习 4：区分 Secret 和 ConfigMap

```bash
kubectl get secret gateway-secrets -n gateway-lab -o yaml
kubectl get secret gateway-secrets -n gateway-lab \
  -o jsonpath='{.data.GATEWAY_API_KEYS}' | base64 --decode
echo
```

Secret 的值只是 base64 编码，不等于加密。仓库中的值仅供本地 kind 实验使用。生产环境应使用外部 Secret 管理方案或集群加密，并避免将真实凭据提交到 Git。

## 练习 5：存活与就绪探针

```bash
kubectl describe pod -n gateway-lab -l app=gateway
curl -i http://localhost:8080/healthz
curl -i http://localhost:8080/readyz
```

- `/healthz` 表示进程仍能处理 HTTP。
- `/readyz` 还会检查 Redis；失败时 Pod 保持运行，但会从 Service Endpoint 中摘除。

暂停 Redis 并观察：

```bash
kubectl scale deployment/redis -n gateway-lab --replicas=0
kubectl get pods,endpoints -n gateway-lab -w
```

恢复：

```bash
kubectl scale deployment/redis -n gateway-lab --replicas=1
kubectl rollout status deployment/redis -n gateway-lab
```

下一步参见 [Day 2](day2-ratelimit.md)，验证两个网关副本如何共享限流和余额状态。
