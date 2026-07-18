package assemble

import (
	"context"
	"fmt"
	"net/http"
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

	r := gin.New()
	r.Use(gin.Recovery())
	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok", "mode": "all-in-one"})
	})

	app := &App{Engine: r, host: opt.Host, port: opt.Port}

	// 1) Admin (DB + routes). Callback wired after hub exists.
	var hub *confighub.Hub
	adminApp, err := adminapp.New(ctx, adminapp.Options{
		WorkDir:   opt.AdminWorkDir,
		Configs:   opt.AdminConfigs,
		StaticDir: opt.AdminStaticDir,
		Engine:    r,
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
