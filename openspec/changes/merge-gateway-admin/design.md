## Context

TokenLive **两种部署形态**：

1. **分部署（主线）**：`tokenlive-gateway` 与 `tokenlive-admin` 独立进程；Gateway 使用 `local|redis|http`。
2. **All-in-one（附加）**：新仓 **`tokenlive-standalone`** 单进程同时运行 Gateway + Admin；`embedded`；无半开。

本设计将实现重心放在 **组装仓**，主线两仓只做 **可嵌入库边界**，避免 Admin 迁入 Gateway 或反向污染。

约束：

- 主线两仓行为与发布节奏独立；standalone 通过 module 版本组合它们。
- All-in-one 必须同时启用 Gateway 与 Admin。
- Engine Pipeline 不在 standalone 重写，只调用 Gateway 库。

## Goals / Non-Goals

**Goals:**

- 新建 `tokenlive-standalone`：合并层 + all-in-one main + 配置 + brew。
- Gateway / Admin 提供稳定 embed API，供 standalone 的 `main` 组装。
- 分部署零破坏；CI 主线与 standalone 分轨。
- Homebrew 只构建/安装 standalone。

**Non-Goals:**

- 不把三仓收成 monorepo（本阶段）。
- 不在 Gateway 仓内 copy Admin 源码。
- 不在 standalone 内 fork 业务逻辑（CRUD/策略应留在 admin 库）。
- All-in-one 多实例扩展（请用分部署 + Redis）。

## Deployment Matrix

| 形态 | 制品 | 进程 | config |
|------|------|------|--------|
| 分部署 | `tokenlive-gateway`、`tokenlive-admin` | 1～2 个进程 | Gateway: local/redis/http；Admin: 既有 TOML |
| All-in-one | **`tokenlive-standalone` → 二进制 `tokenlive`** | 恰好 1 个进程 = GW+Admin | `deploy_mode` 隐含 all-in-one；`config_source=embedded` |

## Repository Layout（目标）

```
tokenlive-gateway/          # 主线：代理 + Engine（可被 import）
tokenlive-admin/            # 主线：控制台（可被 import）
tokenlive-standalone/       # 附加：组装 + 发行
  cmd/tokenlive/main.go
  internal/assemble/        # 单 Gin、生命周期、模式校验
  internal/confighub/       # embedded 快照与热更新
  internal/bridge/          # admin 写路径 → ConfigHub → gateway Engine
  config/all-in-one.example.yml
  Formula/ 或 docs/brew.md
  go.mod  # require gateway@vX, admin@vY
```

依赖方向（禁止反向）：

```
tokenlive-standalone  -->  tokenlive-gateway
tokenlive-standalone  -->  tokenlive-admin
tokenlive-gateway     -x->  tokenlive-admin / standalone
tokenlive-admin       -x->  tokenlive-gateway / standalone   # 分部署下 admin 经 Redis/HTTP 耦合，非 Go import
```

若 admin 与 gateway 出现循环类型依赖，bridge 只依赖双方 **接口/DTO**，DTO 放在更中立的小模块或 duplicate 最小结构体于 standalone。

## Decisions

### D1: 新仓 `tokenlive-standalone` 为 All-in-one 唯一实现主体

- **选择**：合并逻辑、embedded、brew 均在 standalone；不在 gateway 仓做 all-in-one main。
- **理由**：主线干净；组装可独立发版；符合「新开组装仓」决策。
- **备选**：迁入 gateway — 已否决。

### D2: 二元形态，All-in-one 原子

- **选择**：standalone 进程 = Gateway 面 + Admin 面必须同时启动；无 admin-off。
- **校验**：启动时 `config_source` 必须为 `embedded`；否则 fail-fast。
- **分部署**：用户继续跑两个主线二进制，与 standalone 无关。

### D3: Library embed surface（主线小改）

详见同目录 **`embed-api.md`**（签名与版本钉扎）。

