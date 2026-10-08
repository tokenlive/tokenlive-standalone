// Package upgrade implements the Homebrew standalone click-upgrade host:
// installation identity probes, the persistent task store, the launchd
// one-shot worker and the fixed-target Homebrew adapter.
//
// Security posture (per the confirmed design): every external action is a
// fixed argv executed as the current non-root user, the install target is
// pinned to validated Formula bytes, unknown outcomes never produce success,
// and any state that cannot be proven terminal blocks new tasks.
package upgrade

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/tokenlive/tokenlive-admin/pkg/productversion"
)

// Installation is the verified identity of the running Homebrew install.
// Every field has been cross-checked against on-disk evidence; the zero
// value is never a valid installation.
type Installation struct {
	Prefix       string // Homebrew prefix, e.g. /opt/homebrew
	Keg          string // versioned Cellar dir of the running binary
	OptBin       string // <prefix>/opt/tokenlive/bin/tokenlive
	BrewBin      string // <prefix>/bin/brew
	TapDir       string // <prefix>/Library/Taps/tokenlive/homebrew-tokenlive
	FormulaPath  string // canonical tap Formula path
	ServiceLabel string // launchd label of the managing brew services job
	PlistPath    string // user LaunchAgents plist of that job
	Domain       string // launchd domain, e.g. gui/501
	UID          int
	Home         string // real home directory of the running user
	Port         int    // HTTP port this instance listens on
	Current      productversion.Build
}

// Prober discovers and verifies the installation. It is an interface so tests
// can inject filesystem and process fakes without touching the real machine.
type Prober struct {
	// Executable overrides os.Executable (tests).
	Executable string
	// Getpid overrides os.Getpid (tests).
	Getpid func() int
	// ListPID verifies the service label's PID. The main service must inject
	// it and pass; the CLI omits it (its own PID is not the service's).
	ListPID func(label string) (pid int, ok bool, err error)
	// DomainPrinted verifies domain-qualified job existence by exit code.
	DomainPrinted func(domain, label string) error
	// UserHome overrides os.UserHomeDir (tests).
	UserHome string
	// UIDFunc overrides os.Getuid/Geteuid (tests).
	UID  int
	EUID int
}

// NewServiceProber is the production prober for the running service: it
// proves that launchd's PID for the label is this very process.
func NewServiceProber(runner Runner) Prober {
	launch := NewLaunchctl(runner)
	return Prober{
		UID:  os.Getuid(),
		EUID: os.Geteuid(),
		ListPID: func(label string) (int, bool, error) {
			pid, running, err := launch.ListPID(context.Background(), label)
			return pid, running, err
		},
		DomainPrinted: func(domain, label string) error { return launch.PrintExists(context.Background(), domain, label) },
	}
}

// NewCLIProber is the read-only prober for `tokenlive upgrade-status`: disk
// evidence only, no PID self-check and no domain probe.
func NewCLIProber() Prober {
	return Prober{UID: os.Getuid(), EUID: os.Geteuid()}
}

