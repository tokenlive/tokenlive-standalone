# 单机看板 SQLite 持久化设计

## 背景

TokenLive 有两种部署形态：

1. `tokenlive-gateway` 与 `tokenlive-admin` 独立运行的 standalone 双进程模式；
2. `tokenlive-standalone` 同时嵌入 Gateway 和 Admin 的 all-in-one 单进程模式。

当前 Gateway 会把请求指标批量上报到 Admin，但 Admin 只把指标写入进程内的
`metrics.GlobalStore`。在没有 Prometheus 或 Redis 的单机默认配置中，这导致：

- Admin 或 all-in-one 进程重启后，当日请求数、Token、费用、趋势和排行归零；
- 6 小时、24 小时、7 天等长窗口没有有效的本地数据来源；
- 平均延迟、TTFT 和分位数缺少本地持久化数据；
- 指标队列满或 HTTP 上报失败时没有完整性提示；
- 首页无法区分“没有流量”和“采集发生缺口”。

现有 Admin 已使用 SQLite 保存配置、资源和运维事件。单机部署要求默认不依赖
Redis、Prometheus、ClickHouse 或新的外部进程，因此本设计使用同一个
`tokenlive.db` 保存低基数聚合指标。

## 目标

- 单机默认部署在进程重启后保留首页运维数据。
- 支持当前、今日、1 小时、6 小时、24 小时和 7 天等首页窗口。
- 支持全局、模型、供应商和端点四类独立范围的趋势与排行。
- 支持请求量、成功率、Token、费用、平均延迟、平均 TTFT，以及近似
  P50/P95/P99。
- 不把 SQLite 写入放入模型代理请求的同步关键路径。
- 双进程与 all-in-one 使用同一套采集协议、存储语义和查询逻辑。
- Gateway 不依赖 Admin 或 SQLite；Admin 独占指标存储；standalone 只负责组装。
- 发生丢批、写入失败、异常时间戳或采集刚启动时，首页明确展示数据健康状态。
- 保持现有 Dashboard REST 和 WebSocket 客户端兼容。

## 非目标

- 不保存逐请求明细。
- 不实现提示词、响应内容、请求 ID 或错误文本检索。
- 不提供模型、供应商、端点和租户之间的任意多维组合分析。
- 不把看板数据定义为财务对账或审计真相。
- 不要求外部 Redis、Prometheus 或 ClickHouse 才能使用单机首页。
- 不在 `tokenlive-standalone` 中复制 Admin 的 Dashboard 业务逻辑。
- 本阶段不支持 all-in-one 多实例；该场景继续使用分部署及外部存储方案。

## 仓库责任边界

### tokenlive-gateway

Gateway 负责：

- 在最终请求结果稳定后构造不可变的请求结果快照；
- 采集模型、供应商、端点、成功状态、Token、费用、延迟和 TTFT；
- 在内存中预聚合 UTC 分钟桶；
- 维护有界发送队列、批量 flush、有限重试和关闭 drain；
- 通过版本化 HTTP 协议向 Admin 上报；
- 暴露队列丢弃、发送失败和最近成功发送状态。

Gateway 不打开 Admin SQLite，不 import Admin 包，也不决定 Dashboard 的查询和保留
策略。

### tokenlive-admin

Admin 负责：

- 校验、兼容和接收 Gateway 指标协议；
- 在同一事务内执行批次去重和聚合 UPSERT；
- 管理 SQLite schema、迁移、索引、WAL 和 busy timeout；
- 管理分钟保留、日封账、日数据保留和清理任务；
- 提供统一的 Dashboard Store/Service；
- 实现整源切换、健康状态、时区配置和历史重置；
- 为 REST 与 WebSocket 提供相同统计结果；
- 在 Admin 前端展示数据健康状态和危险操作。

Admin 不 import Gateway；双方只共享稳定的 JSON 协议语义。

### tokenlive-standalone

standalone 负责：

- 启用单机默认的 SQLite Dashboard 配置；
- 把 `-data-dir` 确定地映射为 `<data-dir>/tokenlive.db` 和日志目录；
- 使用类型化 Admin runtime override 传递绝对 SQLite DSN；
- 组合已发布的 Admin 和 Gateway 模块；
- 为 all-in-one 提供重启持久化集成测试；
- 固定依赖版本并保证打包使用与 `go.mod` 一致的源码。

