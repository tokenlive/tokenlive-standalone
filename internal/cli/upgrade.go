package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/tokenlive/tokenlive-admin/pkg/productversion"
	"github.com/tokenlive/tokenlive-standalone/internal/upgrade"
)

// upgradePaths derives the probe and upgrades root from build-time defaults.
// It loads no business configuration, initializes no database and opens no
// HTTP listener, so the CLI stays usable when the service is down.
func upgradePaths(info Info) (*upgrade.Installation, string, []string) {
	home, _ := os.UserHomeDir()
	prober := upgrade.NewCLIProber()
	prober.Executable = executableOrBust()
	prober.UserHome = home
	install, reasons := prober.Probe(0, productversion.Build{Version: info.Version, Kind: info.BuildKind})
	if install == nil {
		return nil, "", reasons
	}
	dataDir := info.DefaultDataDir
	if dataDir == "" {
		dataDir = "data"
	}
	root := filepath.Join(filepath.Dir(dataDir), "upgrades", upgrade.InstallIDFor(install))
	return install, root, nil
}

func executableOrBust() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	return exe
}

// RunUpgradeStatus reconciles and prints persisted upgrade task state.
func RunUpgradeStatus(info Info, args []string) int {
	fs := flag.NewFlagSet("upgrade-status", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "print machine-readable JSON")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	install, root, reasons := upgradePaths(info)
	if install == nil {
		out := map[string]interface{}{"supported": false, "reasons": reasons}
		return printStatus(out, *asJSON)
	}
	manager := upgrade.NewManager(root, install, upgrade.ExecRunner{}, executableOrBust())
	if err := manager.Reconcile(); err != nil {
		fmt.Fprintln(os.Stderr, "reconcile failed:", err)
		return 1
	}
	snapshot, err := manager.Snapshot()
	if err != nil {
		fmt.Fprintln(os.Stderr, "read tasks failed:", err)
		return 1
	}
	out := map[string]interface{}{
		"supported": true,
		"prefix":    install.Prefix,
		"keg":       install.Keg,
		"domain":    install.Domain,
		"label":     install.ServiceLabel,
		"tasks":     snapshot,
	}
	return printStatus(out, *asJSON)
}

func printStatus(out map[string]interface{}, asJSON bool) int {
	if asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(out); err != nil {
			return 1
		}
		return 0
	}
	if supported, _ := out["supported"].(bool); !supported {
		fmt.Println("click upgrade: not supported")
		if reasons, ok := out["reasons"].([]string); ok {
			for _, r := range reasons {
				fmt.Println("  reason:", r)
			}
		}
		return 0
	}
	fmt.Printf("click upgrade: supported (prefix %s, domain %s, label %s)\n",
		out["prefix"], out["domain"], out["label"])
	tasks, _ := out["tasks"].([]upgrade.TaskSummary)
	if len(tasks) == 0 {
		fmt.Println("no upgrade tasks recorded")
		return 0
	}
	for _, t := range tasks {
		line := fmt.Sprintf("%s  %-22s %s -> %s", t.TaskID, t.State, t.CurrentVersion, t.TargetVersion)
		if t.FailureStage != "" {
			line += fmt.Sprintf("  [%s/%s] %s", t.FailureStage, t.ErrorKind, t.Detail)
		}
		fmt.Println(line)
	}
	return 0
}

// RunUpgradeWorker executes one accepted task inside the launchd job. It must
// stay free of business config/DB/HTTP initialization: the binary it runs
// from may be the only surviving copy of the old version.
func RunUpgradeWorker(info Info, args []string) int {
	fs := flag.NewFlagSet("upgrade-worker", flag.ContinueOnError)
	taskID := fs.String("task-id", "", "upgrade task id")
	if err := fs.Parse(args); err != nil || *taskID == "" {
		fmt.Fprintln(os.Stderr, "usage: tokenlive upgrade-worker --task-id <id>")
		return 2
	}
	install, root, reasons := upgradePaths(info)
	if install == nil {
		fmt.Fprintln(os.Stderr, "installation not supported:", strings.Join(reasons, ","))
		return 2
	}
	if err := upgrade.RunWorkerTask(context.Background(), upgrade.WorkerConfig{
		Install:    install,
		Root:       root,
		Executable: executableOrBust(),
		Home:       install.Home,
		TaskID:     *taskID,
	}); err != nil {
		fmt.Fprintln(os.Stderr, "upgrade worker:", err)
		return 1
	}
	return 0
}
