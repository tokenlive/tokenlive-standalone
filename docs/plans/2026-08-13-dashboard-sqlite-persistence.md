# 单机看板 SQLite 持久化实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use `subagent-driven-development` (recommended) or `executing-plans` to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 为双进程与 all-in-one 单机部署实现重启安全、无外部依赖的首页运维看板，并补齐采集正确性、幂等上报、分钟/日聚合、健康状态、时区、重置和发布门禁。

**Architecture:** Gateway 在最终请求结果冻结后生成不可变 `RequestOutcome`，按 UTC 分钟和四类 scope 在内存预聚合，再用版本化 HTTP 协议可靠上报。Admin 独占 SQLite，在同一事务中完成批次去重、generation 校验和聚合 UPSERT，并通过统一 Dashboard Service 向 REST/WebSocket 提供整源查询。standalone 只负责绝对数据路径、嵌入组装、版本固定和重启 E2E。

**Tech Stack:** Go 1.25、Gin、GORM、glebarez/SQLite、Viper、Wire、Vue、ECharts、Vitest、Testify、GitHub Actions、Shell。

## Global Constraints

- 默认单机数据库固定为 `<data-dir>/tokenlive.db`，不能依赖进程当前工作目录。
- 分钟桶统一为 UTC，默认保留 30 天；日桶按固定业务时区生成，默认保留 1095 天。
- scope 只允许 `global`、`model`、`provider`、`endpoint`，不生成维度组合。
- 不保存 prompt、response、用户/API Key、请求 ID、错误文本或请求样本。
- Gateway 不 import Admin；Admin 不 import Gateway；standalone 不拥有 Dashboard 业务逻辑。
- 显式启用且健康的 Prometheus 与 SQLite 按整套数据源切换，不能按字段拼接。
- 熔断当前状态继续使用内存心跳，历史熔断继续使用 `event_log`。
- 指标采集和持久化失败不能阻塞模型代理请求。
- Gateway 队列有界；默认 5 秒 flush；关闭时 drain；崩溃允许损失未成功上报的内存批次。
- 批次幂等键固定为 `instance_id + instance_epoch + sequence`；Dashboard 历史世代单独使用数值 `generation`。
- 事件时间默认只接受过去 24 小时至未来 5 分钟。
- SQLite 使用现有 Admin GORM 连接池；启用 WAL、busy timeout、foreign keys 和短事务。
- 首期支持边界为约 100 RPS、数百个活跃资源、单 SQLite 文件。
- 不解析旧日志回填；新版本记录 `collection_started_at`。
- 不执行 git commit、tag、push 或发布；需要发布版本时停在人工授权点。
- Gateway 仓已有未跟踪 `docs/superpowers/`，不得删除、覆盖或误加入。

---

## 文件结构

### tokenlive-gateway

- `pkg/core/outcome.go`：不可变最终结果和 attempt 快照。
- `pkg/core/outcome_status.go`：最终 HTTP 状态和错误类型分类。
- `pkg/core/context.go`：first byte、first token、response started、final status/outcome 生命周期。
- `pkg/core/engine.go`：completion 校验、最终分类、普通 outbound 与 outcome consumer 两阶段执行。
- `pkg/core/filter.go`：`OutcomeConsumerFilter` marker。
- `pkg/llm/sse_intercept_writer.go`：严格 TTFT 和 first-byte 语义。
- `pkg/statusreport/config.go`：reporting 配置和 transport 选择。
- `pkg/statusreport/dto.go`：兼容旧请求 DTO、聚合 DTO、ack 和 generation handshake DTO。
- `pkg/statusreport/histogram.go`：固定版本直方图。
- `pkg/statusreport/aggregate.go`：四类分钟 scope 预聚合。
- `pkg/statusreport/health.go`：发送健康快照和 gap 状态。
- `pkg/statusreport/http_publisher.go`：HTTP 请求、严格 ack 和 TLS client。
- `pkg/statusreport/retry.go`：有界指数退避。
- `pkg/statusreport/batcher.go`：队列、flush、heartbeat、sequence 和 shutdown drain。
- `pkg/filters/outbound/status_collector.go`：只把 `RequestOutcome` 交给 reporter；保留 Redis 兼容路径。
- `internal/bootstrap/engine.go`：构造并拥有 reporter，Gateway cleanup 时先关闭 reporter。
- `config/*.yml`、`.env.example`：新增 `status_reporting` 默认配置。

### tokenlive-admin

- `adminapp/app.go`、`internal/bootstrap/bootstrap.go`：类型化数据库 runtime override。
- `pkg/gormx/gorm.go`：SQLite 路径、WAL、busy timeout 和连接数。
- `internal/config/config.go`：`DashboardMetricsConfig`。
- `pkg/metrics/protocol.go`：Admin 侧版本化协议 DTO，不依赖 Gateway。
- `pkg/metrics/recorder.go`：Resource 模块消费的窄写入接口。
- `internal/mods/dashboard/schema/*.go`：minute/day/batch/state/gap schema。
- `internal/mods/dashboard/dal/*.go`：事务写入、查询、封账、清理和重置。
- `internal/mods/dashboard/biz/histogram.go`：直方图合并和分位数。
- `internal/mods/dashboard/biz/ingest.go`：legacy 与 v2 ingestion、幂等和 generation。
- `internal/mods/dashboard/biz/query.go`：overview/trends/ranking 的 SQLite 查询模型。
- `internal/mods/dashboard/biz/maintenance.go`：日封账、retention 和旧 generation 清理。
- `internal/mods/dashboard/biz/settings.go`：健康、时区和重置。
- `internal/mods/dashboard/api/dashboard.api.go`、`ws.go`：统一 Service 和整源切换。
- `internal/mods/dashboard/api/settings.go`：状态、时区和重置接口。
- `internal/mods/resource/schema/gateway_metric.go`：命名上报表单和校验。
- `internal/mods/resource/api/gateway_sync.go`：持久化后更新内存并返回严格 ack。
- `internal/mods/dashboard/main.go`、`wire.go`、`internal/wirex/wire_gen.go`：迁移和依赖注入。
- `scripts/init.sql`：完整表结构。
- `frontend/src/apis/modules/dashboard.js`：状态、时区和重置 API。
- `frontend/src/views/home/index.vue`：健康提示、数据源、更新时间和趋势缺口。
- `frontend/src/views/system/dashboard-settings.vue`：时区和重置交互；若现有设置路由更合适，则以现有路由文件名创建同职责页面。

### tokenlive-standalone

