# Cloud Gateway Lab

一个用于学习 Kubernetes、高并发网关和故障排查的两周实践项目。当前第一阶段包含：

- Go 标准库实现的 HTTP 反向代理网关
- `users`、`products` 两个模拟后端
- 健康检查、就绪检查、超时和连接池
- 请求数与上游错误数指标
- 两副本网关和后端的 Kubernetes 部署
- ConfigMap、Service、资源限制和探针

## 请求链路

```text
client -> gateway Service -> gateway Pod
                              ├── /api/users/*    -> users Service -> users Pod
                              └── /api/products/* -> products Service -> products Pod
```

## 本地验证

分别打开三个终端：

```bash
SERVICE_NAME=users PORT=8081 go run ./cmd/backend
SERVICE_NAME=products PORT=8082 go run ./cmd/backend
go run ./cmd/gateway
```

发送请求：

```bash
curl http://localhost:8080/api/users/42
curl http://localhost:8080/api/products/7
curl http://localhost:8080/metrics
```

运行测试：

```bash
make test
```

## 部署到 kind

当前机器需要 Docker、kubectl 和 kind。如果尚未安装 kind：

```bash
brew install kind
```

创建集群并部署：

```bash
make cluster
make images
make load
make deploy
make status
```

转发网关端口：

```bash
make port-forward
```

然后在另一个终端请求 `http://localhost:8080/api/users/42`。

## 第一天练习

1. 执行 `kubectl get pods -n gateway-lab -o wide`，理解 Deployment、Pod 与副本的关系。
2. 连续访问 users 接口，观察响应中的 `pod` 是否变化。
3. 删除一个 users Pod，观察 Deployment 自动补齐副本。
4. 把 ConfigMap 中 users 地址改错，应用后重启网关，观察 502 和 `/metrics`。
5. 使用 `kubectl logs`、`kubectl describe pod` 和 Endpoints 定位问题。

建议记录每次实验的现象、诊断命令、根因和修复方式。后续阶段会加入限流、HPA、Prometheus、k6 压测及故障演练。
