# Cloud Gateway Lab

![Go](https://img.shields.io/badge/Go-1.26-00ADD8?logo=go&logoColor=white)
![Kubernetes](https://img.shields.io/badge/Kubernetes-kind-326CE5?logo=kubernetes&logoColor=white)
![Redis](https://img.shields.io/badge/Redis-Lua-DC382D?logo=redis&logoColor=white)

一个可部署到 Kubernetes 的 AI API Gateway 实验项目，覆盖多模型统一接入、Redis 原子限流与 token 配额、健康检查、熔断、故障转移，以及 **Kubernetes Scheduler / GPU 资源模型** 学习实验。

项目只有一套核心网关：本地开发时可以连接 Ollama 或真实模型服务；部署到 kind 时连接内置的 OpenAI-compatible mock model server，在不使用外部 API Key 的情况下复现实验。

普通 Kubernetes 实验不需要 GPU。GPU 模块是独立、可选的；没有 NVIDIA 设备时可以用 `example.com/mock-gpu` 学习 extended resource 调度，它**不等于**真实 `nvidia.com/gpu`。

> [!NOTE]
> 本项目面向学习与实验场景，并非生产就绪的网关发行版。

## Features

- OpenAI-compatible `POST /v1/chat/completions`，支持普通响应与 SSE 流式响应
- Bearer API Key 鉴权，支持内存配置或 MySQL 持久化
- Redis ZSet + Lua 实现滑动窗口限流与 token 配额预扣
- 按健康状态、熔断状态和权重选择上游 endpoint
- 请求重试、跨 endpoint 故障转移和主动健康检查
- OpenAI、Azure OpenAI、Anthropic provider adapter
- Anthropic Messages API 与 OpenAI Chat/SSE 格式转换
- 请求 ID、Prometheus 文本指标和结构化日志
- Kubernetes 双副本 Deployment、Service / EndpointSlice / CoreDNS、ConfigMap、Secret、探针
- DaemonSet Node Agent、HPA、Scheduler 与故障实验
- 可选 GPU 实验：真实 `nvidia.com/gpu` 或非欺骗性的 `example.com/mock-gpu` 模拟
- mock model server + Model Router（按 model / 健康 / 权重选择实例）

## Architecture

```text
Client
  POST /v1/chat/completions
        │
        ▼
  AI Gateway Service
        │
        ▼
  Gateway Pod                 ← Kubernetes Scheduler: Pod → Node
        ├── Authentication
        ├── Redis rate limit + token reservation
        ├── Model Router      ← Request → Model Instance
        └── Retry / failover
                 │
                 ├── Service/model
                 ├── Service/mock-a
                 ├── Service/mock-b
                 └── Real OpenAI / Azure / Anthropic / Ollama
```

两种“调度”不要混：

```text
Kubernetes Scheduler          Model Router
Pod → Node                    Request → Model Instance
```

HPA 是第三件事：只改变 Gateway **副本数量**。

Provider adapter 处理 URL、鉴权和协议差异，路由层只依赖统一接口。Endpoint 与模型映射通过 [`config/endpoints.yaml`](config/endpoints.yaml) 配置。集群内实验清单在 [`k8s/`](k8s/README.md)。

## Local Quick Start

### Prerequisites

- Go 1.26+
- Redis 7+
- 一个兼容的模型服务；默认配置使用本机 [Ollama](https://ollama.com/)

启动 Redis：

```bash
docker run --rm --name gateway-redis -p 6379:6379 redis:7-alpine
```

默认 endpoint 指向 `http://localhost:11434/v1`，并把客户端模型 `gpt-5` 映射到 Ollama 的 `llama3.2`。确保对应模型已经运行，或修改 [`config/endpoints.yaml`](config/endpoints.yaml)。

启动网关：

```bash
GATEWAY_API_KEYS=sk-alice:alice go run ./cmd
```

发送请求：

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
curl http://localhost:8080/model-status
curl http://localhost:8080/node-status
```

未设置 `GATEWAY_API_KEYS` 时，服务会把任意 Bearer token 作为 user ID 接受。该模式仅用于本地联调。

## Kubernetes Quick Start

kind 环境不依赖 Ollama、外部模型服务或 NVIDIA GPU。默认集群是 1 个 control-plane + 2 个 worker，运行两个 Gateway Pod、Redis、`model` / `mock-a` / `mock-b`，以及每个 Node 上的 node-agent DaemonSet。

确保本机已安装 Docker、`kubectl` 和 [kind](https://kind.sigs.k8s.io/)：

```bash
make k8s-up
make k8s-deploy
make k8s-status
```

兼容旧入口：`make cluster` / `make images` / `make load` / `make deploy`。

转发 Service：

```bash
make port-forward
```

使用实验 Secret 中的 API Key 发起请求：

```bash
curl http://localhost:8080/v1/chat/completions \
  -H "Authorization: Bearer sk-lab" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "gpt-5",
    "messages": [{"role": "user", "content": "Hello from kind"}]
  }'
```

响应内容会显示请求最终由 `mock-a` 或 `mock-b` 处理。流式请求只需在 JSON 中增加 `"stream": true`。

也可以请求 `model=mock-model`，走独立的 Model Service：

```bash
curl http://localhost:8080/v1/chat/completions \
  -H "Authorization: Bearer sk-lab" \
  -H "Content-Type: application/json" \
  -d '{"model":"mock-model","messages":[{"role":"user","content":"Hello from kind"}]}'
```

实验清单在 [`k8s/`](k8s/README.md)。Secret 只包含本地实验凭据。

集群内 DNS（**不是**宿主机 DNS）：

```text
model
  ↓
model.gateway-lab.svc.cluster.local
  ↓
Service ClusterIP
  ↓
EndpointSlice
  ↓
Model Pod
```

删除集群：

```bash
make k8s-delete
```

### Scheduler / GPU / 故障实验

```bash
make scheduling-demo
make scheduling-taint
make gpu-check
make gpu-deploy
make fault-pending
make fault-image
make fault-crash
make fault-readiness
make fault-gpu
make load-test
```

`make gpu-check` 在没有 GPU 时只打印依赖，不会让整个项目失败。

HPA（需要 metrics-server，可选）：

```bash
make hpa-setup
make hpa-apply
```

## Configuration

常用环境变量：

- `PORT`：监听端口，默认 `8080`
- `REDIS_ADDR`：Redis 地址，默认 `127.0.0.1:6379`
- `REDIS_STARTUP_TIMEOUT`：启动时等待 Redis 的最长时间，默认 `15s`
- `GATEWAY_API_KEYS`：`<api-key>:<user-id>` 列表
- `ENDPOINTS_FILE`：endpoint 配置文件，默认 `config/endpoints.yaml`
- `UPSTREAM_API_KEY`：默认上游 API Key，可在 YAML 中使用 `${UPSTREAM_API_KEY}`
- `RATE_LIMIT` / `RATE_LIMIT_WINDOW`：限流次数与窗口
- `DEFAULT_BALANCE`：用户初始 token 余额
- `COMPLETION_RESERVE`：每次请求预留的 completion token
- `RETRY_MAX_ATTEMPTS` / `RETRY_BASE_DELAY`：重试次数与退避时间
- `BREAKER_THRESHOLD` / `BREAKER_COOLDOWN`：熔断阈值与冷却时间
- `HEALTH_INTERVAL` / `HEALTH_TIMEOUT`：主动探活间隔与超时
- `MYSQL_DSN`：启用 MySQL API Key 与 endpoint 存储

设置 `MYSQL_DSN` 后，API Key 和 endpoint 从 MySQL 读取，Redis 用于缓存。初始化表结构见 [`deploy/mysql/schema.sql`](deploy/mysql/schema.sql)。

Mock provider 支持 `PROVIDER_NAME`、`MOCK_API_KEY`、`FAIL_STATUS` 和 `DELAY_MS`，用于构造身份、鉴权、失败和慢响应。

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
│   └── mockprovider/       # OpenAI-compatible 模拟模型服务
├── config/                 # 本地模型与 endpoint 配置
├── k8s/                    # 学习实验清单（Gateway / Model / Scheduler / GPU / 故障）
├── deploy/
│   ├── kind/               # 多 Node kind 配置
│   ├── k8s/                # kustomize 入口（指向 ../../k8s）
│   └── mysql/              # MySQL schema
├── scripts/                # make 目标背后的实验脚本
├── internal/
│   ├── aigateway/          # AI 请求处理与 Model Router
│   ├── provider/           # Provider adapters
│   ├── endpoint/           # Endpoint 池与路由
│   ├── breaker/            # 熔断器
│   └── health/             # 主动健康检查
├── pkg/
│   ├── limiter/            # Redis 限流与配额
│   ├── prefixcache/        # Prompt 前缀索引
│   ├── proxy/              # SSE 代理组件
│   └── scheduler/          # 请求优先级排队（不是 kube-scheduler）
└── docs/                   # 实验说明
```

## Learning Guides

- [Kubernetes 知识图谱与实验总览](k8s/README.md)
- [Day 1：部署 AI Gateway 到 Kubernetes](docs/day1-k8s.md)
- [Day 2：多副本 Redis 限流与 token 配额](docs/day2-ratelimit.md)
- [Day 3：上游健康检查、熔断与故障转移](docs/day3-failover.md)
- [Day 4：Scheduler、Label、Taint](docs/day4-scheduling.md)
- [Day 5：GPU 资源模型](docs/day5-gpu.md)

## SONiC → Kubernetes Mapping

如果你从 SONiC / 交换机平台过来，下面是对照，不是“把 SONiC 标签贴进集群就加入了 Kubernetes”。

```text
SONiC switch
    ↓
enable / join Kubernetes          ← 交换机先成为 Worker Node
    ↓
Kubernetes Worker Node
    ↓
Node Label                        ← 只做分类，不是入集群机制
    ↓
DaemonSet selector
    ↓
Feature Container Pod
```

| SONiC 直觉 | Kubernetes |
| --- | --- |
| DUT / 交换机盒子 | Worker Node |
| Feature Container | Pod 里的一个 Container |
| 每台交换机跑一份 agent | DaemonSet |
| 角色标签（spine / leaf / gpu） | Node Label + nodeSelector / affinity |
| 专用设备不允许普通业务 | Taint + Toleration |
| ASIC / 端口资源 | Extended resource（类比 `nvidia.com/gpu`） |

Kubernetes 管理的是 **Pod**，不是直接管理某个 Container。SONiC Feature Container 可以作为 Pod 中的 Container 运行。

Node Label **不是** 把 SONiC 加入 Kubernetes Cluster 的机制。正确顺序是：交换机先加入集群成为 Node，再打 Label，再被 DaemonSet 选中。

## Contributing

提交代码前请确保 `make test` 通过，并为行为变更补充相应测试。