- `cmd/tokenlive/main.go`：`resolveDataDir` 和绝对 DB DSN。
- `internal/assemble/assemble.go`：`AdminDBDSN`、Gateway-before-Admin shutdown、并发安全 Close。
- `config/*.yml`、`configs/admin/conf/server.toml`：单机默认指标配置。
- `test/e2e/sqlite_dashboard_test.go`：直接 ingestion 重启恢复。
- `test/e2e/gateway_dashboard_test.go`：真实 Gateway 请求到 SQLite 的完整链路。
- `scripts/package-release.sh`：本地依赖显式 opt-in。
- `scripts/verify-release-package.sh`、`scripts/package-release_test.sh`：依赖 provenance 和包结构验证。
- `.github/workflows/release-brew.yml`：测试、E2E 和包门禁。

---

### Task 1: Gateway 最终结果与严格 TTFT

**Files:**
- Create: `../tokenlive-gateway/pkg/core/outcome.go`
- Create: `../tokenlive-gateway/pkg/core/outcome_status.go`
- Modify: `../tokenlive-gateway/pkg/core/context.go`
- Modify: `../tokenlive-gateway/pkg/core/filter.go`
- Modify: `../tokenlive-gateway/pkg/core/engine.go`
- Modify: `../tokenlive-gateway/pkg/core/engine_response.go`
- Modify: `../tokenlive-gateway/pkg/llm/sse_intercept_writer.go`
- Modify: `../tokenlive-gateway/pkg/llm/upstream/call.go`
- Modify: `../tokenlive-gateway/pkg/invoker/cluster.go`
- Test: `../tokenlive-gateway/pkg/core/outcome_test.go`
- Test: `../tokenlive-gateway/pkg/core/engine_outcome_test.go`
- Test: `../tokenlive-gateway/pkg/llm/sse_intercept_writer_test.go`

**Interfaces:**
- Produces: `core.RequestOutcome`, `core.RequestFinalStatus`, `core.OutcomeConsumerFilter`, `(*GatewayContext).Outcome()`。
- Consumed by: Tasks 2–4 的 status reporter 和现有 metrics/access-log/event filters。

- [ ] **Step 1: 写 final status 和 outcome 冻结失败测试**

```go
func TestFreezeOutcomeCopiesFinalState(t *testing.T) {
    gctx := &GatewayContext{
        StartTime: time.Unix(100, 0),
        Model: "gpt-test",
        InputTokens: 10,
        OutputTokens: 20,
        History: []AttemptRecord{{EndpointID: "ep-1", Provider: "openai", Success: true}},
    }
    require.NoError(t, gctx.FinalizeStatus(RequestFinalStatus{
        Code: 200, Success: true, CompletedAt: time.Unix(101, 0),
    }))
    require.NoError(t, gctx.FreezeOutcome())

    outcome, ok := gctx.Outcome()
    require.True(t, ok)
    require.Equal(t, int64(10), outcome.InputTokens)
    require.Equal(t, "ep-1", outcome.EndpointID)

    gctx.History[0].EndpointID = "changed"
    require.Equal(t, "ep-1", outcome.Attempts[0].EndpointID)
}
```

同时覆盖 400、401、403、429、503、upstream 4xx/5xx、timeout、premature stream closure 的分类。

- [ ] **Step 2: 运行测试确认当前实现失败**

Run:

```bash
go test ./pkg/core -run 'TestFreezeOutcome|TestDefaultOutcomeClassifier' -count=1
```

Expected: FAIL，因为类型和冻结方法尚不存在。

- [ ] **Step 3: 实现最终结果类型和分类器**

核心接口固定为：

```go
type OutcomeErrorKind string

type RequestFinalStatus struct {
    Code        int
    WireCode    int
    Success     bool
    ErrorKind   OutcomeErrorKind
    CompletedAt time.Time
}

type RequestOutcome struct {
    StartedAt, CompletedAt time.Time
    RequestType RequestType
    OriginalModel, FinalModel string
    Provider, EndpointID, EndpointCode string
    Stream bool
    FinalStatus RequestFinalStatus
    Latency, FirstByte, TTFT time.Duration
    InputTokens, OutputTokens, CachedTokens, CacheCreationTokens int64
    Cost float64
    Attempts []AttemptOutcome
}
```

`FreezeOutcome` 必须深拷贝 slice，不包含 raw body、API Key、用户、请求 ID或错误文本。

- [ ] **Step 4: 写 SSE first-byte/first-token 测试**

覆盖：

```go
func TestWriteHeaderDoesNotRecordTTFT(t *testing.T)
func TestUsageOnlyEventDoesNotRecordTTFT(t *testing.T)
func TestContentDeltaRecordsTTFTAfterSuccessfulWrite(t *testing.T)
func TestWriteFailureDoesNotRecordTTFT(t *testing.T)
```

OpenAI chat、Responses 和 Anthropic Messages 各提供至少一个有效 token event 与一个控制 event。

- [ ] **Step 5: 实现 `MarkResponseStarted`、`MarkFirstByte`、`MarkFirstToken`**

`WriteHeader` 只调用 `MarkResponseStarted(statusCode)`；底层 `Write` 成功写出字节后记录 first byte；解析到实际 content/reasoning/tool argument 增量且写成功后记录 TTFT。把现有 TTFT timeout helper 改名为 first-byte timeout helper，并把 `TTFT > 0` 的“响应已开始”判断替换为 `ResponseStarted()`。

- [ ] **Step 6: 把 Engine 改成两阶段 outbound**

固定顺序：

```text
invocation → completion validation → FinalizeStatus
→ 普通 outbound（token settlement、sticky）
→ FreezeOutcome
→ OutcomeConsumerFilter（metrics、status、access log、event）
→ 写最终 response/error
```

inbound rejection 也必须先分类并冻结，再运行声明 `InboundSafe` 的 outcome consumer。

- [ ] **Step 7: 验证最终结果一致性**

Run:

```bash
go test ./pkg/core ./pkg/llm ./pkg/filters/outbound -count=1
go test -race ./pkg/core ./pkg/llm -count=1
```

Expected: PASS；premature stream 在所有统计 sink 中均为失败，response header 不再等于 TTFT。

- [ ] **Step 8: 记录任务检查点**

```bash
git -C ../tokenlive-gateway diff --check
git -C ../tokenlive-gateway status --short
```

不提交；确认未触碰已有未跟踪 `docs/superpowers/`。

---

### Task 2: Gateway 版本化协议、直方图与分钟预聚合

