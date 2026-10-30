# headscale-exporter

一个用 Go 编写的 Headscale Prometheus exporter，基于 [`hibare/headscale-client-go`](https://github.com/hibare/headscale-client-go) 读取 Headscale REST API，导出节点、用户和子网路由指标，并附带 Grafana 仪表盘。

## 功能

- 节点状态表：节点名、别名、所属用户、IPv4/IPv6、在线状态、最后在线、注册时间、到期时间、注册方式、标签、出口节点。
- 在线用户数、注册用户总数、节点总数、在线节点数。
- 每个用户的在线/离线节点数。
- 子网路由表：网段、节点、用户、是否批准、是否宣告、是否出口路由。
- 节点级时间戳和路由计数，方便告警和 PromQL 聚合。
- 可直接导入的 Grafana 仪表盘，包含数值面板、每小时在线用户折线图、节点表格和子网路由表格。

## 要求

- Headscale **v0.28.0+**（`headscale-client-go` v0.10.0 的要求）。
- 构建 exporter 需要 **Go 1.27.3+**；运行编译后的二进制不需要 Go。
- Prometheus（抓取指标）和 Grafana（展示表格/折线图）。

## 数据来源与限制

Exporter 读取的是 Headscale 控制面 API：

- `GET /api/v1/node`：节点列表。
- `GET /api/v1/user`：注册用户列表。

因此可以展示 Headscale API 暴露的字段，但 **不能** 展示 `tailscale status` 里由客户端本地提供的信息：

| 数据 | 是否支持 | 说明 |
|---|---|---|
| 节点名、用户、IP、在线状态、最后在线、到期时间、注册方式、标签 | ✅ | Headscale API |
| 已批准/已宣告/子网路由、出口节点 | ✅ | 由 API 路由字段推导 |
| 流量 Rx/Tx、速率 | ❌ | Headscale 是控制面，不记录数据面流量；API 和自带 metrics 都没有字节计数 |
| 客户端 OS、Tailscale 版本 | ❌ | Headscale API 不返回这些字段 |
| 直连/DERP 路径、延迟、丢包 | ❌ | 属于数据面或客户端本地信息 |
| 每用户/每节点流量 | ❌ | 同上；替代方案见下文 |

统计口径：

- **在线用户数**：至少有一个在线节点的已注册用户去重数。Headscale 用 `TaggedDevices` 伪用户表示打了标签的设备，该伪用户不计入在线用户数和注册用户总数。
- **总注册用户数**：Users API 返回的用户数量。
- **每小时在线用户数**：exporter 暴露的是实时 gauge，Grafana 用 1 小时步长和 `max_over_time` 展示每小时峰值。也可以改成平均值，详见下文。
- `lastSeen` / `expiry` 为 `null` 时导出为 `0`，Grafana 中显示为“—”/“无”。
- 路由计数按“节点 + 网段”计；同一网段被多个节点宣告会算多条。

## 快速开始

### 1. 创建 Headscale API Key

在 Headscale 服务端执行：

```bash
headscale apikeys create --expiration 90d
```

保存输出中的 API Key（只显示一次）。该 Key **拥有 Headscale API 的完整权限**，请通过环境变量或 secret 注入，不要提交到 Git。

### 2. 直接运行二进制

```bash
go build -o bin/headscale-exporter .
HEADSCALE_URL=http://127.0.0.1:8080 \
HEADSCALE_API_KEY='your-api-key' \
./bin/headscale-exporter
```

PowerShell：

```powershell
go build -o bin/headscale-exporter.exe .
$env:HEADSCALE_URL = "http://127.0.0.1:8080"
$env:HEADSCALE_API_KEY = "your-api-key"
.\bin\headscale-exporter.exe
```

验证：

```bash
curl http://127.0.0.1:9110/metrics
curl http://127.0.0.1:9110/healthz
```

> `HEADSCALE_URL` 只填到端口，不要带 `/api/v1`，例如 `http://127.0.0.1:8080`。
>
> `/healthz` 只表示 exporter 进程存活；Headscale API 是否可用请看 `headscale_up` 指标。

### 3. Docker Compose

```bash
cp .env.example .env
# 编辑 .env，填入 HEADSCALE_URL 和 HEADSCALE_API_KEY
docker compose up -d --build
curl http://127.0.0.1:9110/metrics
```

- Headscale 跑在宿主机时，默认使用 `http://host.docker.internal:8080`（compose 已配置 `host-gateway`）。
- Headscale 在另一个 Docker Compose 网络时，把 exporter 服务接入该网络，并把 `HEADSCALE_URL` 改成 `http://<headscale-service>:8080`。
- Headscale 使用 HTTPS 时，直接填写 `https://...`；容器镜像包含 CA 证书。

### 4. Kubernetes

参考 [`examples/kubernetes.yaml`](examples/kubernetes.yaml)，包含 Namespace、Secret、Deployment、Service：

```bash
# 先修改 Secret 中的 HEADSCALE_URL / HEADSCALE_API_KEY（或用 kubectl create secret 创建后删除该块）
kubectl apply -f examples/kubernetes.yaml
kubectl -n headscale port-forward svc/headscale-exporter 9110:9110
curl http://127.0.0.1:9110/metrics
```

说明：

- `image` 默认是 `ghcr.io/TuF3i/headscale-exporter:latest` 占位值，需要先自行构建推送：`docker build -t <registry>/headscale-exporter:latest .`，再修改清单中的镜像地址。
- `HEADSCALE_URL` 使用集群内 DNS，例如 Headscale 部署在 `headscale` 命名空间时填 `http://headscale.headscale.svc.cluster.local:8080`。
- 清单启用了 `prometheus.io/scrape` 注解，供 vanilla Prometheus 的 Kubernetes 服务发现使用；如果使用 prometheus-operator，可按下面模板创建 ServiceMonitor：

```yaml
apiVersion: monitoring.coreos.com/v1
kind: ServiceMonitor
metadata:
  name: headscale-exporter
  namespace: headscale
  labels:
    release: <prometheus-operator-release>  # 匹配你的 operator selector
spec:
  selector:
    matchLabels:
      app.kubernetes.io/name: headscale-exporter
  endpoints:
    - port: http-metrics
      interval: 30s
      scrapeTimeout: 15s
```

## 配置

命令行 flag 的优先级高于环境变量。

| Flag | 环境变量 | 默认值 | 说明 |
|---|---|---|---|
| `--headscale.url` | `HEADSCALE_URL` | 必填 | Headscale 基础 URL，不含 `/api/v1` |
| `--headscale.api-key` | `HEADSCALE_API_KEY` | 必填 | Headscale API Key |
| `--web.listen-address` | `LISTEN_ADDRESS` | `:9110` | exporter 监听地址 |
| `--web.telemetry-path` | `TELEMETRY_PATH` | `/metrics` | 指标路径 |
| `--scrape.timeout` | `SCRAPE_TIMEOUT` | `10s` | 单次抓取 Headscale API 的超时，最大 5m |
| `--log.level` | `LOG_LEVEL` | `info` | `debug` / `info` / `warn` / `error` |
| `--version` | — | — | 打印版本后退出 |

查看帮助：

```bash
./bin/headscale-exporter --help
```

## 指标

| 指标 | 类型 | 标签 | 说明 |
|---|---|---|---|
| `headscale_up` | gauge | — | 最近一次 Headscale API 抓取是否全部成功，1/0 |
| `headscale_scrape_duration_seconds` | gauge | — | 本次抓取耗时 |
| `headscale_scrape_errors_total` | counter | — | 累计抓取失败次数 |
| `headscale_users` | gauge | — | 注册用户总数 |
| `headscale_users_online` | gauge | — | 在线注册用户数（按用户去重） |
| `headscale_user_nodes` | gauge | `user`, `status=online/offline` | 每个用户的在线/离线节点数；包含 `TaggedDevices` |
| `headscale_nodes` | gauge | — | 节点总数 |
| `headscale_nodes_online` | gauge | — | 在线节点数 |
| `headscale_node_info` | gauge(info) | `id`, `node`, `given_name`, `user`, `ipv4`, `ipv6`, `register_method`, `tags`, `exit_node` | 节点静态信息，值恒为 1 |
| `headscale_node_online` | gauge | `id`, `node`, `user` | 节点在线状态 0/1 |
| `headscale_node_last_seen_timestamp_seconds` | gauge | `id`, `node`, `user` | 最后在线时间；0 表示未知 |
| `headscale_node_created_timestamp_seconds` | gauge | `id`, `node`, `user` | 节点注册时间 |
| `headscale_node_expiry_timestamp_seconds` | gauge | `id`, `node`, `user` | 节点 key 到期时间；0 表示无 |
| `headscale_node_approved_routes` | gauge | `id`, `node`, `user` | 已批准路由数量 |
| `headscale_node_available_routes` | gauge | `id`, `node`, `user` | 节点宣告的路由数量 |
| `headscale_node_subnet_routes` | gauge | `id`, `node`, `user` | 子网路由数量（Headscale 列表接口含 exit 路由） |
| `headscale_node_is_exit_node` | gauge | `id`, `node`, `user` | 是否出口节点 0/1 |
| `headscale_routes` | gauge | `status=approved/available` | 路由条目总数（按节点+网段计） |
| `headscale_route_info` | gauge(info) | `id`, `node`, `user`, `route`, `approved`, `available`, `exit_route` | 子网路由明细，值恒为 1 |
| `headscale_exporter_build_info` | gauge(info) | `version`, `commit`, `go_version` | exporter 构建信息，值恒为 1 |

说明：

- `headscale_up` 表示 exporter 能否成功读取 Headscale API；它和 Prometheus 自动生成的 `up`（exporter 进程是否可抓取）不是一回事。
- `headscale_users` 是 gauge（不是 counter），不使用 `_total` 后缀。
- `headscale_node_info` 和 `headscale_route_info` 是 info 型指标，值恒为 1，实际信息在标签里。
- 所有 `*_timestamp_seconds` 都是 Unix 秒。

## Prometheus 配置

参考 [`examples/prometheus.yml`](examples/prometheus.yml)：

```yaml
scrape_configs:
  - job_name: headscale-exporter
    scrape_interval: 30s
    scrape_timeout: 15s
    static_configs:
      - targets:
          - 127.0.0.1:9110
```

> 建议 Prometheus 的 `scrape_timeout` 大于 exporter 的 `SCRAPE_TIMEOUT`（默认 10s）。这里用 15s，避免 Headscale API 变慢时 Prometheus 先超时，导致看不到 `headscale_up 0`。

如果 Prometheus 和 exporter 不在同一台机器，把 target 改成 exporter 的可达地址。

## Grafana 仪表盘

1. Grafana → Dashboards → New → Import。
2. 上传 [`grafana/headscale-dashboard.json`](grafana/headscale-dashboard.json)。
3. 选择你的 Prometheus 数据源。

仪表盘包含：

- **概览数值**：在线用户数、注册用户总数、在线设备数、设备总数、已批准子网路由数、Exporter 状态。
- **每小时在线用户数**：折线图，PromQL 为 `max_over_time(headscale_users_online[1h])`，最小步长 1h，展示每小时峰值。
- **Tailscale 状态**：节点表格，由 `headscale_node_info` 与在线状态、最后在线、注册时间、到期时间指标按 `id` 合并。
- **子网路由**：路由表格，来自 `headscale_route_info`。

节点表格的时间列在 PromQL 中把 Unix 秒乘以 1000，以适配 Grafana 日期单位要求的毫秒值。

如果想看每小时平均在线用户数，把折线图查询改成：

```promql
avg_over_time(headscale_users_online[1h])
```

如果需要长期保留小时级历史（超过 Prometheus 原始样本保留期），启用 [`examples/headscale.rules.yml`](examples/headscale.rules.yml) 中的 recording rule：

```promql
headscale:users_online:hourly_max
headscale:users_online:hourly_avg
```

## 流量采集替代方案

Headscale 不转发普通 tailnet 流量（除非使用 DERP 中继），也不记录每个节点的字节数，所以 exporter 无法提供流量指标。

如果需要流量数据，推荐在每台 Tailscale 节点上部署 `node_exporter`，然后查询 `tailscale0` 网卡：

```promql
# 接收速率（bytes/s）
rate(node_network_receive_bytes_total{device="tailscale0"}[5m])

# 发送速率（bytes/s）
rate(node_network_transmit_bytes_total{device="tailscale0"}[5m])

# 最近 1 小时接收量
increase(node_network_receive_bytes_total{device="tailscale0"}[1h])
```

`node_exporter` 的 `netdev` collector 默认启用，会采集 `tailscale0`。可以用 relabeling 把 `instance` 映射成节点名。

注意：

- 这是该节点 `tailscale0` 网卡的总流量，不是按 peer/用户拆分，也不包含 DERP 中继流量。
- `tailscale status --json` 的 `PeerStatus.RxBytes` / `TxBytes` 是 **该客户端视角** 的每 peer 统计，需要 exporter 与 `tailscaled` 同机，未包含在本项目中。
- 出口节点转发的流量同样只能在出口节点上通过网卡计数观察。

## 安全建议

- Headscale API Key 是全权限密钥；使用环境变量、Docker secret 或 systemd `EnvironmentFile` 注入，不要写入镜像或提交到仓库。
- 建议定期轮换 Key，并设置过期时间。
- exporter 的 `/metrics` 没有认证，只监听内网地址或用防火墙限制来源。
- 跨网络访问 Headscale 时使用 HTTPS，并确保证书可信。
- Docker 镜像使用 distroless nonroot 运行，exporter 本身只执行读操作。

## 开发

```bash
make test      # go test ./...
make vet       # go vet ./...
make build     # 生成 bin/headscale-exporter
make docker    # 构建镜像
```

目录结构：

```
main.go                          # 启动、HTTP server、优雅退出
internal/config/                 # flag/env 配置解析与校验
internal/collector/              # Prometheus collector 与单元测试
grafana/headscale-dashboard.json # Grafana 仪表盘
examples/                        # Prometheus 配置、recording rules、Kubernetes 清单
```

测试使用 `httptest` 模拟 Headscale API，覆盖在线/离线、TaggedDevices、空时间戳、exit 节点、路由并集和 API 失败场景。

## License

MIT
