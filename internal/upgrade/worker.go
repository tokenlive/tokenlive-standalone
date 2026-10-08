package upgrade

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/tokenlive/tokenlive-admin/pkg/upgradehost"
)

// Stage budgets. Each bounds supervision only: exceeding one means "we
// cannot prove what happened", which is needs_attention — never failure.
const (
	stageInstallTimeout  = 10 * time.Minute
	stageStopTimeout     = 90 * time.Second
	stageStartTimeout    = 120 * time.Second
	stageVerifyBudget    = 3 * time.Minute
	stageVerifyPollEvery = 2 * time.Second
)

// Worker executes one accepted upgrade task inside the one-shot launchd job.
// Every stage first persists its pending state, then performs the external
// action, so a crash leaves evidence instead of fiction.
type Worker struct {
	Store  *Store
	Runner Runner
	Launch *Launchctl
	Health *HealthClient
	// Prefix and Home come from the verified installation record.
	Prefix string
	Home   string
	Now    func() time.Time
	// BootoutSelf removes the job from launchd by exec-replacing the process.
	// In production it never returns; tests substitute a recorder.
	BootoutSelf func(domain, label string) error
	// pollEvery is the verify poll interval; zero uses the default. Tests
	// shorten it together with an accelerated clock.
	pollEvery time.Duration
}

// Run executes the task. It returns only when the task reached a terminal
// state or an unexpected internal error occurred (recorded as needs_attention).
func (w *Worker) Run(ctx context.Context, taskID string) error {
	lock, ok, err := TryLock(w.Store.Root())
	if err != nil {
		return w.failUnknown(taskID, "lock", err)
	}
	if !ok {
		// Another holder owns the install lock: refuse, do not queue.
		return w.failUnknown(taskID, "lock", fmt.Errorf("install lock busy"))
	}
	defer lock.Release()

	task, err := w.Store.Load(taskID)
	if err != nil {
		return err
	}
	if task == nil {
		return fmt.Errorf("task %s not found", taskID)
	}
	if task.State != upgradehost.StateQueued {
		return fmt.Errorf("task %s is %s, not queued", taskID, task.State)
	}

	if err := w.writeReady(task); err != nil {
		return w.failUnknown(task.TaskID, "ready", err)
	}

	// Stage: snapshot — copy the confirmed Formula bytes into the tap.
	task.State = upgradehost.StateDownloading
	task.Phase = "tap_snapshot"
	_ = w.Store.Save(task)
	snapshotPath, err := w.stageSnapshot(task)
	if err != nil {
		// Service untouched; this is a known, clean failure.
		return w.fail(task, "tap_snapshot", "snapshot_failed", err)
	}

	// Stage: brew upgrade with the fixed snapshot path.
	task.State = upgradehost.StateInstalling
	task.Phase = "brew_upgrade"
	_ = w.Store.Save(task)
	if err := w.runBrew(ctx, stageInstallTimeout, "upgrade", "--formula", snapshotPath); err != nil {
		if _, unknown := err.(*TimeoutError); unknown {
			return w.failUnknown(task.TaskID, "brew_upgrade", err)
		}
		return w.fail(task, "brew_upgrade", "brew_upgrade_failed", err)
	}

	// Stage: service restart. Capture the current service PID first so
	// verification can reject a process that never actually restarted.
	// stop --max-wait bounds the wait inside brew; our own timeout still
	// supervises the whole invocation (research §4.1).
	task.State = upgradehost.StateRestarting
	task.Phase = "service_pid"
	_ = w.Store.Save(task)
	if pid, running, err := w.Launch.ListPID(ctx, task.ServiceLabel); err != nil {
		return w.failUnknown(task.TaskID, "service_pid", err)
	} else if running {
		task.ServicePID = pid
	}
	task.Phase = "service_stop"
	_ = w.Store.Save(task)
	if err := w.runBrew(ctx, stageStopTimeout, "services", "stop", "--max-wait=60", "tokenlive"); err != nil {
		// A stop that neither provably succeeded nor provably failed leaves
		// the service state uncertain; that is attention, not success.
		return w.failUnknown(task.TaskID, "service_stop", err)
	}

	task.Phase = "service_start"
	_ = w.Store.Save(task)
	if err := w.runBrew(ctx, stageStartTimeout, "services", "start", "tokenlive"); err != nil {
		return w.failUnknown(task.TaskID, "service_start", err)
	}

	// Stage: verify the restarted service is provably the confirmed target.
	task.State = upgradehost.StateVerifying
	task.Phase = "health"
	_ = w.Store.Save(task)
	if ok, reason := w.verify(ctx, task); !ok {
		return w.failUnknown(task.TaskID, "verify", fmt.Errorf("verification failed: %s", reason))
	}

	// Terminal success plus two-phase cleanup marker, then self-removal.
	task.State = upgradehost.StateSucceeded
	task.Phase = ""
	task.CleanupPending = true
	task.SucceededAt = w.Now().UTC()
	if err := w.Store.Save(task); err != nil {
		return err
	}
	if w.BootoutSelf != nil {
		w.BootoutSelf(task.Domain, taskLabel(task.InstallID, task.TaskID))
	}
	return nil
}

