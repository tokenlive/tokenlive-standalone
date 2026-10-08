package assemble

import (
	"os"
	"path/filepath"
	"runtime"

	"github.com/tokenlive/tokenlive-standalone/internal/upgrade"
)

// newUpgradeManager probes the running installation and, when click upgrade
// is supported, returns the host manager wired to the persistent task store.
// Any probe failure yields nil: capability honestly reports unsupported.
func newUpgradeManager(opt Options, port int) *upgrade.Manager {
	if runtime.GOOS != "darwin" {
		return nil
	}
	prober := upgrade.NewServiceProber(upgrade.ExecRunner{})
	current := adminIdentity(opt.Version, opt.BuildKind, opt.InstallChannel)
	install, _ := prober.Probe(port, current.Build)
	if install == nil {
		return nil
	}
	dataDir := opt.DataDir
	if dataDir == "" {
		dataDir = "data"
	}
	varRoot := filepath.Dir(dataDir)
	if varRoot == "." || varRoot == dataDir {
		varRoot = dataDir
	}
	root := filepath.Join(varRoot, "upgrades", upgrade.InstallIDFor(install))
	executable, err := os.Executable()
	if err != nil {
		return nil
	}
	manager := upgrade.NewManager(root, install, upgrade.ExecRunner{}, executable)
	// Startup reconciliation: resolve leftover queued/unknown-outcome tasks
	// and verify pending cleanups from previous attempts.
	_ = manager.Reconcile()
	return manager
}

func portFor(opt Options) int {
	if opt.Port != 0 {
		return opt.Port
	}
	if opt.GatewayConf != nil {
		if port := opt.GatewayConf.GetInt("http.port"); port != 0 {
			return port
		}
	}
	return 2525
}
