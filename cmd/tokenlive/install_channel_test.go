package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInstalledChannelRequiresMarker(t *testing.T) {
	root, exe := installedChannelFixture(t)
	if got := installedChannel(exe); got != "unknown" {
		t.Fatalf("unmarked executable channel = %q", got)
	}
	writeInstallMarker(t, root, "homebrew\n")
	if got := installedChannel(exe); got != "homebrew" {
		t.Fatalf("marked executable channel = %q", got)
	}
}

func TestInstalledChannelRejectsInvalidMarkers(t *testing.T) {
	for _, content := range []string{"", "release", "Homebrew\n", " homebrew\n", "homebrew \n", "homebrew\nextra", "homebrew\n\n"} {
		t.Run(content, func(t *testing.T) {
			root, exe := installedChannelFixture(t)
			writeInstallMarker(t, root, content)
			if got := installedChannel(exe); got != "unknown" {
				t.Fatalf("marker %q channel = %q", content, got)
			}
		})
	}
	t.Run("no trailing newline", func(t *testing.T) {
		root, exe := installedChannelFixture(t)
		writeInstallMarker(t, root, "homebrew")
		if got := installedChannel(exe); got != "homebrew" {
			t.Fatalf("marker channel = %q", got)
		}
	})
}

func TestInstalledChannelResolvesHomebrewSymlinks(t *testing.T) {
	root, exe := installedChannelFixture(t)
	writeInstallMarker(t, root, "homebrew\n")
	prefix := t.TempDir()
	opt := filepath.Join(prefix, "opt", "tokenlive")
	if err := os.MkdirAll(filepath.Dir(opt), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(root, opt); err != nil {
		t.Fatal(err)
	}
	linkedExe := filepath.Join(prefix, "bin", "tokenlive")
	if err := os.MkdirAll(filepath.Dir(linkedExe), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(opt, "bin", "tokenlive"), linkedExe); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{exe, filepath.Join(opt, "bin", "tokenlive"), linkedExe} {
		if got := installedChannel(path); got != "homebrew" {
			t.Fatalf("%s channel = %q", path, got)
		}
	}
	// A marker beside the symlink is not evidence for the actual executable.
	if err := os.Remove(filepath.Join(root, "libexec", "tokenlive-install-channel")); err != nil {
		t.Fatal(err)
	}
	writeInstallMarker(t, prefix, "homebrew\n")
	if got := installedChannel(linkedExe); got != "unknown" {
		t.Fatalf("symlink-adjacent marker channel = %q", got)
	}
}

func TestInstalledChannelResolutionErrorsAreUnknown(t *testing.T) {
	root, exe := installedChannelFixture(t)
	writeInstallMarker(t, root, "homebrew\n")
	// Executable lookup failures must not accidentally read a cwd marker.
	t.Chdir(root)
	dangling := filepath.Join(root, "bin", "dangling")
	if err := os.Symlink(filepath.Join(root, "bin", "missing"), dangling); err != nil {
		t.Fatal(err)
	}
	loop := filepath.Join(root, "bin", "loop")
	if err := os.Symlink(loop, loop); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"", filepath.Join(root, "bin", "missing"), dangling, loop} {
		if got := installedChannel(path); got != "unknown" {
			t.Fatalf("invalid executable %q channel = %q", path, got)
		}
	}
	marker := filepath.Join(root, "libexec", "tokenlive-install-channel")
	if err := os.Remove(marker); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(marker, 0755); err != nil {
		t.Fatal(err)
	}
	if got := installedChannel(exe); got != "unknown" {
		t.Fatalf("unreadable marker channel = %q", got)
	}
}

func installedChannelFixture(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	exe := filepath.Join(root, "bin", "tokenlive")
	if err := os.MkdirAll(filepath.Dir(exe), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(exe, []byte("test"), 0755); err != nil {
		t.Fatal(err)
	}
	return root, exe
}

func writeInstallMarker(t *testing.T, root, content string) {
	t.Helper()
	marker := filepath.Join(root, "libexec", "tokenlive-install-channel")
	if err := os.MkdirAll(filepath.Dir(marker), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(marker, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}