standalone 不拥有指标 schema、聚合、查询或页面业务逻辑。

## 最终请求结果语义

当前 Gateway 的部分统计 sink 会在流式 completion 校验之前执行，可能先把中途断流
记录为成功，再在之后将请求判定为失败。持久化之前必须统一执行顺序：

```text
调用与重试结束
→ 流式 completion 校验
→ 最终 success/status/error 分类
→ Token 与费用结算
→ 构造不可变 RequestOutcome
→ metrics/access log/event 等 sink 消费同一结果
→ 写出最终响应
```

`RequestOutcome` 至少包含：

- 请求发生时间；
- 原始模型与最终模型；
- 最终供应商和端点；
- 最终成功状态和 HTTP 状态码；
- 端到端 Gateway latency；
- TTFT；
- input/output/cached/cache-creation Token；
- 请求发生时计算并固化的费用；
- 必要的尝试结果副本。

成功率、access log、事件和 Dashboard 指标必须读取同一个最终结果，不能分别从仍可
变化的 `GatewayContext` 推导。

### TTFT 定义

TTFT 表示 Gateway 观察到的首次有效流式内容相对请求开始的时间。仅写出响应头不算首次
Token。非流式请求或无法观测 TTFT 时，该值为空；不能用数值 `0` 伪装成真实观测值。

### 费用定义

费用在请求发生时按当时生效的价格计算并固化。价格配置以后变化，不重算历史金额。
失败请求如果实际产生可计费 Token，也计入费用。该金额服务运维概览，不替代结构化
access log 的账务语义。

## 上报协议

保留现有端点和认证方式：

```text
POST /api/v1/gateway/metrics
X-Sync-Token: <token>
```

现有 `metrics`、`open_endpoints`、`open_services` 及已有请求字段保持兼容。顶层增加：

```text
schema_version
instance_id
instance_epoch
sequence
generation
sent_at
```

每条聚合记录增加：

```text
bucket_time
scope_type
scope_id
scope_code
scope_name
request_count
success_count
failure_count
input_tokens
output_tokens
cached_tokens
cache_creation_tokens
cost
latency_sum_ms
latency_count
latency_histogram
latency_histogram_version
ttft_sum_ms
ttft_count
ttft_histogram
ttft_histogram_version
```

`scope_type` 只允许：

- `global`
- `model`
- `provider`
- `endpoint`

四类 scope 分别生成记录，不产生维度笛卡尔组合。旧 Gateway 发送的逐请求
`RequestMetric` 继续被 Admin 接受并由 Admin 转成相同的内部聚合增量，以支持滚动升级。
新 Gateway 不发送用户、API Key、请求正文、响应正文、请求 ID或错误文本。

## Gateway 内存聚合与发送

每个完成请求最多更新四个独立 UTC 分钟桶。Gateway 在内存中先合并同一分钟、同一
scope 的增量，再通过有界队列发送聚合批次。

默认行为：

- 每 5 秒 flush；
- 达到批量阈值时提前 flush；
- 队列有固定容量；
- 请求路径仅执行内存更新和非阻塞通知；
- 队列满时不阻塞代理请求；
- 正常关闭时停止接收新记录、drain 队列、flush worker 当前批次；
- 关闭有总超时且可重复调用。

发送失败时采用有界指数退避。重试批次保持相同的
`instance_id + instance_epoch + sequence`。超过重试窗口或容量后丢弃，并累计：

- dropped batch count；
- dropped record count；
- publish failure count；
- last successful publish time；
- queue depth；
- shutdown flush failure。

这些状态随后通过成功批次或独立状态字段传给 Admin，用于完整性提示。有限重试不提供
持久化 spool；进程崩溃最多丢失尚未成功上报的内存数据。

## 批次幂等和 generation

每个 Gateway 进程拥有稳定的 `instance_id`。每次启动创建新的 `instance_epoch`，并在该
epoch 内使用单调递增 `sequence`。

Admin 的批次唯一键为：

```text
instance_id + instance_epoch + sequence
```

