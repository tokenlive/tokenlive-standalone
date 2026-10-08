package upgrade

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"time"
)

// maxOutputBytes bounds captured command output persisted into diagnostics.
const maxOutputBytes = 64 << 10

// Run implements Runner with os/exec. The child runs with a private process
// group so launchd's group semantics match the research analysis; killing on
// timeout still reports an unknown outcome, because descendants outside the
// group (or racing spawns) cannot be proven gone.
func (ExecRunner) Run(ctx context.Context, argv []string, env []string, timeout time.Duration) ([]byte, error) {
	if len(argv) == 0 {
		return nil, fmt.Errorf("empty argv")
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(runCtx, argv[0], argv[1:]...)
	cmd.Env = env
	cmd.Stdin = nil
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case waitErr := <-done:
		trimmed := out.Bytes()
		if len(trimmed) > maxOutputBytes {
			trimmed = trimmed[:maxOutputBytes]
		}
		var exitErr *exec.ExitError
		if errors.As(waitErr, &exitErr) {
			return trimmed, &ExitError{Argv: argv, Code: exitErr.ExitCode(), Output: string(trimmed)}
		}
		if waitErr != nil {
			// Start succeeded but supervision failed (signal, IO error):
			// the exit status is not trustworthy, report unknown.
			return trimmed, &TimeoutError{Argv: argv}
		}
		return trimmed, nil
	case <-runCtx.Done():
		_ = cmd.Process.Kill()
		<-done
		return out.Bytes(), &TimeoutError{Argv: argv}
	}
}
