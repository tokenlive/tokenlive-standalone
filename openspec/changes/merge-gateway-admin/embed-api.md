# Embed API 契约（Gateway + Admin）

供 `tokenlive-standalone` 组装 all-in-one。主线 `cmd` 行为不变。

## 1. 版本钉扎

| 组件 | Module | 策略 |
|------|--------|------|
| 本仓 | `github.com/tokenlive/tokenlive-standalone` | 独立 semver |
| Gateway | `github.com/tokenlive/tokenlive-gateway` | `go.mod` **require 精确 tag**（如 `v0.x.y`） |
| Admin | `github.com/tokenlive/tokenlive-admin` | 同上 |

- 开发期可用 `replace => ../tokenlive-gateway` / `../tokenlive-admin`。
- 发布 bottle / release 前必须去掉 replace，只保留 tag。
- 不承诺任意 gateway×admin 交叉组合；CI 只验证当前钉死的一对版本。

## 2. 二进制与仓库

| 项 | 值 |
|----|-----|
| 组装仓 | `github.com/tokenlive/tokenlive-standalone` |
| All-in-one 二进制 | `tokenlive` |
| 分部署 Gateway | `tokenlive-gateway`（主线仓） |
| 分部署 Admin | `tokenlive-admin`（主线仓） |

## 3. Gateway embed API

**包**：`github.com/tokenlive/tokenlive-gateway/pkg/gateway`

```go
// 构建（不 listen）
gw, cleanup, err := gateway.New(conf *viper.Viper, logger *log.Logger, opts *gateway.Options)

type Options struct {
    Provider       config.GatewayProvider // 可注入 Embedded 实现
    Redis          *redis.Client          // 可选共享
    ClickHouse     clickhouse.Conn
    SkipClickHouse bool
}

// 挂到宿主 Gin
gw.RegisterGin(r *gin.Engine)  // 注册 /v1/* 与 /v1beta/*

// 热更新
gw.UpdateEngineConfig(cfg *core.EngineConfig) error
gw.PurgeAPIKeyCache()
gw.UpdateYAMLConfig(gwCfg *config.GatewayConfig)

// 只读访问
gw.Engine   *core.Engine
gw.Provider config.GatewayProvider
gw.Config   *config.ConfigManager
```

**状态（gateway 仓）**：`pkg/gateway` + `internal/bootstrap` 已落地；`cmd/server/wire` 委托 bootstrap。

**Embedded provider**：实现放在 **standalone**（`internal/confighub`），满足 `config.GatewayProvider`，经 `Options.Provider` 注入。Gateway 不依赖 standalone。

## 4. Admin embed API（目标签名）

**包（规划）**：`github.com/tokenlive/tokenlive-admin/adminapp`

```go
type Options struct {
    WorkDir   string
    Configs   string   // 相对 WorkDir，如 "dev"
    StaticDir string   // SPA；空则不挂前端

    Engine *gin.Engine // 非 nil：只 Register，不 Listen

    // 配置影响 Gateway 运行时的变更通知（admin 不 import gateway）
    OnConfigChanged func(ctx context.Context, kind string, keys ...string)
    // kind: "endpoints" | "policies" | "apikeys" | "all"
}

func New(ctx context.Context, opt Options) (*App, error)
func (a *App) Register(ctx context.Context, e *gin.Engine) error
func (a *App) Handler() http.Handler
func (a *App) Start(ctx context.Context) error   // CLI 兼容：自 listen
func (a *App) Shutdown(ctx context.Context) error
```

**状态**：尚未实现；见 tasks §3。

## 5. Standalone 组装伪代码

```go
r := gin.New()
// admin first or gateway first — 路径不冲突：/api vs /v1
adminApp, _ := adminapp.New(ctx, adminapp.Options{
    Engine: r,
    StaticDir: "...",
    OnConfigChanged: bridge.OnAdminConfigChanged,
})
_ = adminApp.Register(ctx, r)

hub := confighub.New(...)
provider := hub.AsGatewayProvider()
gw, cleanup, _ := gateway.New(viperYAML, logger, &gateway.Options{
    Provider: provider,
    SkipClickHouse: true, // brew 默认
})
defer cleanup()
gw.RegisterGin(r)

r.GET("/health", ...)
http.ListenAndServe(addr, r)
```

## 6. 依赖方向

```
tokenlive-standalone → tokenlive-gateway
tokenlive-standalone → tokenlive-admin
tokenlive-gateway    ↛ admin / standalone
tokenlive-admin      ↛ gateway / standalone
```