Admin 在一个数据库事务内：

1. 校验 generation 和事件时间；
2. 插入批次去重记录；
3. UPSERT 所有分钟聚合；
4. 更新采集状态；
5. 提交事务。

如果唯一键已经存在，Admin 直接返回成功，不再次累计。只有事务提交后才确认批次。
批次去重记录保留时间必须长于 Gateway 最大重试窗口。

`generation` 表示当前看板历史世代。Gateway worker 启动时先调用 Admin 的采集状态接口取得
当前 generation；握手成功前只在有界内存中聚合，不发送带猜测 generation 的批次。
重置历史时 Admin 原子推进 generation。旧 generation 的飞行中批次到达时不计入新历史，
Admin 返回当前 generation；Gateway 收到后丢弃旧队列并切换到新 generation。Admin
暂时不可达时继续遵守队列和重试上限，不能为了等待握手无限占用内存。

## SQLite 数据模型

指标与现有配置、资源和事件共用 `tokenlive.db`。新增以下逻辑表。

### dashboard_metric_minute

保存 UTC 分钟聚合。核心列：

```text
generation
bucket_time
scope_type
scope_id
scope_code
scope_name_snapshot
request_count
success_count
failure_count
input_tokens
output_tokens
cached_tokens
cache_creation_tokens
cost
latency_sum_ms
latency_count
latency_histogram
latency_histogram_version
ttft_sum_ms
ttft_count
ttft_histogram
ttft_histogram_version
created_at
updated_at
```

唯一键：

```text
generation + bucket_time + scope_type + scope_id
```

范围查询索引：

```text
generation + scope_type + scope_id + bucket_time
```

清理索引：

```text
generation + bucket_time
```

### dashboard_metric_day

字段与分钟表的指标列一致，桶键改为业务时区日期。唯一键：

```text
generation + bucket_date + scope_type + scope_id
```

每行同时记录生成该日桶时使用的业务时区。

### dashboard_ingest_batch

保存批次去重键、generation、接收时间和记录数。去重记录按配置周期清理，但保留时间必须
大于最大重试窗口。

### dashboard_collection_state

每个当前 generation 保存：

- generation；
- business timezone；
- collection started time；
- last received time；
- last persisted time；
- last rollup time；
- dropped batch/record count；
- invalid timestamp count；
- unknown scope count；
- 当前 freshness/completeness 状态。

### dashboard_data_gap

保存已知缺口的开始、结束、原因和影响记录数。缺口来源包括：

- Gateway 队列丢弃；
- 重试耗尽；
- SQLite 持续不可写；
- 时间戳越界；
- scope 基数保护触发。

趋势查询遇到已知缺口时返回缺口元数据，前端绘制断点而不是零值。

## 资源身份与基数保护

模型、供应商和端点同时保存稳定 ID/代码和当时名称快照。查询展示规则：

1. 资源仍存在时优先展示当前名称；
2. 资源已删除时回退到历史名称快照。

只有 Admin 当前配置可识别的稳定资源 ID 能生成独立 scope。未知、缺失或超出上限的值
归入每类固定的 `unknown` scope，并累计异常计数。全局 scope 始终写入，因此维度数据
异常不会丢失总量。

每类 scope 设置可配置的活跃键数量上限。上限用于保护数据库，不作为租户或用户级限流。
租户维度不在本设计内；未来增加时必须重新评估隐私和基数。

## 直方图与分位数

latency 和 TTFT 使用固定毫秒边界的累积或非累积直方图。边界和编码格式由
`histogram_version` 标识。相同版本可以直接合并；不同版本不能静默相加，查询层必须按
版本转换到共同边界或分别计算后合并近似结果。

直方图用于近似计算 P50/P95/P99，不承诺逐请求精确分位数。平均值使用
`sum/count` 计算。TTFT 缺失的请求不增加 TTFT count。

首期实现应通过基准测试在两种物理表示中选择一种：

- 固定 bucket 强类型列；
- 版本化紧凑二进制字段。

选择标准是跨 SQLite/MySQL 兼容性、UPSERT 成本、范围查询性能和迁移可读性，不能改变
上述协议语义。

## SQLite 运行参数与事务