**Files:**
- Create: `../tokenlive-gateway/pkg/statusreport/dto.go`
- Create: `../tokenlive-gateway/pkg/statusreport/histogram.go`
- Create: `../tokenlive-gateway/pkg/statusreport/aggregate.go`
- Test: `../tokenlive-gateway/pkg/statusreport/dto_test.go`
- Test: `../tokenlive-gateway/pkg/statusreport/histogram_test.go`
- Test: `../tokenlive-gateway/pkg/statusreport/aggregate_test.go`

**Interfaces:**
- Consumes: `core.RequestOutcome` from Task 1。
- Produces: `statusreport.RequestMetric`, `MetricBucket`, `MetricsPayload`, `PublishAck`, `MinuteAggregator`。
- Consumed by: Tasks 3、4、7。

- [ ] **Step 1: 写旧 JSON 兼容测试**

断言旧字段名和类型不变：

```go
func TestRequestMetricKeepsV05JSONContract(t *testing.T) {
    raw := `{"time":1,"model":"m","success":true,"input_tokens":2,"output_tokens":3,"cached_tokens":0,"cache_creation_tokens":0,"cost":0.1,"attempts":[]}`
    var got RequestMetric
    require.NoError(t, json.Unmarshal([]byte(raw), &got))
    encoded, err := json.Marshal(got)
    require.NoError(t, err)
    require.JSONEq(t, raw, string(encoded))
}
```

- [ ] **Step 2: 定义 additive DTO**

`RequestMetric` 保留 v0.5.0 字段，并新增 provider/endpoint/status/latency/TTFT 等可选字段。新聚合 envelope 固定为：

```go
type MetricsPayload struct {
    SchemaVersion int `json:"schema_version"`
    InstanceID string `json:"instance_id"`
    InstanceEpoch string `json:"instance_epoch"`
    Sequence uint64 `json:"sequence"`
    Generation uint64 `json:"generation"`
    SentAtMS int64 `json:"sent_at_ms"`
    Buckets []MetricBucket `json:"buckets,omitempty"`
    Metrics []RequestMetric `json:"metrics,omitempty"`
    OpenEndpoints []string `json:"open_endpoints"`
    OpenServices []string `json:"open_services"`
    Health HealthSnapshot `json:"health"`
}
```

历史 generation 由 Admin handshake 提供；instance epoch 是 Gateway 每次启动的 UUID，不能混用。

- [ ] **Step 3: 写固定直方图测试**

使用明确边界，例如：

```go
var LatencyBoundsMS = []int64{10, 25, 50, 100, 250, 500, 1000, 2500, 5000, 10000, 30000, 60000}
```

测试边界值、overflow、合并、P50/P95/P99 近似和版本不匹配错误。

- [ ] **Step 4: 实现版本化直方图**

```go
type Histogram struct {
    Version uint16 `json:"version"`
    Counts []uint64 `json:"counts"`
}

func NewLatencyHistogram() Histogram
func NewTTFTHistogram() Histogram
func (h *Histogram) Observe(milliseconds int64)
func (h *Histogram) Merge(other Histogram) error
func (h Histogram) Quantile(q float64) (int64, bool)
```

TTFT 缺失时不调用 `Observe`。

- [ ] **Step 5: 写四类 scope 聚合测试**

一个带模型、供应商和端点的 outcome 应生成 global/model/provider/endpoint 四行；缺失 provider 时生成 provider `unknown`，但 global 仍正确。两个同分钟同 scope 请求合并为一行，跨分钟不合并。

- [ ] **Step 6: 实现 `MinuteAggregator`**

```go
type MinuteAggregator struct {
    mu sync.Mutex
    buckets map[BucketKey]*MetricBucket
}

func (a *MinuteAggregator) Add(outcome *core.RequestOutcome) error
func (a *MinuteAggregator) Drain() []MetricBucket
func (a *MinuteAggregator) Size() int
```

只保存聚合字段与名称快照，不保存 RequestOutcome 本身。

- [ ] **Step 7: 验证协议和聚合**

```bash
go test ./pkg/statusreport -run 'TestRequestMetric|TestHistogram|TestMinuteAggregator' -count=1
go test -race ./pkg/statusreport -count=1
```

Expected: PASS。

---

### Task 3: Gateway reporting 配置、HTTP publisher、handshake 和 retry

**Files:**
- Create: `../tokenlive-gateway/pkg/statusreport/config.go`
- Create: `../tokenlive-gateway/pkg/statusreport/health.go`
- Create: `../tokenlive-gateway/pkg/statusreport/http_publisher.go`
- Create: `../tokenlive-gateway/pkg/statusreport/retry.go`
- Modify: `../tokenlive-gateway/pkg/config/http_provider.go`
- Test: `../tokenlive-gateway/pkg/statusreport/config_test.go`
- Test: `../tokenlive-gateway/pkg/statusreport/http_publisher_test.go`
- Test: `../tokenlive-gateway/pkg/statusreport/retry_test.go`

**Interfaces:**
- Consumes: Task 2 DTO。
- Produces: `Config`, `Publisher`, `GenerationClient`, `RetryPublisher`, `Health`。
- Consumed by: Task 4 batcher/bootstrap。

- [ ] **Step 1: 写配置默认与 transport 选择测试**

覆盖：缺配置等价 `auto`；有 Redis 时 `auto→redis`；无 Redis但有 Admin URL 时 `auto→http`；standalone 显式 `http`；显式 transport 缺依赖时报错；`disabled` 不构造 worker。

- [ ] **Step 2: 实现配置类型**

```go
type Config struct {
    Enabled *bool `mapstructure:"enabled"`
    Transport Transport `mapstructure:"transport"`
    QueueSize, BatchSize int
    FlushInterval, HeartbeatInterval time.Duration
    RequestTimeout, ShutdownTimeout time.Duration
    Retry RetryConfig
}

func DefaultConfig() Config
func LoadConfig(v *viper.Viper) (Config, error)
func (c Config) ResolveTransport(redisAvailable bool, adminURL string) (Transport, error)
```

默认 queue=5000、batch=100、flush=5s、heartbeat=5s、request timeout=5s、shutdown timeout=10s。

- [ ] **Step 3: 写 generation handshake 和严格 ack 测试**

Admin 状态接口返回：

```json
{"generation":3,"collection_started_at":"...","business_timezone":"Asia/Shanghai"}
```

严格 batch ack 返回：

```json
{"accepted":true,"generation":3,"accepted_sequence":9,"duplicate":false}
```

测试 generation mismatch、重复成功、空 body 在 strict mode 下失败、HTTP 5xx 可重试、HTTP 4xx 不重试。

- [ ] **Step 4: 实现 `GenerationClient` 和 `HTTPPublisher`**

