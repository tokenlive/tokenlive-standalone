package assemble

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/spf13/viper"
	"github.com/tokenlive/tokenlive-admin/adminapp"
	"github.com/tokenlive/tokenlive-gateway/pkg/gateway"
	"github.com/tokenlive/tokenlive-gateway/pkg/log"
	"github.com/tokenlive/tokenlive-standalone/internal/bridge"
	"github.com/tokenlive/tokenlive-standalone/internal/confighub"
)

// Options configures the all-in-one process.
type Options struct {
	GatewayConf *viper.Viper
	Logger      *log.Logger

	AdminWorkDir   string
	AdminConfigs   string
	AdminStaticDir string

	Host string
	Port int
}

// App is a running all-in-one instance (not listening until ListenAndServe).
type App struct {
	Engine  *gin.Engine
	Gateway *gateway.Gateway
	Admin   *adminapp.App
	Hub     *confighub.Hub

	host string
	port int

	gwCleanup func()
}

// ValidateAllInOne fails fast if config is not a legal all-in-one setup.
func ValidateAllInOne(v *viper.Viper) error {
	if v == nil {
		return fmt.Errorf("assemble: config is nil")
	}
	src := v.GetString("gateway.config_source")
	if src != "embedded" {
		return fmt.Errorf("assemble: all-in-one requires gateway.config_source=embedded, got %q", src)
	}
	return nil
}

// New builds Gin + Admin + ConfigHub + Gateway. Does not listen.
func New(ctx context.Context, opt Options) (*App, error) {
	if opt.GatewayConf == nil {
		return nil, fmt.Errorf("assemble: GatewayConf is nil")
	}
	if opt.Logger == nil {
		return nil, fmt.Errorf("assemble: Logger is nil")
	}
	if err := ValidateAllInOne(opt.GatewayConf); err != nil {
		return nil, err
	}

	if opt.Host == "" {
		opt.Host = opt.GatewayConf.GetString("http.host")
	}
	if opt.Port == 0 {
		opt.Port = opt.GatewayConf.GetInt("http.port")
	}
	if opt.Port == 0 {
		opt.Port = 2525
	}
	if opt.Host == "" {
		opt.Host = "127.0.0.1"
	}
	if opt.AdminWorkDir == "" {
		opt.AdminWorkDir = "configs"
	}
	if opt.AdminConfigs == "" {
		opt.AdminConfigs = "dev"
	}
	if opt.AdminStaticDir == "" {
		opt.AdminStaticDir = DetectAdminStaticDir()
	}

	r := gin.New()
	r.Use(gin.Recovery())
	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok", "mode": "all-in-one"})
	})

	// Friendly root when SPA is not mounted (static middleware owns "/" when present).
	if opt.AdminStaticDir == "" {
		r.GET("/", func(c *gin.Context) {
			c.Header("Content-Type", "text/html; charset=utf-8")
			c.String(http.StatusOK, noSPAHTML)
		})
	}

	app := &App{Engine: r, host: opt.Host, port: opt.Port}

	// 1) Admin (DB + routes). Callback wired after hub exists.
	// When SPA is present, keep NoRoute so static middleware can serve index.html for "/".
	var keepNoRoute *bool
	if opt.AdminStaticDir != "" {
		v := false
		keepNoRoute = &v // DisableNoRoute=false
	}
	var hub *confighub.Hub
	adminApp, err := adminapp.New(ctx, adminapp.Options{
		WorkDir:        opt.AdminWorkDir,
		Configs:        opt.AdminConfigs,
		StaticDir:      opt.AdminStaticDir,
		Engine:         r,
		DisableNoRoute: keepNoRoute,
		OnConfigChanged: func(ctx context.Context, kind string, keys ...string) {
			if hub == nil || app.Gateway == nil {
				return
			}
			if err := hub.Refresh(ctx, kind); err != nil {
				return
			}
		},
	})
	if err != nil {
		return nil, fmt.Errorf("assemble: admin: %w", err)
	}
	app.Admin = adminApp

	// 2) ConfigHub from admin DB snapshot
	hub = confighub.New(&bridge.AdminSnapshotSource{Admin: adminApp})
	app.Hub = hub

	// Initial load (empty DB is ok — empty config/keys)
	if err := hub.Refresh(ctx, "all"); err != nil {
		// Soft-fail: keep YAML seed models so Engine can start; host can retry after seeding admin.
		opt.Logger.Logger.Sugar().Warnf("confighub initial refresh: %v (using YAML seed until admin has data)", err)
	}

	hub.OnReload = func(ctx context.Context, kind string) {
		if app.Gateway == nil {
			return
		}
		switch kind {
		case "endpoints", "all":
			if cfg := hub.GatewayConfig(); cfg != nil && len(cfg.Models) > 0 {
				_ = app.Gateway.ApplyGatewayConfig(cfg)
			}
			app.Gateway.PurgeAPIKeyCache()
			app.Gateway.PurgePolicyCache()
		case "policies":
			app.Gateway.PurgePolicyCache()
		case "apikeys":
			app.Gateway.PurgeAPIKeyCache()
		default:
			app.Gateway.PurgeAPIKeyCache()
			app.Gateway.PurgePolicyCache()
		}
	}

	// 3) Gateway with embedded provider
	gw, cleanup, err := gateway.New(opt.GatewayConf, opt.Logger, &gateway.Options{
		Provider:       hub.Provider(),
		SkipClickHouse: true,
	})
	if err != nil {
		_ = adminApp.Shutdown(ctx)
		return nil, fmt.Errorf("assemble: gateway: %w", err)
	}
	app.Gateway = gw
	app.gwCleanup = cleanup
	gw.RegisterGin(r)

	// Apply admin snapshot to engine if we have models
	if cfg := hub.GatewayConfig(); cfg != nil && len(cfg.Models) > 0 {
		if err := gw.ApplyGatewayConfig(cfg); err != nil {
			opt.Logger.Logger.Sugar().Warnf("apply embedded config: %v", err)
		}
	}

	return app, nil
}

