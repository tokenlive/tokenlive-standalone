package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/tokenlive/tokenlive-gateway/pkg/config"
	"github.com/tokenlive/tokenlive-gateway/pkg/log"
	"github.com/tokenlive/tokenlive-standalone/internal/assemble"
)

// Set via -ldflags "-X main.version=..."
var version = "dev"

func main() {
	showVersion := flag.Bool("version", false, "print version and exit")
	confPath := flag.String("conf", "config/all-in-one.example.yml", "gateway-side YAML config")
	adminWorkDir := flag.String("admin-workdir", "", "admin configs workdir (default: ./configs/admin)")
	// When using bundled configs/admin, leave empty (load all toml in workdir).
	adminConfigs := flag.String("admin-config", "", "admin config subset under workdir (empty = all files in workdir)")
	adminStatic := flag.String("admin-static", "", "admin SPA static dir (optional)")
	dataDir := flag.String("data-dir", "data", "mutable data directory (sqlite, logs)")
	flag.Parse()

	if *showVersion {
		fmt.Println(version)
		return
	}

	v := config.NewConfig(*confPath)
	if err := assemble.ValidateAllInOne(v); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}

	if err := os.MkdirAll(*dataDir, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "data-dir: %v\n", err)
		os.Exit(1)
	}

	// Force SQLite for all-in-one so ambient DB_TYPE=mysql / path-like DB_DSN cannot break startup.
	dbPath := filepath.Join(*dataDir, "tokenlive.db")
	_ = os.Setenv("DB_TYPE", "sqlite3")
	_ = os.Setenv("DB_DSN", dbPath)

	workDir := *adminWorkDir
	if workDir == "" {
		workDir = os.Getenv("TOKENLIVE_ADMIN_WORKDIR")
	}
	if workDir == "" {
		workDir = "configs/admin"
	}

	adminCfg := *adminConfigs
	// Bundled layout: configs/admin/conf/*.toml + menu.json at workdir root.
	// Sibling tokenlive-admin: workdir=.../configs, subset=dev.
	if adminCfg == "" {
		switch {
		case dirExists(filepath.Join(workDir, "conf")):
			adminCfg = "conf"
		case dirExists(filepath.Join(workDir, "dev")):
			adminCfg = "dev"
		default:
			adminCfg = "conf"
		}
	}

	logger := log.NewLog(v)
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	host := v.GetString("http.host")
	if host == "" {
		host = "127.0.0.1"
	}
	port := v.GetInt("http.port")
	if port == 0 {
		port = 2525
	}

	staticDir := *adminStatic
	if staticDir == "" {
		staticDir = assemble.DetectAdminStaticDir()
	}

	app, err := assemble.New(ctx, assemble.Options{
		GatewayConf:    v,
		Logger:         logger,
		AdminWorkDir:   workDir,
		AdminConfigs:   adminCfg,
		AdminStaticDir: staticDir,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "assemble failed: %v\n", err)
		os.Exit(1)
	}
	defer app.Close(context.Background())

	if staticDir != "" {
		fmt.Fprintf(os.Stderr, "tokenlive all-in-one listening on http://%s:%d (console SPA: %s)\n", host, port, staticDir)
	} else {
		fmt.Fprintf(os.Stderr, "tokenlive all-in-one listening on http://%s:%d (no SPA — open / for setup hints, or pass -admin-static)\n", host, port)
	}
	if err := app.ListenAndServe(ctx); err != nil && err != context.Canceled {
		fmt.Fprintf(os.Stderr, "server error: %v\n", err)
		os.Exit(1)
	}
}

func dirExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}