```go
type GenerationClient interface {
    Current(ctx context.Context) (GenerationState, error)
}

type Publisher interface {
    Publish(ctx context.Context, payload MetricsPayload) (PublishAck, error)
    Close(ctx context.Context) error
}
```

复用统一 `NewAdminHTTPClient(timeout, tlsSkipVerify)`，限制响应 body 为 64 KiB，关闭 idle connections。

- [ ] **Step 5: 实现有限 retry decorator**

```go
type RetryPublisher struct { delegate Publisher; cfg RetryConfig }
func (p *RetryPublisher) Publish(ctx context.Context, payload MetricsPayload) (PublishAck, error)
```

同一个 payload 重试时 sequence 不变；只重试网络、timeout、429 和 5xx；尊重 context 与 Retry-After；protocol/generation mismatch 返回结构化错误给 batcher处理。

- [ ] **Step 6: 验证配置、TLS、ack 和 retry**

```bash
go test ./pkg/statusreport -run 'TestConfig|TestHTTPPublisher|TestRetryPublisher|TestGenerationClient' -count=1
```

Expected: PASS。

---

### Task 4: Gateway batcher、健康状态、关闭 drain 与 bootstrap

**Files:**
- Create: `../tokenlive-gateway/pkg/statusreport/batcher.go`
- Create: `../tokenlive-gateway/pkg/statusreport/redis_aggregator.go`
- Modify: `../tokenlive-gateway/pkg/filters/outbound/status_collector.go`
- Modify: `../tokenlive-gateway/pkg/filters/outbound/status_collector_test.go`
- Modify: `../tokenlive-gateway/internal/bootstrap/engine.go`
- Modify: `../tokenlive-gateway/config/local.yml`
- Modify: `../tokenlive-gateway/config/prod.yml`
- Modify: `../tokenlive-gateway/config/local.yml.example`
- Modify: `../tokenlive-gateway/.env.example`
- Test: `../tokenlive-gateway/pkg/statusreport/batcher_test.go`

**Interfaces:**
- Consumes: Tasks 1–3。
- Produces: `Reporter.EnqueueOutcome(*core.RequestOutcome)`, `Reporter.Health()`, `Reporter.Close(context.Context)`。
- Consumed by: standalone through normal Gateway lifecycle；Admin receives its payload in Task 7。

- [ ] **Step 1: 写 batch threshold、timer、heartbeat 和 close 测试**

使用 fake clock/channel barrier，不能靠固定长 `Sleep`。覆盖：第 100 条触发；不足阈值时 timer flush；无数据 heartbeat；Close drain worker 本地桶；Close 两次；enqueue 与 Close 并发；publish 阻塞时 shutdown timeout。

- [ ] **Step 2: 实现单 worker `Batcher`**

```go
type Reporter interface {
    EnqueueOutcome(*core.RequestOutcome) EnqueueResult
    Health() Health
    Close(context.Context) error
}
```

worker 首先完成 generation handshake；握手前继续有界聚合。一个逻辑 batch 只分配一次 sequence。generation mismatch 时丢弃旧 generation 的待发 batch，重新握手并记录 gap。

- [ ] **Step 3: 实现 gap/health 计数**

Health 至少包含 queue depth、dropped batches/records、publish failures、last successful publish time、shutdown flush failure。队列满时非阻塞返回 `EnqueueDroppedFull`。

- [ ] **Step 4: 重写 `StatusCollectorFilter` 为薄适配器**

```go
type StatusCollectorFilter struct { reporter statusreport.Reporter }
func (f *StatusCollectorFilter) RequiresRequestOutcome() {}
func (f *StatusCollectorFilter) OnResponse(gctx *core.GatewayContext) error
```

只读取冻结 outcome。Redis 模式用有界 `RedisAggregator`，不再每请求启动 goroutine；HTTP 模式使用 batcher。

- [ ] **Step 5: bootstrap 显式持有 reporter 并调整 cleanup**

构造 filter 前加载 `status_reporting`。cleanup 顺序固定为：关闭 status reporter → 关闭 access log filter → compensation worker → telemetry → engine → event publisher。所有 Close 幂等并有超时。

- [ ] **Step 6: 更新 Gateway 示例配置**

普通 Gateway 使用 `transport: auto`；不改变既有生产选择。完整列出 queue/batch/flush/retry 默认。

- [ ] **Step 7: 运行 Gateway 完整验证**

```bash
go test ./pkg/statusreport ./pkg/filters/outbound ./internal/bootstrap -count=1
go test -race ./pkg/statusreport ./pkg/filters/outbound -count=1
go test ./... -count=1
```

Expected: PASS；若仓库既有外部依赖测试失败，记录准确包名和输出，不掩盖。

---

### Task 5: Admin runtime DB override 与 SQLite 运行参数

**Files:**
- Modify: `../tokenlive-admin/adminapp/app.go`
- Modify: `../tokenlive-admin/internal/bootstrap/bootstrap.go`
- Modify: `../tokenlive-admin/pkg/gormx/gorm.go`
- Test: `../tokenlive-admin/adminapp/app_test.go`
- Test: `../tokenlive-admin/internal/bootstrap/bootstrap_test.go`
- Test: `../tokenlive-admin/pkg/gormx/gorm_test.go`

**Interfaces:**
- Produces: `adminapp.DatabaseOptions` and `Options.Database`。
- Consumed by: Task 12 standalone assemble。

- [ ] **Step 1: 写 override 顺序和校验测试**

```go
type DatabaseOptions struct {
    Type string
    DSN string
    AutoMigrate *bool
}
```

测试 nil 保持 TOML；非 nil 覆盖 Type/DSN/AutoMigrate 并清空 Resolver；空 Type/DSN 拒绝；SQLite embed override 要求绝对路径。

- [ ] **Step 2: 实现公开 options 到 bootstrap override 的映射**

覆盖必须发生在 `config.MustLoad` 之后、`wirex.BuildInjector` 之前。不得使用 `os.Setenv`，不得公开 `*gorm.DB`。

- [ ] **Step 3: 写 SQLite pragma 和路径测试**

打开临时数据库后执行：

```sql
PRAGMA journal_mode;
PRAGMA busy_timeout;
PRAGMA foreign_keys;
```

断言 WAL、正 busy timeout、foreign keys=1；普通路径父目录被创建；SQLite URI 不被直接交给 `filepath.Dir`。

- [ ] **Step 4: 实现 gormx SQLite 配置**

对 SQLite 限制连接数，应用 WAL/busy timeout/foreign keys。把文件路径和构造后的 DSN 分开，确保绝对路径包含空格时仍正确。