// Probe verifies that this process is a Homebrew-managed tokenlive service
// eligible for click upgrade. It returns the installation or the ordered
// list of stable blocking reasons. Probe performs only read-only checks.
func (p Prober) Probe(port int, current productversion.Build) (*Installation, []string) {
	var reasons []string
	fail := func(r string) (*Installation, []string) { reasons = append(reasons, r); return nil, reasons }
	if runtime.GOOS != "darwin" {
		return fail("os_unsupported")
	}
	if p.UID != p.EUID || p.UID == 0 {
		return fail("user_context_unsupported")
	}
	exe := p.Executable
	if exe == "" {
		exe = executablePath()
	}
	if exe == "" {
		return fail("executable_unresolved")
	}
	resolved, err := filepath.EvalSymlinks(exe)
	if err != nil {
		return fail("executable_unresolved")
	}
	// <prefix>/Cellar/tokenlive/<version>/bin/tokenlive → 4 parents to prefix.
	keg := filepath.Dir(filepath.Dir(resolved))
	if filepath.Base(filepath.Dir(keg)) != "tokenlive" || filepath.Base(filepath.Dir(filepath.Dir(keg))) != "Cellar" {
		return fail("install_unsupported")
	}
	prefix := filepath.Dir(filepath.Dir(filepath.Dir(keg)))
	marker := filepath.Join(prefix, "libexec", "tokenlive-install-channel")
	if content, err := os.ReadFile(marker); err != nil || strings.TrimSpace(string(content)) != "homebrew" {
		return fail("install_unsupported")
	}
	brewBin := filepath.Join(prefix, "bin", "brew")
	if info, err := os.Stat(brewBin); err != nil || info.IsDir() {
		return fail("brew_missing")
	}
	tapDir := filepath.Join(prefix, "Library", "Taps", "tokenlive", "homebrew-tokenlive")
	formulaPath := filepath.Join(tapDir, "Formula", "tokenlive.rb")
	if info, err := os.Stat(formulaPath); err != nil || info.IsDir() {
		return fail("tap_missing")
	}
	home := p.UserHome
	if home == "" {
		if h, err := os.UserHomeDir(); err == nil {
			home = h
		} else {
			return fail("user_context_unsupported")
		}
	}
	// Homebrew configuration overrides could change sources, permissions or
	// cleanup behavior of our fixed commands; refuse rather than adapt.
	for _, envPath := range []string{
		filepath.Join(prefix, ".homebrew", "brew.env"),
		filepath.Join(home, ".homebrew", "brew.env"),
		filepath.Join(home, ".config", "homebrew", "brew.env"),
	} {
		if _, err := os.Stat(envPath); err == nil {
			return fail("homebrew_env_overrides")
		}
	}
	// The opt link must resolve to the running keg, proving this install is
	// the standard, non-pinned, single installation for the prefix.
	optBin := filepath.Join(prefix, "opt", "tokenlive", "bin", "tokenlive")
	optResolved, err := filepath.EvalSymlinks(optBin)
	if err != nil || optResolved != resolved {
		return fail("opt_link_mismatch")
	}
	label, plistPath, err := p.servicePlist(home, optBin)
	if err != nil {
		return fail("service_identity_unverifiable")
	}
	pid := p.Getpid
	if pid == nil {
		pid = os.Getpid
	}
	if p.ListPID != nil {
		listed, ok, err := p.ListPID(label)
		if err != nil || !ok || listed != pid() {
			return fail("service_identity_unverifiable")
		}
	}
	if p.DomainPrinted != nil {
		domain := fmt.Sprintf("gui/%d", p.UID)
		if err := p.DomainPrinted(domain, label); err != nil {
			return fail("service_identity_unverifiable")
		}
	}
	return &Installation{
		Prefix:       prefix,
		Keg:          keg,
		OptBin:       optBin,
		BrewBin:      brewBin,
		TapDir:       tapDir,
		FormulaPath:  formulaPath,
		ServiceLabel: label,
		PlistPath:    plistPath,
		Domain:       fmt.Sprintf("gui/%d", p.UID),
		UID:          p.UID,
		Home:         home,
		Port:         port,
		Current:      current,
	}, nil
}

// servicePlist finds the unique user LaunchAgents plist whose program is this
// install's opt binary and returns its label and path. Ambiguity is failure:
// multiple matching plists or an unparseable plist must never be guessed.
func (p Prober) servicePlist(home, optBin string) (label, plistPath string, err error) {
	agents := filepath.Join(home, "Library", "LaunchAgents")
	entries, err := os.ReadDir(agents)
	if err != nil {
		return "", "", err
	}
	var matches []string
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".plist") {
			continue
		}
		path := filepath.Join(agents, entry.Name())
		program, perr := plistProgram(path)
		if perr != nil {
			continue
		}
		if program == optBin {
			matches = append(matches, path)
		}
	}
	if len(matches) != 1 {
		return "", "", fmt.Errorf("expected exactly one managing plist, found %d", len(matches))
	}
	base := filepath.Base(matches[0])
	return strings.TrimSuffix(base, ".plist"), matches[0], nil
}