**Gateway（已落地初版）：**

- 包 `pkg/gateway`：`New` / `RegisterGin` / `UpdateEngineConfig` / `PurgeAPIKeyCache`。
- 组装逻辑下沉 `internal/bootstrap`；`cmd/server/wire` 薄委托。
- `Options.Provider` 注入 Embedded（实现在 standalone）。

**Admin 应导出（待做）：**

- 包 `adminapp`：`New` / `Register` / `Shutdown`；挂 `/api/v1`、中间件、Casbin。
- DB Open + AutoMigrate + 默认管理员 seed。
- SPA：StaticDir 或后续 `embed.FS`。
- **写路径钩子**：`OnConfigChanged(kind, keys...)`，admin 不 import gateway。

### D4: Embedded 与 ConfigHub 放在 standalone

- **选择**：`EmbeddedGatewayProvider` + `ConfigHub` 实现位于 `tokenlive-standalone/internal/confighub`。
- Admin 变更 → bridge 读 admin 库或调用 admin 只读服务 → 更新 Hub → `engine.UpdateConfig` + 清缓存。
- **理由**：热更新是组装关注点，不是分部署 Admin 的职责；分部署仍用 Redis/HTTP。

### D5: 配置

- **standalone**：单一 YAML（从 gateway 风格扩展 `admin:` 段：DSN、JWT、data_dir 等）。
- **分部署 Admin**：继续 TOML，不强制迁移。
- **默认数据**：SQLite 路径 `{data_dir}/tokenlive.db`；state memory；Redis/CH 可选。

### D6: 版本与发布

- standalone `go.mod` 使用语义化版本钉住两库。
- 发布流程：先发 gateway/admin tag → 升 standalone 依赖 → 打 standalone release → 更新 Homebrew bottle。
- 兼容策略：standalone 声明支持的 gateway/admin 版本范围。

### D7: Homebrew

- Formula 只构建 `tokenlive-standalone`。
- 配置 / data 目录在 Homebrew prefix 约定路径。
- 文档明确：生产分部署 ≠ brew 路径。

### D8: 本 OpenSpec 变更落点

- 变更目录目前在 **gateway 仓** `openspec/changes/merge-gateway-admin/`，作为 **跨仓设计记录**。
- 实施时：gateway/admin 的 library PR 在各自仓；standalone **新建仓**后可将本 change 归档说明「实现主体已迁至 tokenlive-standalone」，或在新仓复制 openspec。

## Risks / Trade-offs

- **[两库尚不可 embed]** → 先做 library 出口 PR，再写 standalone main；tasks 分阶段。
- **[Go module 私有/替换]** → 开发期可用 `replace` 指本地路径；发布去掉 replace。
- **[admin 回调与 gateway 类型]** → 用 standalone bridge + 窄接口，禁止 admin→gateway import。
- **[版本组合爆炸]** → CI 矩阵测「当前钉死的一对版本」；不承诺任意交叉组合。
- **[SPA 构建]** → standalone 构建脚本调用 admin frontend 或消费 admin release 附带的 dist artifact。

## Migration Plan

1. Gateway：导出 embed API，保持 `cmd/server` 行为不变。
2. Admin：导出 embed API + change hook，保持 `main` 分部署不变。
3. 创建 `tokenlive-standalone`：assemble + confighub + example config。
4. 联调 all-in-one；补 fail-fast 校验。
5. Homebrew Formula + 文档。
6. 分部署用户：无动作。

## Open Questions

1. 新仓 GitHub 路径是否固定为 `github.com/tokenlive/tokenlive-standalone`？
2. Admin frontend dist：submodule 构建 vs 发布物下载？
3. Gateway embed 是抽 `pkg/embed` 还是整理现有 `cmd/server/wire` 为可调用包？
4. 是否需要 gateway-only 小二进制继续从 gateway 仓发布（与 standalone 并行）？— 是，分部署仍用 gateway 仓制品。