// ListenAndServe blocks until ctx is done, then shuts down gracefully.
func (a *App) ListenAndServe(ctx context.Context) error {
	if a == nil || a.Engine == nil {
		return fmt.Errorf("assemble: app not initialized")
	}
	addr := fmt.Sprintf("%s:%d", a.host, a.port)
	srv := &http.Server{Addr: addr, Handler: a.Engine}

	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.ListenAndServe()
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
		a.Close(shutdownCtx)
		return ctx.Err()
	case err := <-errCh:
		a.Close(context.Background())
		if err == http.ErrServerClosed {
			return nil
		}
		return err
	}
}

// Close releases admin and gateway resources.
func (a *App) Close(ctx context.Context) {
	if a == nil {
		return
	}
	if a.Admin != nil {
		_ = a.Admin.Shutdown(ctx)
	}
	if a.gwCleanup != nil {
		a.gwCleanup()
		a.gwCleanup = nil
	}
}

// DetectAdminStaticDir finds a built admin SPA directory.
// Order: TOKENLIVE_ADMIN_STATIC, ./web/dist, ../tokenlive-admin/frontend/dist.
func DetectAdminStaticDir() string {
	if p := os.Getenv("TOKENLIVE_ADMIN_STATIC"); p != "" {
		if spaDirOK(p) {
			return p
		}
	}
	candidates := []string{
		"web/dist",
		"frontend/dist",
		filepath.Join("..", "tokenlive-admin", "frontend", "dist"),
	}
	for _, p := range candidates {
		if spaDirOK(p) {
			abs, err := filepath.Abs(p)
			if err == nil {
				return abs
			}
			return p
		}
	}
	return ""
}

func spaDirOK(dir string) bool {
	st, err := os.Stat(filepath.Join(dir, "index.html"))
	return err == nil && !st.IsDir()
}

const noSPAHTML = `<!DOCTYPE html>
<html lang="zh-CN">
<head>
  <meta charset="utf-8"/>
  <title>TokenLive</title>
  <style>
    body{font-family:system-ui,sans-serif;max-width:40rem;margin:3rem auto;padding:0 1rem;line-height:1.5;color:#1a1a1a}
    code{background:#f4f4f5;padding:.1rem .35rem;border-radius:4px}
    a{color:#1677ff}
  </style>
</head>
<body>
  <h1>TokenLive All-in-one</h1>
  <p>服务已启动，但<strong>未挂载管理控制台前端</strong>，所以打开 <code>/</code> 会看不到页面。</p>
  <p>健康检查：<a href="/health">/health</a> · API 前缀：<code>/api/v1</code> · LLM：<code>/v1</code></p>
  <h2>启用控制台 UI</h2>
  <ol>
    <li>构建前端：
      <pre>cd ../tokenlive-admin/frontend &amp;&amp; npm ci &amp;&amp; npm run build</pre>
    </li>
    <li>重启（自动探测 <code>../tokenlive-admin/frontend/dist</code>），或显式指定：
      <pre>./bin/tokenlive -admin-static ../tokenlive-admin/frontend/dist</pre>
    </li>
  </ol>
  <p>默认管理员（验证码已关）：<code>admin</code> / <code>admin</code></p>
</body>
</html>
`