Admin 使用现有 GORM 连接池，不额外打开第二个 SQLite pool。SQLite 模式启用：

- WAL journal mode；
- 合理的 busy timeout；
- foreign keys；
- 有界连接数；
- 短批量事务。

数据库路径与 SQLite URI 参数分开处理，不能把完整 `file:` URI 直接传给
`filepath.Dir`。Admin runtime options 接收绝对数据库文件路径，再由 gormx 构造 DSN。

每个上报 envelope 先在 Go 中按聚合键再次合并，然后在单事务中使用数据库中立的
UPSERT。Admin 公共模块继续支持 SQLite、MySQL 和 PostgreSQL，因此 DAL 不在查询中使用
`strftime`、`DATE_FORMAT` 等方言函数；时间边界在 Go 中计算。

## 时间、迟到数据和业务时区

分钟桶统一使用 UTC。Admin 按请求事件时间归桶，不按接收时间归桶。默认只接受：

```text
当前时间之前 24 小时 ～ 当前时间之后 5 分钟
```

越界记录不写入指标表，进入健康状态和缺口记录。范围应可配置，但必须有上限。

“今日”和日桶按显式业务时区切分。第一次产生指标前：

1. 读取配置的业务时区；
2. 配置缺失时使用系统时区；
3. 把最终时区写入 collection state 并固定。

一旦当前 generation 已有指标，业务时区不可直接修改。修改接口返回冲突，并提示先重置
看板历史。这样所有日桶始终使用同一日期边界。

## 日封账与保留策略

默认保留：

- 分钟桶 30 天；
- 日桶 1095 天，即 3 年。

当天统计直接查询分钟桶。每天按业务时区封账上一日：

1. 计算上一日对应的 UTC 起止时间；
2. 从分钟表重新聚合完整日数据；
3. 以幂等方式替换或 UPSERT 对应日桶；
4. 记录 rollup 状态。

任务启动时补算最近若干日，以覆盖停机和迟到数据。清理任务独立删除过期分钟、日桶和
批次去重记录。在线任务不自动执行 `VACUUM`，避免长时间锁库。

封账和清理失败记录日志及健康状态，但不阻塞指标接收或模型代理。

## Dashboard 数据源选择

看板按请求选择一个完整数据源，不按字段拼接：

```text
显式启用且健康的 Prometheus → 整套 Prometheus
否则                        → 整套 SQLite
SQLite 暂时异常              → 内存短时降级，并标记 incomplete
```

Redis 可继续服务运行时状态和旧部署兼容，但不与 SQLite 拼成同一组统计响应。切换到
Prometheus 时，overview、trends 和 ranking 使用同一来源。

熔断器当前状态继续保存在内存，由 Gateway 心跳刷新。进程重启后显示“等待状态同步”，
不把重启前的最后状态当成当前事实。熔断历史继续使用现有 `event_log`。

## Dashboard API 与 WebSocket

现有 overview、trends 和 model-ranking 响应字段保持兼容，并统一增加数据元信息：

```text
source
collection_started_at
last_persisted_at
fresh
complete
dropped_batches
dropped_records
gap_count
gaps
```

REST 与 WebSocket 必须调用同一个 Dashboard Service，不允许复制数据源选择或聚合逻辑。

新增受权限控制的接口：

- 查询采集健康状态；
- 读取和设置业务时区；
- 重置看板历史。

时区已有数据后修改返回 HTTP 409。重置必须要求明确确认参数，并只影响 Dashboard 指标
相关表和状态。

## 前端行为

首页沿用现有视觉体系并增加：

- 当前数据源；
- 采集开始时间；
- 最近成功落库时间；
- “采集刚开始”“等待 Gateway 状态同步”“数据存在缺口”的非阻塞提示；
- 趋势图已知缺口断点；
- 数据不完整时卡片仍展示可用值，但附带状态说明。

管理设置增加业务时区和“重置看板数据”：

- 当前 generation 已有数据时禁用时区编辑并说明原因；
- 重置操作二次确认；
- 文案明确不会删除配置、用户、API Key 或 `event_log`；
- 重置成功后刷新 generation 和采集起点。

## data-dir 与数据库路径

