package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/tokenlive/tokenlive-gateway/pkg/config"
	"github.com/tokenlive/tokenlive-gateway/pkg/log"
	"github.com/tokenlive/tokenlive-standalone/internal/assemble"
)

func main() {
	confPath := flag.String("conf", "config/all-in-one.example.yml", "gateway-side YAML config")
	adminWorkDir := flag.String("admin-workdir", "", "admin configs workdir (default: env TOKENLIVE_ADMIN_WORKDIR or ../tokenlive-admin/configs)")
	adminConfigs := flag.String("admin-config", "dev", "admin config name under workdir")
	adminStatic := flag.String("admin-static", "", "admin SPA static dir (optional)")
	flag.Parse()

	v := config.NewConfig(*confPath)
	if err := assemble.ValidateAllInOne(v); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}

	workDir := *adminWorkDir
	if workDir == "" {
		workDir = os.Getenv("TOKENLIVE_ADMIN_WORKDIR")
	}
	if workDir == "" {
		// Dev default: sibling checkout
		workDir = "../tokenlive-admin/configs"
	}

	logger := log.NewLog(v)
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	app, err := assemble.New(ctx, assemble.Options{
		GatewayConf:    v,
		Logger:         logger,
		AdminWorkDir:   workDir,
		AdminConfigs:   *adminConfigs,
		AdminStaticDir: *adminStatic,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "assemble failed: %v\n", err)
		os.Exit(1)
	}
	defer app.Close(context.Background())

	fmt.Fprintf(os.Stderr, "tokenlive all-in-one listening (health on /health)\n")
	if err := app.ListenAndServe(ctx); err != nil && err != context.Canceled {
		fmt.Fprintf(os.Stderr, "server error: %v\n", err)
		os.Exit(1)
	}
}