- [ ] **Step 5: 验证 Admin embed DB 边界**

```bash
go test ./adminapp ./internal/bootstrap ./pkg/gormx -count=1
```

Expected: PASS。

---

### Task 6: Admin 配置、协议 DTO 与完整 schema

**Files:**
- Modify: `../tokenlive-admin/internal/config/config.go`
- Create: `../tokenlive-admin/pkg/metrics/protocol.go`
- Create: `../tokenlive-admin/pkg/metrics/recorder.go`
- Create: `../tokenlive-admin/internal/mods/dashboard/schema/metric.go`
- Create: `../tokenlive-admin/internal/mods/dashboard/schema/batch.go`
- Create: `../tokenlive-admin/internal/mods/dashboard/schema/state.go`
- Create: `../tokenlive-admin/internal/mods/dashboard/schema/gap.go`
- Modify: `../tokenlive-admin/scripts/init.sql`
- Modify: `../tokenlive-admin/configs/dev/server.toml`
- Modify: `../tokenlive-admin/configs/prod/server.toml`
- Test: `../tokenlive-admin/internal/mods/dashboard/schema/schema_test.go`
- Test: `../tokenlive-admin/pkg/metrics/protocol_test.go`

**Interfaces:**
- Consumes: Task 2 JSON contract by structural compatibility, without Go import。
- Produces: Admin schema and `metrics.Recorder` for Tasks 7–11。

- [ ] **Step 1: 定义 `DashboardMetricsConfig` 与代码默认值**

字段包括 Enabled、BusinessTimezone、MinuteRetentionDays=30、DayRetentionDays=1095、CleanupInterval=6h、RollupInterval、IngestBatchRetention、MaxPastSkew=24h、MaxFutureSkew=5m、ActiveScopeLimit、FreshnessThreshold。普通 Admin 默认 disabled；standalone 配置在 Task 12 显式 enabled。

- [ ] **Step 2: 写 legacy/v2 协议兼容测试**

Admin 必须接受现有 `metrics` payload 和新 `buckets` payload；严格 v2 要求 instance/epoch/sequence/generation；空 metrics 可作为 breaker heartbeat。

- [ ] **Step 3: 定义五类 schema**

`dashboard_metric_minute`、`dashboard_metric_day`、`dashboard_ingest_batch`、`dashboard_collection_state`、`dashboard_data_gap`。minute/day 唯一键必须包含 generation、bucket、scope_type、scope_id；金额使用现有兼容 decimal 精度；直方图含 version 和 payload。

- [ ] **Step 4: 写 AutoMigrate 索引测试**

使用临时 SQLite，迁移两次，查询 `sqlite_master`/GORM migrator 断言五张表和唯一索引存在。

- [ ] **Step 5: 更新 `scripts/init.sql`**

加入与 GORM schema 一致的完整 DDL、唯一键和查询/清理索引。不能只依赖 AutoMigrate。

- [ ] **Step 6: 验证配置与 schema**

```bash
go test ./pkg/metrics ./internal/mods/dashboard/schema -count=1
```

Expected: PASS。

---

### Task 7: Admin 事务 ingestion、幂等和 generation handshake

**Files:**
- Create: `../tokenlive-admin/internal/mods/dashboard/dal/ingest.dal.go`
- Create: `../tokenlive-admin/internal/mods/dashboard/biz/histogram.go`
- Create: `../tokenlive-admin/internal/mods/dashboard/biz/ingest.go`
- Create: `../tokenlive-admin/internal/mods/resource/schema/gateway_metric.go`
- Modify: `../tokenlive-admin/internal/mods/resource/api/gateway_sync.go`
- Modify: `../tokenlive-admin/internal/mods/resource/wire.go`
- Modify: `../tokenlive-admin/internal/mods/dashboard/wire.go`
- Test: `../tokenlive-admin/internal/mods/dashboard/biz/ingest_test.go`
- Test: `../tokenlive-admin/internal/mods/resource/api/gateway_sync_test.go`

**Interfaces:**
- Consumes: Task 6 protocol/schema。
- Produces: `IngestService.RecordEnvelope`, `CurrentGeneration`, strict ack；consumed by Gateway Tasks 3–4 and Admin query Tasks 8–10。

- [ ] **Step 1: 写事务幂等测试**

同一个 `(instance_id, instance_epoch, sequence)` 发送两次，只累计一次并返回 `duplicate=true`。模拟第二个 UPSERT 失败时，batch 去重记录和前一个 UPSERT均回滚。

- [ ] **Step 2: 写 generation 和事件时间测试**

当前 generation=3：generation 2 不累计且返回 3；generation 3 接受；过去 24h 边界接受，早一毫秒拒绝并记 gap；未来 5m 边界接受，之后拒绝。

- [ ] **Step 3: 实现 `IngestDAL.WithEnvelopeTransaction`**

单事务流程：锁/读取 state → 校验 generation → 插入 batch key → scope 白名单/上限 → 增量 UPSERT minute rows → 更新 state/health → commit。重复唯一键转换为成功 duplicate ack。

- [ ] **Step 4: 实现 legacy adapter 与 v2 ingestion**

```go
type Recorder interface {
    RecordEnvelope(context.Context, *metrics.MetricsEnvelope) (*metrics.PublishAck, error)
    CurrentGeneration(context.Context) (*metrics.GenerationState, error)
}
```

legacy 请求由 Admin 按请求时间转换成 global/model/provider/endpoint 聚合；缺失 provider/endpoint 进入 unknown。legacy 没有可重试 batch identity，因此只做一次提交，不宣称 exactly-once。

- [ ] **Step 5: 修改 GatewaySync 响应顺序**

DB commit 成功后再更新 `metrics.GlobalStore` 和 breaker snapshot，然后返回 JSON ack。DB 失败返回 5xx，不能先增加内存。增加 `GET /api/v1/gateway/metrics/state`，继续用 sync token 校验，供 Gateway handshake。

- [ ] **Step 6: 验证 API 合约**

```bash
go test ./internal/mods/dashboard/biz ./internal/mods/resource/api -run 'TestIngest|TestReportMetrics|TestCurrentGeneration' -count=1
```

Expected: PASS。

---

### Task 8: Admin SQLite 查询、直方图分位数与统一 Dashboard Service

**Files:**
- Create: `../tokenlive-admin/internal/mods/dashboard/dal/query.dal.go`
- Create: `../tokenlive-admin/internal/mods/dashboard/biz/query.go`
- Create: `../tokenlive-admin/internal/mods/dashboard/biz/source.go`
- Modify: `../tokenlive-admin/internal/mods/dashboard/api/dashboard.api.go`
- Modify: `../tokenlive-admin/internal/mods/dashboard/api/ws.go`
- Test: `../tokenlive-admin/internal/mods/dashboard/biz/query_test.go`
- Test: `../tokenlive-admin/internal/mods/dashboard/api/dashboard.api_test.go`

