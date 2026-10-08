package upgrade

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Launchctl wraps the fixed launchctl invocations. Per the launchd research:
// legacy `list <label>` output is parsed with strict bounds (PID or "-"),
// `bootstrap`/`bootout` use exact domain-target pairs, and only exit codes are
// interpreted — never `print` output structure.
type Launchctl struct {
	Runner Runner
	// Bin is the launchctl path; tests substitute a fake.
	Bin string
}

func NewLaunchctl(r Runner) *Launchctl {
	return &Launchctl{Runner: r, Bin: "/bin/launchctl"}
}

var listLine = regexp.MustCompile(`^\s*(\d+|-)\s+\d+\s+\S+\s*$`)

// ListPID returns the PID of a running job, (0,false,nil) when launchctl
// reports the job loaded but not running ("-"), and an error for any output
// shape it cannot interpret strictly.
func (l *Launchctl) ListPID(ctx context.Context, label string) (pid int, running bool, err error) {
	argv := []string{l.Bin, "list", label}
	out, err := l.Runner.Run(ctx, argv, []string{"PATH=/usr/bin:/bin"}, 10*time.Second)
	if err != nil {
		// launchctl exits non-zero when the label is unknown; that is a
		// definitive "not loaded", not a failure.
		if exit, ok := err.(*ExitError); ok {
			if strings.Contains(exit.Output, "Could not find service") {
				return 0, false, nil
			}
		}
		return 0, false, err
	}
	lines := strings.Split(strings.TrimRight(string(out), "\n"), "\n")
	if len(lines) != 1 {
		return 0, false, fmt.Errorf("launchctl list: unexpected %d lines", len(lines))
	}
	match := listLine.FindStringSubmatch(lines[0])
	if match == nil {
		return 0, false, fmt.Errorf("launchctl list: unparseable output")
	}
	if match[1] == "-" {
		return 0, false, nil
	}
	value, err := strconv.Atoi(match[1])
	if err != nil {
		return 0, false, fmt.Errorf("launchctl list: bad pid")
	}
	return value, true, nil
}

// Bootstrap loads the exact plist into the exact domain. Any non-zero exit is
// returned verbatim: the caller must reconcile rather than retry blindly.
func (l *Launchctl) Bootstrap(ctx context.Context, domain, plistPath string) error {
	_, err := l.Runner.Run(ctx, []string{l.Bin, "bootstrap", domain, plistPath},
		[]string{"PATH=/usr/bin:/bin"}, 15*time.Second)
	if err != nil {
		if exit, ok := err.(*ExitError); ok && strings.Contains(exit.Output, "already bootstrapped") {
			return nil
		}
		return err
	}
	return nil
}

// Bootout removes the exact domain-target job.
func (l *Launchctl) Bootout(ctx context.Context, domain, label string) error {
	_, err := l.Runner.Run(ctx, []string{l.Bin, "bootout", domain + "/" + label},
		[]string{"PATH=/usr/bin:/bin"}, 15*time.Second)
	if err != nil {
		if exit, ok := err.(*ExitError); ok {
			// Bootout of an already-gone job is the desired end state.
			if strings.Contains(exit.Output, "Could not find service") ||
				strings.Contains(exit.Output, "No such process") {
				return nil
			}
		}
		return err
	}
	return nil
}

// PrintExists probes domain-qualified existence by exit code only.
func (l *Launchctl) PrintExists(ctx context.Context, domain, label string) error {
	_, err := l.Runner.Run(ctx, []string{l.Bin, "print", domain + "/" + label},
		[]string{"PATH=/usr/bin:/bin"}, 10*time.Second)
	return err
}