// stageSnapshot writes the persisted Formula bytes into the tap's dedicated
// hidden directory and verifies the digest on disk before use.
func (w *Worker) stageSnapshot(task *Task) (string, error) {
	dir := w.Store.taskDir(task.TaskID)
	body, err := os.ReadFile(filepath.Join(dir, "snapshot", "tokenlive.rb"))
	if err != nil {
		return "", err
	}
	if sha256Hex(body) != task.FormulaSHA256 {
		return "", fmt.Errorf("snapshot digest mismatch")
	}
	snapshotDir := filepath.Join(w.Prefix, "Library", "Taps", "tokenlive", "homebrew-tokenlive",
		".tokenlive-upgrade", task.TaskID)
	if err := os.MkdirAll(snapshotDir, 0o700); err != nil {
		return "", err
	}
	snapshotPath := filepath.Join(snapshotDir, "tokenlive.rb")
	if err := writeFileAtomic(snapshotPath, body, 0o600); err != nil {
		return "", err
	}
	onDisk, err := os.ReadFile(snapshotPath)
	if err != nil || sha256Hex(onDisk) != task.FormulaSHA256 {
		return "", fmt.Errorf("snapshot verify failed after write")
	}
	return snapshotPath, nil
}

// runBrew executes one reviewed Homebrew command with the fixed whitelist
// environment. Bare `brew upgrade` is structurally impossible: the snapshot
// path is always part of upgrade argv.
func (w *Worker) runBrew(ctx context.Context, timeout time.Duration, args ...string) error {
	argv := append([]string{filepath.Join(w.Prefix, "bin", "brew")}, args...)
	_, err := w.Runner.Run(ctx, argv, brewEnv(w.Prefix, w.Home), timeout)
	return err
}

// verify polls until the health handshake proves the target or the budget
// ends. Polling survives transient restart windows (research: not yet up is
// not failed).
func (w *Worker) verify(ctx context.Context, task *Task) (bool, string) {
	poll := w.pollEvery
	if poll <= 0 {
		poll = stageVerifyPollEvery
	}
	deadline := w.Now().Add(stageVerifyBudget)
	var reason string
	for {
		if ctx.Err() != nil {
			return false, "context canceled"
		}
		if !w.Now().Before(deadline) {
			return false, "verification budget exhausted: " + reason
		}
		pid, running, err := w.Launch.ListPID(ctx, task.ServiceLabel)
		if err != nil || !running {
			reason = "service_not_running"
		} else if ok, r := w.Health.Check(ctx, task.Port, task.TargetVersion,
			task.TargetKegVersion, w.Prefix, "homebrew", pid, task.ServicePID); ok {
			return true, ""
		} else {
			reason = r
		}
		time.Sleep(poll)
	}
}

func (w *Worker) writeReady(task *Task) error {
	ready := struct {
		PID     int       `json:"pid"`
		Started time.Time `json:"started"`
	}{PID: os.Getpid(), Started: w.Now().UTC()}
	task.WorkerPID = ready.PID
	data, _ := json.Marshal(ready)
	if err := writeFileAtomic(filepath.Join(w.Store.taskDir(task.TaskID), "ready.json"), data, 0o600); err != nil {
		return err
	}
	return w.Store.Save(task)
}

// fail records a provably-ended failure with bounded diagnostics.
func (w *Worker) fail(task *Task, stage, kind string, cause error) error {
	task.State = upgradehost.StateFailed
	task.FailureStage = stage
	task.ErrorKind = kind
	task.Detail = truncate(diagnose(cause), 300)
	task.Phase = ""
	return w.Store.Save(task)
}

// failUnknown records an uncertain outcome; it never claims the service is
// down or that external processes ended.
func (w *Worker) failUnknown(taskID, stage string, cause error) error {
	if task, err := w.Store.Load(taskID); err == nil && task != nil {
		task.State = upgradehost.StateNeedsAttention
		task.FailureStage = stage
		task.ErrorKind = "outcome_unknown"
		task.Detail = truncate(diagnose(cause), 300)
		task.Phase = ""
		_ = w.Store.Save(task)
	}
	return cause
}

func diagnose(err error) string {
	switch e := err.(type) {
	case *ExitError:
		return fmt.Sprintf("%s exited %d: %s", filepath.Base(e.Argv[0]), e.Code, lastLine(e.Output))
	case *TimeoutError:
		return e.Error()
	default:
		return err.Error()
	}
}

func lastLine(s string) string {
	var last string
	for _, line := range strings.Split(s, "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			last = trimmed
		}
	}
	return last
}

// bootoutSelf exec-replaces this process with the exact bootout command.
// It only returns on failure to exec.
func bootoutSelf(domain, label string) error {
	return syscall.Exec("/bin/launchctl",
		[]string{"launchctl", "bootout", domain + "/" + label},
		[]string{"PATH=/usr/bin:/bin"})
}

// WorkerConfig carries everything the one-shot worker needs. It is populated
// from disk evidence only: no business config, DB or HTTP involvement.
type WorkerConfig struct {
	Install    *Installation
	Root       string
	Executable string
	Home       string
	TaskID     string
}

// RunWorkerTask is the production entry used by the `upgrade-worker`
// subcommand. On success it exec-replaces itself with the job bootout.
func RunWorkerTask(ctx context.Context, cfg WorkerConfig) error {
	worker := &Worker{
		Store:       NewStore(cfg.Root),
		Runner:      ExecRunner{},
		Launch:      NewLaunchctl(ExecRunner{}),
		Health:      NewHealthClient(),
		Prefix:      cfg.Install.Prefix,
		Home:        cfg.Home,
		Now:         time.Now,
		BootoutSelf: bootoutSelf,
	}
	return worker.Run(ctx, cfg.TaskID)
}