**Interfaces:**
- Consumes: minute/day/state/gap data from Tasks 6–7。
- Produces: `DashboardService.Overview/Trends/ModelRanking`, shared by REST and WebSocket。

- [ ] **Step 1: 写 overview/trends/ranking 查询测试**

固定 clock 和业务时区，插入跨分钟、跨日、四种 scope 数据。断言今日边界、1h/6h/24h/7d、Token/费用、成功率、平均值和 P50/P95/P99。缺口区间不能被补成零。

- [ ] **Step 2: 实现数据库中立查询 DAL**

仅使用 `SUM`、`GROUP BY` 和半开时间范围；时间步长聚合在 Go 中完成。资源显示名优先当前资源，删除后使用 snapshot。

- [ ] **Step 3: 实现整源 `DashboardSource`**

```go
type Source interface {
    Overview(context.Context, OverviewQuery) (*OverviewResult, error)
    Trends(context.Context, TrendsQuery) (*TrendsResult, error)
    ModelRanking(context.Context, RankingQuery) (*RankingResult, error)
}
```

Prometheus 配置且健康时整套选择 Prometheus，否则整套 SQLite；SQLite 异常才使用内存短时降级并标记 incomplete。Redis 不按字段混入。

- [ ] **Step 4: 把 REST 和 WebSocket 改为共享 service**

保留现有字段，同时在响应顶层增加 source、collection_started_at、last_persisted_at、fresh、complete、dropped_batches、dropped_records、gap_count、gaps。WebSocket 不重新计算另一套逻辑。

- [ ] **Step 5: 验证整源切换**

测试 Prometheus healthy、Prometheus failure→SQLite、SQLite failure→memory incomplete，禁止出现 overview 来自 Prometheus而 ranking 来自 SQLite。

- [ ] **Step 6: 运行 Dashboard 查询测试**

```bash
go test ./internal/mods/dashboard/biz ./internal/mods/dashboard/api -count=1
```

Expected: PASS。

---

### Task 9: Admin 日封账、retention、健康状态、时区与 generation 重置

**Files:**
- Create: `../tokenlive-admin/internal/mods/dashboard/dal/maintenance.dal.go`
- Create: `../tokenlive-admin/internal/mods/dashboard/biz/maintenance.go`
- Create: `../tokenlive-admin/internal/mods/dashboard/biz/settings.go`
- Create: `../tokenlive-admin/internal/mods/dashboard/api/settings.go`
- Modify: `../tokenlive-admin/internal/mods/dashboard/main.go`
- Modify: `../tokenlive-admin/internal/mods/dashboard/wire.go`
- Modify: `../tokenlive-admin/internal/mods/dashboard/api/dashboard.api.go`
- Test: `../tokenlive-admin/internal/mods/dashboard/biz/maintenance_test.go`
- Test: `../tokenlive-admin/internal/mods/dashboard/biz/settings_test.go`
- Test: `../tokenlive-admin/internal/mods/dashboard/api/settings_test.go`

**Interfaces:**
- Consumes: Tasks 6–8。
- Produces: maintenance lifecycle and `/dashboard/status|timezone|reset` endpoints。

- [ ] **Step 1: 写幂等日封账测试**

按 `Asia/Shanghai` 上一日边界从 minute 重建 day；运行两次结果不翻倍；插入迟到分钟后第三次重建正确更新；启动补算最近数日。

- [ ] **Step 2: 写 retention 和旧 generation 清理测试**

30 天分钟和 1095 天日数据边界；批次去重保留超过最大 retry；重置后查询立即看不到旧 generation，后台再分批物理删除。

- [ ] **Step 3: 实现 `MaintenanceTask`**

Start 时先补封账和清理一次，再按 ticker 运行。失败只记录日志和 collection state，不终止 Admin；不用在线 VACUUM。

- [ ] **Step 4: 写时区锁定与重置测试**

没有数据时可以设置有效 IANA 时区；已有数据返回 HTTP 409；重置推进 generation、重置采集起点和健康计数，不删除 `event_log`、用户、配置。

- [ ] **Step 5: 实现 settings service/API**

```text
GET  /api/v1/dashboard/status
GET  /api/v1/dashboard/timezone
PUT  /api/v1/dashboard/timezone
POST /api/v1/dashboard/reset
```

reset body 要求明确确认字段，例如 `{"confirm":"RESET_DASHBOARD"}`，并受 Dashboard 管理权限控制。

- [ ] **Step 6: Dashboard module 管理迁移和任务 lifecycle**

`Init` 在 server 接收请求前 AutoMigrate 五张表、初始化 state、启动 maintenance；`Release` 取消 goroutine并等待退出。Wire 用构造函数注入，运行 `make wire` 更新生成文件。

- [ ] **Step 7: 验证 maintenance/settings**

```bash
make wire
go test ./internal/mods/dashboard/... -count=1
```

Expected: PASS；`wire_gen.go` 与 provider set 一致。

---

### Task 10: Admin 前端健康提示、缺口、时区与重置

**Files:**
- Modify: `../tokenlive-admin/frontend/src/apis/modules/dashboard.js`
- Modify: `../tokenlive-admin/frontend/src/views/home/index.vue`
- Create or Modify: `../tokenlive-admin/frontend/src/views/system/dashboard-settings.vue`
- Modify: relevant router/menu files discovered in current Admin frontend
- Test: corresponding `*.test.js`/`*.spec.js` under existing frontend test convention

**Interfaces:**
- Consumes: Tasks 8–9 API。
- Produces: 用户可见的 freshness/completeness 和安全重置交互。

- [ ] **Step 1: 写 API client 和组件行为测试**

覆盖：完整数据不显示警告；fresh=false 显示最近成功时间；complete=false 显示缺口；刚启用显示采集起点；趋势 gap 产生 `null` 断点；有数据时禁用时区；reset 必须输入确认文案。

- [ ] **Step 2: 扩展 dashboard API client**

加入 status/timezone/reset 方法；修正现有 `rankingSortBy.value` 的初次请求问题，并避免重复 static counts 请求。

- [ ] **Step 3: 首页增加非阻塞状态区**

沿用现有视觉语言，展示 source、collection started、last persisted。现有卡片仍展示可用值，但 incomplete 时带说明；无数据源时显示不可用而不是全零。

