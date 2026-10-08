package upgrade

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tokenlive/tokenlive-admin/pkg/productversion"
)

// syntheticInstall builds a full Homebrew-shaped install inside temp dirs and
// returns (prefix, home). The opt link and LaunchAgents plist mirror the real
// brew services layout so the prober exercises its actual evidence chain.
func syntheticInstall(t *testing.T) (string, string) {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	prefix := filepath.Join(root, "opt", "homebrew")
	home := filepath.Join(root, "home")
	keg := filepath.Join(prefix, "Cellar", "tokenlive", "1.0.0")
	for _, dir := range []string{
		filepath.Join(prefix, "bin"),
		filepath.Join(prefix, "opt"),
		filepath.Join(keg, "bin"),
		filepath.Join(prefix, "libexec"),
		filepath.Join(prefix, "Library", "Taps", "tokenlive", "homebrew-tokenlive", "Formula"),
		filepath.Join(home, "Library", "LaunchAgents"),
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write := func(path, body string) {
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(prefix, "bin", "brew"), "#!/bin/bash\n")
	write(filepath.Join(keg, "bin", "tokenlive"), "binary")
	write(filepath.Join(prefix, "libexec", "tokenlive-install-channel"), "homebrew\n")
	write(filepath.Join(prefix, "Library", "Taps", "tokenlive", "homebrew-tokenlive", "Formula", "tokenlive.rb"), testFormula)
	if err := os.Symlink(keg, filepath.Join(prefix, "opt", "tokenlive")); err != nil {
		t.Fatal(err)
	}
	optBin := filepath.Join(prefix, "opt", "tokenlive", "bin", "tokenlive")
	write(filepath.Join(home, "Library", "LaunchAgents", "homebrew.mxcl.tokenlive.plist"),
		fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<plist version="1.0"><dict>
<key>Label</key><string>homebrew.mxcl.tokenlive</string>
<key>ProgramArguments</key><array>
<string>%s</string>
<string>-conf</string><string>/opt/homebrew/etc/tokenlive/config.yml</string>
</array></dict></plist>
`, optBin))
	return prefix, home
}

const testFormula = `class Tokenlive < Formula
  desc "TokenLive all-in-one"
  homepage "https://github.com/tokenlive/tokenlive-standalone"
  version "1.1.0"

  if Hardware::CPU.intel?
    url "https://github.com/tokenlive/tokenlive-standalone/releases/download/v1.1.0/tokenlive-1.1.0-darwin-amd64.tar.gz"
    sha256 "e4e340076d92e31da008807765ad8c2259929f1cd9c4e5ef72272bd4c4234f49"
  else
    url "https://github.com/tokenlive/tokenlive-standalone/releases/download/v1.1.0/tokenlive-1.1.0-darwin-arm64.tar.gz"
    sha256 "ebe7940542a5024e33b078e2ad1713bfdfe3065278c4f771d2430ee5b2b6f870"
  end

  def install
    bin.install "bin/tokenlive"
  end
end
`

func testProber(prefix, home string) Prober {
	return Prober{
		Executable:    filepath.Join(prefix, "Cellar", "tokenlive", "1.0.0", "bin", "tokenlive"),
		Getpid:        func() int { return 4242 },
		UserHome:      home,
		UID:           501,
		EUID:          501,
		ListPID:       func(string) (int, bool, error) { return 4242, true, nil },
		DomainPrinted: func(string, string) error { return nil },
	}
}

func TestProbeVerifiesStandardInstall(t *testing.T) {
	prefix, home := syntheticInstall(t)
	install, reasons := testProber(prefix, home).Probe(2525, productversion.Build{Version: "1.0.0", Kind: "release"})
	if reasons != nil {
		t.Fatalf("expected probe success, got reasons %v", reasons)
	}
	if install.Prefix != prefix || install.Keg != filepath.Join(prefix, "Cellar", "tokenlive", "1.0.0") {
		t.Fatalf("wrong install identity: %+v", install)
	}
	if install.ServiceLabel != "homebrew.mxcl.tokenlive" {
		t.Fatalf("wrong label: %s", install.ServiceLabel)
	}
	if install.Domain != "gui/501" {
		t.Fatalf("wrong domain: %s", install.Domain)
	}
}

func TestProbeRejectsBrokenIdentity(t *testing.T) {
	base := func() (string, string) { return syntheticInstall(t) }

	t.Run("service pid mismatch", func(t *testing.T) {
		prefix, home := base()
		prober := testProber(prefix, home)
		prober.ListPID = func(string) (int, bool, error) { return 9999, true, nil }
		if _, reasons := prober.Probe(2525, productversion.Build{}); len(reasons) == 0 || reasons[0] != "service_identity_unverifiable" {
			t.Fatalf("expected service_identity_unverifiable, got %v", reasons)
		}
	})

	t.Run("domain query failure", func(t *testing.T) {
		prefix, home := base()
		prober := testProber(prefix, home)
		prober.DomainPrinted = func(string, string) error { return context.DeadlineExceeded }
		if _, reasons := prober.Probe(2525, productversion.Build{}); len(reasons) == 0 || reasons[0] != "service_identity_unverifiable" {
			t.Fatalf("expected service_identity_unverifiable, got %v", reasons)
		}
	})

	t.Run("missing managing plist", func(t *testing.T) {
		prefix, home := base()
		if err := os.Remove(filepath.Join(home, "Library", "LaunchAgents", "homebrew.mxcl.tokenlive.plist")); err != nil {
			t.Fatal(err)
		}
		if _, reasons := testProber(prefix, home).Probe(2525, productversion.Build{}); len(reasons) == 0 || reasons[0] != "service_identity_unverifiable" {
			t.Fatalf("expected service_identity_unverifiable, got %v", reasons)
		}
	})

	t.Run("unrelated plist program", func(t *testing.T) {
		prefix, home := base()
		path := filepath.Join(home, "Library", "LaunchAgents", "homebrew.mxcl.tokenlive.plist")
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		replaced := strings.Replace(string(body), prefix+"/opt/tokenlive/bin/tokenlive", "/usr/bin/other", 1)
		if err := os.WriteFile(path, []byte(replaced), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, reasons := testProber(prefix, home).Probe(2525, productversion.Build{}); len(reasons) == 0 || reasons[0] != "service_identity_unverifiable" {
			t.Fatalf("expected service_identity_unverifiable, got %v", reasons)
		}
	})

	t.Run("root user context", func(t *testing.T) {
		prefix, home := base()
		prober := testProber(prefix, home)
		prober.UID, prober.EUID = 0, 0
		if _, reasons := prober.Probe(2525, productversion.Build{}); len(reasons) == 0 || reasons[0] != "user_context_unsupported" {
			t.Fatalf("expected user_context_unsupported, got %v", reasons)
		}
	})

	t.Run("homebrew env overrides", func(t *testing.T) {
		prefix, home := base()
		if err := os.MkdirAll(filepath.Join(prefix, ".homebrew"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(prefix, ".homebrew", "brew.env"), []byte("HOMEBREW_PROXY=x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, reasons := testProber(prefix, home).Probe(2525, productversion.Build{}); len(reasons) == 0 || reasons[0] != "homebrew_env_overrides" {
			t.Fatalf("expected homebrew_env_overrides, got %v", reasons)
		}
	})
}

func stringsReplace(s, old, new string) string {
	return strings.Replace(s, old, new, -1)
}
