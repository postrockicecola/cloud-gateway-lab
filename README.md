# Cloud Gateway Lab

![Go](https://img.shields.io/badge/Go-1.26-00ADD8?logo=go&logoColor=white)
![Kubernetes](https://img.shields.io/badge/Kubernetes-kind-326CE5?logo=kubernetes&logoColor=white)
![Redis](https://img.shields.io/badge/Redis-Lua-DC382D?logo=redis&logoColor=white)

一个使用 Go 构建的网关实验项目，覆盖 AI API 统一接入、Redis 原子限流、配额结算、故障转移，以及 Kubernetes 多副本部署与排障。

项目包含两个可独立运行的网关：

- **AI API Gateway**（`go run ./cmd`）：提供 OpenAI-compatible API，统一接入 OpenAI、Azure OpenAI、Anthropic 或本地 Ollama。
- **Kubernetes Lab Gateway**（`go run ./cmd/gateway`）：反向代理 `users` 和 `products` 服务，用于练习探针、限流、配额、多副本与故障排查。

> [!NOTE]
> 本项目面向学习与实验场景，并非生产就绪的网关发行版。

## Features

### AI API Gateway

- OpenAI-compatible `POST /v1/chat/completions`，支持普通响应与 SSE 流式响应
- Bearer API Key 鉴权，支持内存配置或 MySQL 持久化
- Redis ZSet + Lua 实现滑动窗口限流与 token 配额预扣
- 按健康状态、熔断状态和权重选择上游 endpoint
- 请求重试、跨 endpoint 故障转移和主动健康检查
- OpenAI、Azure OpenAI、Anthropic 三种 provider adapter
- Anthropic Messages API 与 OpenAI Chat/SSE 格式转换
- 请求 ID、Prometheus 文本格式指标和结构化日志
- Prompt 前缀匹配与 token 用量结算

### Kubernetes Lab Gateway

- 基于 Go 标准库的 HTTP 反向代理
- 进程内或 Redis Lua 滑动窗口限流
- 进程内或 Redis Lua 配额预扣
- 健康检查、就绪检查、超时与连接池
- 两副本网关、Redis 和模拟后端的 Kubernetes manifests
- ConfigMap、Service、资源限制及存活/就绪探针

## Architecture

```text
Client
  POST /v1/chat/completions
  Authorization: Bearer <API_KEY>
        │
        ▼
  Authentication
  (SHA-256 key hash + Redis cache)
        │
        ▼
  Rate limit + token reservation
  (Redis ZSet + Lua)
        │
        ▼
  Endpoint router
  (health + circuit breaker + weighted round-robin)
        │
        ├── OpenAI-compatible API
        ├── Azure OpenAI
        └── Anthropic Messages API
        │
        ▼
  Retry / failover / quota settlement
```

Provider adapter 负责处理 URL、鉴权和协议差异，路由层只依赖统一接口。Endpoint 与模型映射通过 [`config/endpoints.yaml`](config/endpoints.yaml) 配置。

## Quick Start

### Prerequisites

- Go 1.26+
- Redis 7+
- 一个兼容的模型服务；默认配置使用本机 [Ollama](https://ollama.com/)

### 1. 启动依赖

```bash
docker run --rm --name gateway-redis -p 6379:6379 redis:7-alpine
```

默认 endpoint 指向 `http://localhost:11434/v1`，并把客户端请求中的 `gpt-5` 映射到 Ollama 的 `llama3.2`。请先确保对应模型已经运行，或修改 [`config/endpoints.yaml`](config/endpoints.yaml) 接入其他 provider。

### 2. 启动 AI API Gateway

```bash
GATEWAY_API_KEYS=sk-alice:alice go run ./cmd
```

`GATEWAY_API_KEYS` 的格式为 `<api-key>:<user-id>`，多个用户之间使用逗号分隔。

### 3. 发送请求

```bash
curl http://localhost:8080/v1/chat/completions \
  -H "Authorization: Bearer sk-alice" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "gpt-5",
    "messages": [{"role": "user", "content": "Hello"}]
  }'
```

服务状态和指标：

```bash
curl http://localhost:8080/healthz
curl http://localhost:8080/readyz
curl http://localhost:8080/metrics
```

未设置 `GATEWAY_API_KEYS` 时，服务会把任意 Bearer token 作为 user ID 接受。该模式仅用于本地联调。

## Configuration

AI API Gateway 支持以下常用环境变量：

- `PORT`：监听端口，默认 `8080`
- `REDIS_ADDR`：Redis 地址，默认 `127.0.0.1:6379`
- `GATEWAY_API_KEYS`：本地 API Key 列表
- `ENDPOINTS_FILE`：endpoint 配置文件，默认 `config/endpoints.yaml`
- `UPSTREAM_API_KEY`：默认上游 API Key，可在 YAML 中使用 `${UPSTREAM_API_KEY}`
- `RATE_LIMIT` / `RATE_LIMIT_WINDOW`：限流次数与窗口，默认 `20` / `1s`
- `DEFAULT_BALANCE`：用户初始 token 余额，默认 `100000`
- `COMPLETION_RESERVE`：每次请求预留的 completion token，默认 `256`
- `RETRY_MAX_ATTEMPTS` / `RETRY_BASE_DELAY`：重试次数与基础退避时间
- `BREAKER_THRESHOLD` / `BREAKER_COOLDOWN`：熔断阈值与冷却时间
- `HEALTH_INTERVAL` / `HEALTH_TIMEOUT`：主动探活间隔与超时
- `MYSQL_DSN`：启用 MySQL API Key 与 endpoint 存储

设置 `MYSQL_DSN` 后，API Key 和 endpoint 从 MySQL 读取，Redis 用于缓存。初始化表结构见 [`deploy/mysql/schema.sql`](deploy/mysql/schema.sql)。

## Kubernetes Lab

### 本地运行

分别在三个终端启动两个模拟后端和网关：

```bash
SERVICE_NAME=users PORT=8081 go run ./cmd/backend
SERVICE_NAME=products PORT=8082 go run ./cmd/backend
go run ./cmd/gateway
```

默认使用进程内滑动窗口，每秒允许 100 次请求，不启用配额。可以通过以下请求验证：

```bash
curl http://localhost:8080/api/users/42
curl http://localhost:8080/api/products/7
curl -H "X-User-ID: alice" http://localhost:8080/api/users/42
curl http://localhost:8080/metrics
```

切换到 Redis 限流和配额：

```bash
RATE_LIMIT_BACKEND=redis \
RATE_LIMIT_LIMIT=10 \
RATE_LIMIT_WINDOW=1s \
QUOTA_DEFAULT=25 \
go run ./cmd/gateway
```

限流维度是「路由 + 身份」，配额维度是身份。身份优先取自 `X-User-ID`，未提供时使用客户端 IP。

### 部署到 kind

确保本机已安装 Docker、`kubectl` 和 [kind](https://kind.sigs.k8s.io/)：

```bash
make cluster
make images
make load
make deploy
make status
```

将网关转发到本机：

```bash
make port-forward
```

随后访问 `http://localhost:8080/api/users/42`。集群中的两个网关副本共享 Redis 限流和配额状态。

实验结束后删除集群：

```bash
make clean
```

## Testing

运行单元测试、静态检查和 Kubernetes manifest 校验：

```bash
make test
```

也可以只运行 Go 测试：

```bash
go test ./...
```

## Project Structure

```text
.
├── cmd/
│   ├── main.go             # AI API Gateway
│   ├── gateway/            # Kubernetes Lab Gateway
│   └── backend/            # 模拟后端
├── config/                 # 模型与 endpoint 配置
├── deploy/
│   ├── k8s/                # Kubernetes manifests
│   └── mysql/              # MySQL schema
├── internal/
│   ├── aigateway/          # AI 请求处理链路
│   ├── provider/           # Provider adapters
│   ├── endpoint/           # Endpoint 池与路由
│   ├── breaker/            # 熔断器
│   ├── health/             # 主动健康检查
│   └── gateway/            # Kubernetes Lab 反向代理
├── pkg/
│   ├── limiter/            # Redis 限流与配额
│   ├── prefixcache/        # Prompt 前缀索引
│   ├── proxy/              # SSE 代理组件
│   └── scheduler/          # 优先级调度组件
└── docs/                   # 实验说明
```

## Learning Guides

- [Day 1：Kubernetes 网关部署与故障排查](docs/day1-k8s.md)
- [Day 2：Redis 分布式限流与配额](docs/day2-ratelimit.md)

## Contributing

欢迎提交 Issue 或 Pull Request。提交代码前请确保 `make test` 通过，并为行为变更补充相应测试。
