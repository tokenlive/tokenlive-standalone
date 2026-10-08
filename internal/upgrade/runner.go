package upgrade

import (
	"context"
	"fmt"
	"os"
	"time"
)

// Runner executes fixed argv commands with a whitelist environment and a
// bounded timeout. It is the single execution point for every external
// command in the upgrade flow, so tests can inject fakes and reviews have
// one place to audit. No shell is ever spawned; argv is passed verbatim.
type Runner interface {
	// Run executes argv and returns bounded combined output. A non-zero exit
	// is reported as *ExitError; a timeout as *TimeoutError with unknown
	// descendant state (callers must treat it as unresolved, never failed).
	Run(ctx context.Context, argv []string, env []string, timeout time.Duration) ([]byte, error)
}

// ExitError reports a command that provably exited non-zero. Evidence of a
// clean process exit distinguishes known failures from unknown outcomes.
type ExitError struct {
	Argv   []string
	Code   int
	Output string
}

func (e *ExitError) Error() string {
	return fmt.Sprintf("%s exited %d", e.Argv[0], e.Code)
}

// TimeoutError reports that the supervision budget elapsed. The child may
// still be running with descendants; the outcome is deliberately unknown.
type TimeoutError struct {
	Argv []string
}

func (e *TimeoutError) Error() string {
	return fmt.Sprintf("%s timed out; process state unknown", e.Argv[0])
}

// ExecRunner is the production Runner. The process runner itself is injected
// by tests; here we adapt os/exec lazily to keep the interface fakeable.
type ExecRunner struct{}

// brewEnv builds the fixed, reviewed environment for Homebrew commands.
// The whitelist exists so inherited variables cannot redirect sources,
// permissions, proxies or cleanup behavior (see Homebrew research §3.5).
func brewEnv(prefix, home string) []string {
	return []string{
		"PATH=" + prefix + "/bin:/usr/bin:/bin:/usr/sbin:/sbin",
		"HOME=" + home,
		"HOMEBREW_PREFIX=" + prefix,
		"HOMEBREW_NO_AUTO_UPDATE=1",
		"HOMEBREW_NO_INSTALL_CLEANUP=1",
		"HOMEBREW_NO_INSTALLED_DEPENDENTS_CHECK=1",
		"HOMEBREW_NO_ANALYTICS=1",
		"HOMEBREW_NO_ENV_HINTS=1",
		"HOMEBREW_NO_INSTALL_FROM_API=1",
	}
}

func executablePath() string {
	path, err := os.Executable()
	if err != nil {
		return ""
	}
	return path
}