standalone 的 `-data-dir` 是 SQLite 和日志的唯一可变数据根目录：

```text
-data-dir=/var/lib/tokenlive
→ /var/lib/tokenlive/tokenlive.db
→ /var/lib/tokenlive/logs/...
```

该映射不依赖进程当前工作目录。standalone 将绝对数据库路径通过类型化
`adminapp.Options`/runtime override 传给 Admin，override 在 Admin 创建 GORM injector 前
应用。

不能依赖全局 `DB_DSN` 环境变量作为正式接口，因为 Admin 配置是进程级 singleton，环境
变量会污染同进程测试和嵌入场景。Homebrew 的 working directory 不再参与数据库定位。

## 配置

Admin 增加独立的 Dashboard metrics 配置组，至少包含：

```text
Enabled
BusinessTimezone
MinuteRetentionDays
DayRetentionDays
CleanupInterval
RollupInterval
IngestBatchRetention
MaxPastSkew
MaxFutureSkew
ActiveScopeLimit
FreshnessThreshold
```

Gateway 增加显式 status reporting 配置：

```text
Enabled
Transport              auto | redis | http | disabled
QueueSize
BatchSize
FlushInterval
RequestTimeout
ShutdownTimeout
RetryMaxElapsed
```

配置块缺失时必须有代码默认值。standalone 显式使用 HTTP transport；普通 Gateway 保持
`auto` 以兼容现有部署。HTTP client 复用 Admin 连接的 TLS 配置，不另建行为不一致的
client。

Homebrew 保留用户 active config 时，新字段仍通过代码默认值生效。新版完整默认配置写入
`config.yml.default`，文档说明差异比较方式。

## 数据重置

“重置看板数据”是独立危险操作。重置事务只切换逻辑世代，不在同一事务中删除大批历史行：

1. 锁定 collection state；
2. 推进 generation；
3. 重置新 generation 的健康计数；
4. 写入新的 `collection_started_at` 和固定业务时区；
5. 提交。

提交后，查询立即只读取新 generation。后台清理任务再分批删除旧 generation 的分钟、
日、批次去重和缺口记录，避免长事务锁库。

它不删除：

- 资源和策略配置；
- 用户和 API Key；
- Admin 审计数据；
- `event_log`；
- 应用日志。

旧 generation 的物理清理失败不会让旧数据重新可见；任务记录失败状态并在以后继续分批
重试。

## 错误处理与降级

### Gateway

- 队列满：不阻塞请求，记录丢弃并形成 gap。
- Admin 暂时不可达：有界指数退避。
- 重试耗尽：丢弃并记录 gap。
- 关闭超时：退出并记录最终 flush 失败。
- generation 不匹配：清理旧队列，切换新 generation。

### Admin

- 无效协议或 scope：返回 4xx，不写部分数据。
- generation 过旧：返回当前 generation，不累计。
- 重复批次：返回成功，不重复累计。
- SQLite busy：在 busy timeout 内等待，随后返回可重试错误。
- 磁盘满或持久化失败：返回 5xx、记录健康状态；不先更新内存并谎报成功。
- rollup/cleanup 失败：保留原数据，记录状态并在下次任务重试。
- Prometheus 不健康：完整回退 SQLite，不按字段混合。

### 前端

- 后端可返回部分有效数据时不整体报错；展示 incomplete 状态。
- 无任何可用来源时显示明确不可用状态，而不是全零。
- 缺口时间段不插入零值。

## 迁移与兼容

- 新版本只创建 schema，不解析旧 JSON 日志，不回填旧历史。
- collection state 记录新数据开始时间，首页明确展示历史起点。
- Admin 继续接受旧 Gateway 的现有逐请求 payload。
- Gateway 新字段均为 additive，旧 Admin 可忽略未知字段；可靠重试只在确认 Admin 支持
  对应 schema/version 后启用。
- 自动迁移启用时由 Dashboard module 管理新表；`scripts/init.sql` 同步维护完整 DDL。
- 迁移、索引创建和任务启动必须发生在 HTTP Server 接收指标之前。

## 发布与依赖确定性

跨仓发布顺序：