- [ ] **Step 4: 图表把缺口映射为 null**

后端 gaps 与时间点相交时写 `null`，不写 0；tooltip 说明采集缺口。

- [ ] **Step 5: 实现设置页/区域**

显示固定业务时区；已有数据时禁用编辑并提示先重置。重置二次确认明确“不影响配置、用户、API Key 和运维事件”。

- [ ] **Step 6: 格式化并测试前端**

```bash
cd ../tokenlive-admin/frontend
npx prettier --config .prettierrc --write \
  src/apis/modules/dashboard.js \
  src/views/home/index.vue \
  src/views/system/dashboard-settings.vue
npm test -- --run
npm run build
```

若仓库脚本名称不同，使用 `package.json` 中对应的 test/build script；记录实际命令和结果。

---

### Task 11: Admin 全量迁移、重启恢复和错误降级验证

**Files:**
- Create: `../tokenlive-admin/test/dashboard_sqlite_test.go`
- Modify: existing Admin test helpers as needed

**Interfaces:**
- Verifies: Tasks 5–10 as one Admin deliverable。

- [ ] **Step 1: 写 SQLite close/reopen 集成测试**

临时文件 DB：迁移 → ingest v2 batch → 查询 overview/trends/ranking → 关闭 sql.DB → 用同一文件重开 → 再查询，断言值、直方图和 state 不变。

- [ ] **Step 2: 写 busy/写失败降级测试**

持有写锁超过 busy timeout，断言 ingestion 返回可重试 5xx且内存未先增加；只读/磁盘写失败场景记录 incomplete；maintenance 失败保留旧数据。

- [ ] **Step 3: 写 reset 隔离测试**

reset 后旧飞行批次返回当前 generation 且不累计；新 generation batch 可写；`event_log` 和资源记录仍存在。

- [ ] **Step 4: 运行 Admin 全量验证**

```bash
go test ./internal/mods/dashboard/... ./internal/mods/resource/api ./test/... -count=1
go test -race ./internal/mods/dashboard/... ./internal/mods/resource/api -count=1
go test ./... -count=1
```

Expected: PASS；数据库测试对称清理数据，不产生脏数据。

- [ ] **Step 5: 检查 Admin diff**

```bash
git -C ../tokenlive-admin diff --check
git -C ../tokenlive-admin status --short
```

不提交。

---

### Task 12: standalone 绝对 data-dir、Admin override、配置与关闭顺序

**Files:**
- Modify: `cmd/tokenlive/main.go`
- Modify: `cmd/tokenlive/main_test.go`
- Modify: `internal/assemble/assemble.go`
- Modify: `internal/assemble/assemble_test.go`
- Modify: `config/all-in-one.example.yml`
- Modify: `config/brew.yml`
- Modify: `configs/admin/conf/server.toml`

**Interfaces:**
- Consumes: Task 5 `adminapp.DatabaseOptions` and Task 4 Gateway cleanup。
- Produces: deterministic all-in-one runtime for Tasks 13–14。

- [ ] **Step 1: 写无副作用 data-dir 表格测试**

```go
func resolveDataDir(raw, cwd string) (string, error)
```

覆盖相对、绝对、clean、空白、data-dir 指向普通文件；不使用并发不安全的 `os.Chdir`。

- [ ] **Step 2: 实现 data-dir 与 DB/log 映射**

CLI fail-fast 创建绝对 data-dir；`AdminDBDSN=filepath.Join(resolvedDataDir,"tokenlive.db")`；相对日志拼到 resolvedDataDir。示例日志路径使用 `logs/tokenlive.log`，避免 `<data-dir>/data/logs`。

- [ ] **Step 3: 扩展 assemble options 和校验**

```go
type Options struct {
    // existing fields
    AdminDBDSN string
}
```

空或非绝对 DSN 在 Admin 初始化前返回错误。调用 `adminapp.New` 时固定 Type=`sqlite3`、DSN=绝对路径、AutoMigrate=true。

- [ ] **Step 4: 修正 shutdown 所有权**

`App.Close` 用 `sync.Once`/mutex 实现并发安全、幂等。顺序：停止 HTTP → Gateway cleanup（最终 flush）→ Admin shutdown（关闭 SQLite）。不能先关 Admin。

- [ ] **Step 5: 启用 standalone 完整默认配置**

Admin metrics Enabled=true、30/1095、业务时区默认空（首次启动固化系统时区）；Gateway `transport:http`、strict ack、5s flush。缺字段时代码默认仍安全。

- [ ] **Step 6: 本地跨仓编译验证**

由于本任务没有权限 commit/tag 两个依赖，使用仓库外临时 Go workspace，不把 replace 写入提交文件：

```bash
WORK_FILE="$(mktemp)/go.work"
GOWORK="$WORK_FILE" go work init \
  "$PWD" \
  "$PWD/../tokenlive-admin" \
  "$PWD/../tokenlive-gateway"
GOWORK="$WORK_FILE" go test ./cmd/tokenlive ./internal/... -count=1
```

Expected: PASS。永久 `go.mod` 版本固定留到 Task 15 的人工发布检查点后完成。

---

### Task 13: standalone SQLite restart 与完整 Gateway E2E

**Files:**
- Create: `test/e2e/sqlite_dashboard_test.go`
- Create: `test/e2e/gateway_dashboard_test.go`
- Modify: `Makefile`

**Interfaces:**
- Verifies: Gateway→Admin→SQLite→Dashboard→restart 完整链路。

- [ ] **Step 1: 写直接 ingestion restart E2E**

使用子进程而非同进程重启，避免 Admin `sync.Once` 配置污染。第一次从 cwd-A 启动，向 sync endpoint 发 v2 batch，登录查询 overview/trends/ranking，SIGTERM；第二次从 cwd-B 用同一 data-dir 启动并再次查询。

断言：

```text
<data-dir>/tokenlive.db 存在
<cwd-A>/data/tokenlive.db 不存在
<cwd-B>/data/tokenlive.db 不存在
```

- [ ] **Step 2: 写真实 Gateway telemetry E2E**

父测试启动 OpenAI-compatible `httptest.Server`，临时模型 endpoint 指向它；向 `/v1/chat/completions` 发请求；轮询直到 Gateway reporter flush 到 Admin；查询 Dashboard；SIGTERM 重启后再次查询。

- [ ] **Step 3: 覆盖 shutdown final flush**

发一个请求后立即 SIGTERM，不等待 5 秒 ticker；重启后该请求仍存在，证明 Gateway cleanup 发生在 Admin shutdown 前。

- [ ] **Step 4: 增加 Makefile 目标**

