package assemble

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/spf13/viper"
	"github.com/tokenlive/tokenlive-admin/adminapp"
	"github.com/tokenlive/tokenlive-gateway/pkg/config"
	"github.com/tokenlive/tokenlive-gateway/pkg/gateway"
	"github.com/tokenlive/tokenlive-gateway/pkg/log"
)

// Options configures the all-in-one process.
type Options struct {
	// GatewayConf is the viper tree for tokenlive-gateway (YAML).
	GatewayConf *viper.Viper
	Logger      *log.Logger

	// Admin embed options (TOML workdir/configs under admin tree).
	AdminWorkDir   string
	AdminConfigs   string
	AdminStaticDir string

	// HTTP listen (overrides gateway http.* if set).
	Host string
	Port int
}

// App is a running all-in-one instance (not listening until ListenAndServe).
type App struct {
	Engine  *gin.Engine
	Gateway *gateway.Gateway
	Admin   *adminapp.App

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

// New builds Gin + Admin + Gateway. Does not listen.
// OnConfigChanged currently purges API key cache; full ConfigHub reload is TODO.
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
		opt.Port = 8000
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

	// Admin first so /api/v1 is registered; gateway owns /v1.
	adminApp, err := adminapp.New(ctx, adminapp.Options{
		WorkDir:   opt.AdminWorkDir,
		Configs:   opt.AdminConfigs,
		StaticDir: opt.AdminStaticDir,
		Engine:    r,
		OnConfigChanged: func(ctx context.Context, kind string, keys ...string) {
			// Placeholder until ConfigHub lands: purge API key cache on apikey changes.
			if app.Gateway == nil {
				return
			}
			switch kind {
			case "apikeys", "all":
				app.Gateway.PurgeAPIKeyCache()
			}
		},
	})
	if err != nil {
		return nil, fmt.Errorf("assemble: admin: %w", err)
	}
	app.Admin = adminApp

	// Provider: for now use gateway default from conf; Embedded provider replaces this in confighub work.
	// config_source=embedded is validated but ProvideGatewayProvider does not yet know "embedded" —
	// inject a no-op redis provider so New succeeds; real embedded store is next task.
	provider := config.NewRedisGatewayProviderWithAPIKeyPepper(nil, opt.GatewayConf.GetString("llm.api_key_pepper"))

	gw, cleanup, err := gateway.New(opt.GatewayConf, opt.Logger, &gateway.Options{
		Provider:       provider,
		SkipClickHouse: true,
	})
	if err != nil {
		_ = adminApp.Shutdown(ctx)
		return nil, fmt.Errorf("assemble: gateway: %w", err)
	}
	app.Gateway = gw
	app.gwCleanup = cleanup
	gw.RegisterGin(r)

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