1. Admin 完成 schema、Store、查询、API、前端和兼容接收；
2. Gateway 完成最终结果、协议、重试、幂等标识和生命周期；
3. 两仓分别通过测试后发布明确版本；
4. standalone 升级 `go.mod` 中的两个版本；
5. standalone 完成 all-in-one 重启持久化测试后发布。

standalone 打包默认严格使用 `go.mod` 版本。本地兄弟仓源码只在显式开发开关下通过
`replace` 使用。打包日志打印依赖版本、commit 和 dirty 状态；正式发布遇到 dirty 或版本
不匹配时失败，避免普通构建与本地包行为不同。

本设计不授权自动 commit、tag、push 或发布。

## 测试策略

### tokenlive-gateway

- 最终 outcome：400/401/403/429/503 和流式中断分类正确；
- metrics、access log 和 event 使用同一 success/status；
- TTFT 只在首次有效流式内容时记录；
- DTO 向后兼容；
- 四类 scope 分钟预聚合正确；
- batch 阈值、定时 flush、有限重试和幂等 sequence；
- 队列满不阻塞且增加丢弃计数；
- `Close` drain、超时和重复调用安全；
- enqueue 与 Close 并发无竞态；
- 使用 `go test -race` 覆盖批处理生命周期。

### tokenlive-admin

- schema、唯一索引和 AutoMigrate 幂等；
- 重复 batch 不重复累计；
- 单批次去重和聚合原子提交；
- 事件时间边界；
- unknown scope 和活跃键上限；
- latency/TTFT 直方图合并和分位数；
- overview、1h/6h/24h/7d trends 和 ranking；
- 当前资源名称与历史快照回退；
- 数据库关闭重开后恢复；
- 日封账补跑和幂等；
- 30 天/3 年 retention；
- 时区锁定与 HTTP 409；
- generation 重置及旧批次隔离；
- gap/freshness/completeness 元数据；
- Prometheus/SQLite 整源切换；
- SQLite busy、磁盘写失败和任务失败降级；
- 前端状态提示、趋势断点、时区禁用和重置确认。

### tokenlive-standalone

- 任意 cwd 下 `-data-dir` 仍确定数据库位置；
- 临时 data-dir 启动 all-in-one；
- 上报指标并查询首页；
- 关闭并用同一 DB 重启；
- overview、trends 和 ranking 保持；
- 正常关闭刷新最后批次；
- 旧 active config 缺少新字段时使用安全默认值；
- 打包使用 `go.mod` 版本，只有显式开关使用本地依赖；
- release workflow 在发布前执行兼容、迁移和重启持久化门禁。

## 性能与支持边界

首期承诺目标：

- 峰值约 100 RPS；
- 数百个活跃模型、供应商和端点；
- 分钟数据 30 天；
- 日数据 3 年；
- 单个 SQLite 文件。

基准测试至少记录：

- 开启指标前后的代理请求延迟差异；
- 100 RPS 下内存聚合和 flush 耗时；
- 队列深度、重试和丢弃；
- SQLite 事务时间和 busy 等待；
- Admin CRUD 延迟；
- 30 天与 3 年数据规模估算；
- Dashboard 常用范围查询耗时；
- 日封账和 retention 任务耗时。

如果实际规模长期超过该边界，应显式切换到 Prometheus/ClickHouse 等外部方案，而不是在
默认单机 SQLite 中增加请求明细或无限维度。

## 实施顺序

虽然作为一次完整方案交付，代码仍按可验证依赖顺序推进：

1. Gateway 修正最终结果语义和 TTFT；
2. 定义并测试向后兼容的协议；
3. Admin 建立 schema、迁移、聚合 Store 和幂等接收；
4. Gateway 增加分钟预聚合、重试、generation 和 shutdown drain；
5. Admin 完成 Dashboard Service、整源切换和查询；
6. Admin 完成日封账、retention、健康状态、时区和重置；
7. Admin 前端增加状态、缺口、时区和重置交互；
8. standalone 修复 data-dir、升级依赖并增加 E2E；
9. 统一打包依赖来源和 release 门禁；
10. 执行单元、集成、竞态、重启和性能验证。

每一步都应保持对应仓库可测试，不通过中间状态宣称完整功能可用。