```make
test-unit:
	go test ./cmd/tokenlive ./internal/... -count=1

test-e2e:
	go test ./test/e2e -count=1 -timeout=180s
```

- [ ] **Step 5: 用临时 workspace 运行 E2E**

```bash
GOWORK="$WORK_FILE" go test ./test/e2e \
  -run 'TestSQLiteDashboardSurvivesRestart|TestGatewayMetricsReachSQLiteAndSurviveRestart' \
  -count=1 -timeout=240s
```

Expected: PASS。

---

### Task 14: 打包依赖确定性与 release 门禁

**Files:**
- Modify: `scripts/package-release.sh`
- Modify: `scripts/publish-brew-release.sh`
- Modify: `scripts/brew-install-local.sh`
- Modify: `scripts/publish-linux-release.sh`
- Create: `scripts/verify-release-package.sh`
- Create: `scripts/package-release_test.sh`
- Modify: `Makefile`
- Modify: `.github/workflows/release-brew.yml`
- Modify: `docs/homebrew.md`

**Interfaces:**
- Produces: official package uses only pinned module versions；local source requires explicit opt-in。

- [ ] **Step 1: 写 package script failing test**

设置 `USE_LOCAL_DEPS=0` 和无效兄弟仓路径，包仍应按 `go.mod` 成功；设置 `USE_LOCAL_DEPS=1` 且路径无效必须失败；正式 publish 遇到 local deps 必须失败。

- [ ] **Step 2: 默认禁用本地 replace**

`package-release.sh` 只有 `USE_LOCAL_DEPS=1` 时执行 `go mod edit -replace`。默认运行 `go mod download` 和 `go mod verify`，不执行隐式 `go mod tidy`。拆分 backend module source 与 Admin frontend source。

- [ ] **Step 3: 实现 package provenance verifier**

检查二进制、配置、Admin TOML、web 和 libexec；用 `go version -m` 断言 Admin/Gateway version 等于 standalone `go.mod`，正式包不含本地 filesystem replace 或 `(devel)` dependency。

- [ ] **Step 4: 修改 publish scripts**

正式 Brew/Linux publish 强制 `USE_LOCAL_DEPS=0`；本地 Homebrew 开发脚本显式 `USE_LOCAL_DEPS=1`；上传前对所有架构包运行 verifier。

- [ ] **Step 5: 增加 workflow 门禁**

发布前运行 module verify、unit、script、SQLite restart E2E、完整 Gateway E2E、package test。只有全部通过后才能执行 `gh release create/upload` 和 Formula 更新。

- [ ] **Step 6: 验证 shell 和 package tests**

```bash
bash -n scripts/package-release.sh
bash -n scripts/publish-brew-release.sh
bash -n scripts/verify-release-package.sh
bash scripts/install-brew-config_test.sh
bash scripts/package-release_test.sh
python3 -m unittest scripts/update_homebrew_formula_test.py
```

Expected: PASS。

---

### Task 15: 依赖版本人工发布检查点与 standalone 固定版本

**Files:**
- Modify after authorized releases: `go.mod`
- Modify after authorized releases: `go.sum`

**Interfaces:**
- Consumes: 已发布且包含 Tasks 1–11 的 Admin/Gateway versions。
- Produces: standalone 不依赖临时 workspace 即可独立构建。

- [ ] **Step 1: 汇总两个依赖仓库验证证据**

记录各自 `git diff --check`、unit、race、frontend build 和全量测试结果。确认 Gateway 的已有 `docs/superpowers/` 未进入变更。

- [ ] **Step 2: 停止并请求用户授权 commit/tag/publish**

这是不可逆且对外动作。没有明确授权时不得执行，也不得猜测版本号。用户可以自行发布后提供版本号。

- [ ] **Step 3: 发布完成后固定精确版本**

```bash
: "${TOKENLIVE_ADMIN_VERSION:?set the exact user-authorized Admin release version}"
: "${TOKENLIVE_GATEWAY_VERSION:?set the exact user-authorized Gateway release version}"
go get "github.com/tokenlive/tokenlive-admin@${TOKENLIVE_ADMIN_VERSION}"
go get "github.com/tokenlive/tokenlive-gateway@${TOKENLIVE_GATEWAY_VERSION}"
go mod tidy
go mod verify
```

两个环境变量只能设置为用户明确提供或授权发布产生的真实版本；未设置时命令必须立即
失败，不能猜测版本号。

- [ ] **Step 4: 确认无 replace 且独立测试**

```bash
go mod edit -json | jq -e '.Replace == null or (.Replace | length == 0)'
GOWORK=off go test ./... -count=1
```

Expected: PASS。

---

### Task 16: 最终全量验证与规格对照

**Files:**
- Modify only if verification exposes defects
- Verify: `docs/specs/2026-08-13-dashboard-sqlite-persistence-design.md`

**Interfaces:**
- Verifies: 完整交付，不新增功能。

- [ ] **Step 1: Gateway 最终验证**

```bash
cd ../tokenlive-gateway
go test ./... -count=1
go test -race ./pkg/core ./pkg/llm ./pkg/statusreport ./pkg/filters/outbound -count=1
git diff --check
```

- [ ] **Step 2: Admin 最终验证**

```bash
cd ../tokenlive-admin
make wire
go test ./... -count=1
go test -race ./internal/mods/dashboard/... ./internal/mods/resource/api -count=1
cd frontend && npm test -- --run && npm run build
cd .. && git diff --check
```

- [ ] **Step 3: standalone 最终验证**

```bash
cd ../tokenlive-standalone
GOWORK=off go mod verify
GOWORK=off make test-unit
GOWORK=off make test-e2e
make test-scripts
make test-package
git diff --check
```

- [ ] **Step 4: 性能基准/烟测**

以 100 RPS 运行至少覆盖多个 flush 周期，记录代理 P50/P95 增量、queue depth、flush duration、SQLite busy time、Admin CRUD latency 和 DB 文件增长。验收标准：请求路径无同步 DB 写；无持续队列增长；无 SQLite lock error；Dashboard 常用查询在目标机器可交互使用。

- [ ] **Step 5: 对照规格逐项检查**

确认 30 天/1095 天、UTC/业务时区、四 scope、直方图、generation、gap、整源切换、breaker 内存状态、严格隐私、独立 reset、data-dir、重启恢复、发布门禁均有实现和测试。

- [ ] **Step 6: 如实报告结果**

列出三个仓库的修改文件、测试命令与结果、性能数据、未执行的 commit/tag/publish，以及任何因环境缺失而跳过或失败的验证。没有证据不得声称完成或通过。
