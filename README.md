# Cloud Gateway Lab

一个用于学习 Kubernetes、高并发网关和故障排查的两周实践项目。仓库里有两条可运行的线：

**K8s 练习网关**（`go run ./cmd/gateway`）反代 `users` / `products`，用来练副本、探针和 Redis 限流。

**AI API Gateway**（`go run ./cmd`）对外提供 OpenAI-compatible `POST /v1/chat/completions`，内部完成鉴权、限流、模型路由、熔断、故障转移和主动探活。

## AI API Gateway

```text
Client
  POST /v1/chat/completions
  Authorization: Bearer <API_KEY>
        |
        v
  Auth (sha256 key hash, Redis cache)
        |
  Rate limit + token pre-deduct (Redis ZSet + Lua)
        |
  Router: health + circuit breaker + weighted RR
        |
  OpenAI-compatible Adapter  -->  Endpoint A / B / C
        |
  Retry / Failover / Settle quota
```

本地需要 Redis。默认读 `config/endpoints.yaml`，把流量打到本机 Ollama（`http://localhost:11434/v1`）。

```bash
# 可选：docker run -p 6379:6379 redis:7-alpine
GATEWAY_API_KEYS=sk-alice:alice \
  go run ./cmd
```

```bash
curl http://localhost:8080/v1/chat/completions \
  -H "Authorization: Bearer sk-alice" \
  -H "Content-Type: application/json" \
  -d '{"model":"gpt-5","messages":[{"role":"user","content":"Hello"}]}'
```

未设置 `GATEWAY_API_KEYS` 时，任意 Bearer token 会被当成 user id（仅本地联调）。设置 `MYSQL_DSN` 后，API Key 和 Endpoint 改为从 MySQL 读，Redis 做空值缓存 / singleflight / TTL 抖动。表结构见 `deploy/mysql/schema.sql`。

`/metrics` 会输出请求数、限流、鉴权拒绝、token 用量，以及每个 endpoint 的健康状态和熔断状态。响应带 `X-Request-ID`。

## K8s 练习网关

- Go 标准库实现的 HTTP 反向代理网关
- `users`、`products` 两个模拟后端
- 健康检查、就绪检查、超时和连接池
- 滑动窗口限流（进程内 / Redis + Lua）
- 配额预扣（进程内 / Redis + Lua）
- 请求数、上游错误、限流拒绝、配额拒绝指标
- 两副本网关、Redis 账本和后端的 Kubernetes 部署
- ConfigMap、Service、资源限制和探针

## 请求链路

```text
client -> gateway Service -> gateway Pod
                              ├── 限流（滑动窗口）
                              ├── 预扣费（配额）
                              ├── /api/users/*    -> users Service -> users Pod
                              └── /api/products/* -> products Service -> products Pod
```

限流按「路由 + 身份」，配额按身份。身份优先使用 `X-User-ID`，否则用客户端 IP。

## 本地验证

分别打开三个终端：

```bash
SERVICE_NAME=users PORT=8081 go run ./cmd/backend
SERVICE_NAME=products PORT=8082 go run ./cmd/backend
go run ./cmd/gateway
```

默认是内存滑动窗口、100 次/秒、不扣配额。发送请求：

```bash
curl http://localhost:8080/api/users/42
curl http://localhost:8080/api/products/7
curl -H "X-User-ID: alice" http://localhost:8080/api/users/42
curl http://localhost:8080/metrics
```

本地改走 Redis（需本机 `redis-server` 或 `docker run -p 6379:6379 redis:7-alpine`）：

```bash
RATE_LIMIT_BACKEND=redis RATE_LIMIT_LIMIT=10 RATE_LIMIT_WINDOW=1s QUOTA_DEFAULT=25 \
  go run ./cmd/gateway
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

然后在另一个终端请求 `http://localhost:8080/api/users/42`。集群里两个网关副本共用 Redis：每用户每路由 10 次/秒，余额 25。

## 第一天练习

1. 执行 `kubectl get pods -n gateway-lab -o wide`，理解 Deployment、Pod 与副本的关系。
2. 连续访问 users 接口，观察响应中的 `pod` 是否变化。
3. 删除一个 users Pod，观察 Deployment 自动补齐副本。
4. 把 ConfigMap 中 users 地址改错，应用后重启网关，观察 502 和 `/metrics`。
5. 使用 `kubectl logs`、`kubectl describe pod` 和 Endpoints 定位问题。

对照笔记见 [docs/day1-k8s.md](docs/day1-k8s.md)。

## 第二天练习

1. 用同一 `X-User-ID` 连打 15 次，观察 429 和 `X-RateLimit-Remaining`。
2. 看两个 gateway Pod 的日志，确认请求落在不同副本，但 Redis 计数仍共享。
3. 把 `RATE_LIMIT_BACKEND` 改成 `memory`，对比多副本超卖。
4. 打超 25 次成功请求，观察 403 配额耗尽。
5. 删掉 redis Pod，观察 `/readyz` 变 503，恢复后自动接流。

对照笔记见 [docs/day2-ratelimit.md](docs/day2-ratelimit.md)。Lua 脚本在 `internal/ratelimit/sliding_window.lua` 和 `internal/quota/reserve.lua`。
