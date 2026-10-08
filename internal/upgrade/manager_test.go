package upgrade

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tokenlive/tokenlive-admin/pkg/productversion"
	"github.com/tokenlive/tokenlive-admin/pkg/upgradehost"
)

// fakeRunner records invocations and fails selected subcommands on demand.
type fakeRunner struct {
	outputs [][]string
	fail    map[string]error
	// keepServicePID makes launchctl list report the same PID after start.
	// The default restart reports a new PID.
	keepServicePID bool
}

func (f *fakeRunner) Run(ctx context.Context, argv []string, env []string, timeout time.Duration) ([]byte, error) {
	f.outputs = append(f.outputs, argv)
	// Match injection keys against any argument after the binary so
	// "stop"/"start" also hit inside `brew services stop`.
	for _, arg := range argv[1:] {
		if err, ok := f.fail[arg]; ok {
			return nil, err
		}
	}
	// launchctl list must be a strict one-line PID record. A restarted
	// service reports a different PID than the one captured before stop.
	for _, arg := range argv {
		if arg == "list" {
			pid := "4242"
			if !f.keepServicePID && f.calls("start") > 0 {
				pid = "5353"
			}
			return []byte(pid + "\t0\tcom.tokenlive.service\n"), nil
		}
	}
	return []byte("ok"), nil
}

// calls counts invocations whose argv contains sub as a standalone token.
func (f *fakeRunner) calls(sub string) int {
	n := 0
	for _, argv := range f.outputs {
		for _, arg := range argv {
			if arg == sub {
				n++
				break
			}
		}
	}
	return n
}

// managerFixture wires a Manager over a synthetic install with injected
// fetch and runner so flows run without touching the real machine.
type managerFixture struct {
	manager *Manager
	runner  *fakeRunner
	root    string
	prefix  string
}

func newManagerFixture(t *testing.T, now func() time.Time) *managerFixture {
	t.Helper()
	prefix, home := syntheticInstall(t)
	prober := testProber(prefix, home)
	install, reasons := prober.Probe(2525, productversion.Build{Version: "1.0.0", Kind: "release"})
	if reasons != nil {
		t.Fatalf("probe failed: %v", reasons)
	}
	root := filepath.Join(prefix, "var", "tokenlive", "upgrades", InstallIDFor(install))
	runner := &fakeRunner{fail: map[string]error{}}
	manager := NewManager(root, install, runner, filepath.Join(install.Keg, "bin", "tokenlive"))
	// The UI sees the canonical candidate (v-prefixed); the keg stays raw.
	manager.fetch = func(context.Context) ([]byte, string, string, error) {
		return []byte(testFormula), "v1.1.0", "1.1.0", nil
	}
	if now != nil {
		manager.now = now
	}
	return &managerFixture{manager: manager, runner: runner, root: root, prefix: prefix}
}

func TestManagerCapabilityEmpty(t *testing.T) {
	f := newManagerFixture(t, nil)
	capability, err := f.manager.Capability(context.Background())
	if err != nil || !capability.Supported || !capability.Allowed || capability.ActiveTask != nil {
		t.Fatalf("capability: %+v %v", capability, err)
	}
}

func TestManagerPrepareValidation(t *testing.T) {
	f := newManagerFixture(t, nil)
	ctx := context.Background()

	prep, err := f.manager.Prepare(ctx, upgradehost.PrepareRequest{TargetVersion: "v1.1.0", Initiator: "admin"})
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	if prep.TaskID == "" || prep.Credential == "" || prep.ConfirmExpiresIn != 300 || !prep.RestartWarning {
		t.Fatalf("preparation incomplete: %+v", prep)
	}
	if prep.ReleaseURL != "https://github.com/tokenlive/tokenlive-standalone/releases/tag/v1.1.0" {
		t.Fatalf("release url: %s", prep.ReleaseURL)
	}

	// Second prepare conflicts with the awaiting task.
	if _, err := f.manager.Prepare(ctx, upgradehost.PrepareRequest{TargetVersion: "v1.1.0", Initiator: "admin"}); !errors.Is(err, upgradehost.ErrTaskConflict) {
		t.Fatalf("expected conflict, got %v", err)
	}

	// Candidate moving between confirmations invalidates the old target.
	base := time.Now()
	f.manager.now = func() time.Time { return base.Add(10 * time.Minute) }
	if err := f.manager.Reconcile(); err != nil {
		t.Fatal(err)
	}
	f.manager.fetch = func(context.Context) ([]byte, string, string, error) {
		return []byte(testFormula), "v1.2.0", "1.2.0", nil
	}
	if _, err := f.manager.Prepare(ctx, upgradehost.PrepareRequest{TargetVersion: "v1.1.0", Initiator: "admin"}); !errors.Is(err, upgradehost.ErrTargetChanged) {
		t.Fatalf("expected target changed, got %v", err)
	}
}

func TestManagerSubmitValidation(t *testing.T) {
	f := newManagerFixture(t, nil)
	ctx := context.Background()
	if _, err := f.manager.Prepare(ctx, upgradehost.PrepareRequest{TargetVersion: "v1.1.0", Initiator: "admin"}); err != nil {
		t.Fatal(err)
	}
	tasks, _ := f.manager.store.List()
	taskID := tasks[0].TaskID

	if _, err := f.manager.Submit(ctx, upgradehost.SubmitRequest{TaskID: "nope", Credential: "x", Confirm: true, Initiator: "admin"}); !errors.Is(err, upgradehost.ErrInvalidCredential) {
		t.Fatalf("unknown task: %v", err)
	}
	if _, err := f.manager.Submit(ctx, upgradehost.SubmitRequest{TaskID: taskID, Credential: "wrong", Confirm: true, Initiator: "admin"}); !errors.Is(err, upgradehost.ErrInvalidCredential) {
		t.Fatalf("credential mismatch: %v", err)
	}
	if _, err := f.manager.Submit(ctx, upgradehost.SubmitRequest{TaskID: taskID, Credential: "wrong", Confirm: true, Initiator: "mallory"}); !errors.Is(err, upgradehost.ErrInvalidCredential) {
		t.Fatalf("user binding: %v", err)
	}
	if _, err := f.manager.Submit(ctx, upgradehost.SubmitRequest{TaskID: taskID, Credential: "x", Confirm: false, Initiator: "admin"}); !errors.Is(err, upgradehost.ErrInvalidCredential) {
		t.Fatalf("missing confirm: %v", err)
	}
}

func TestManagerSubmitLaunchesWorker(t *testing.T) {
	f := newManagerFixture(t, nil)
	ctx := context.Background()
	prep, err := f.manager.Prepare(ctx, upgradehost.PrepareRequest{TargetVersion: "v1.1.0", Initiator: "admin"})
	if err != nil {
		t.Fatal(err)
	}
	view, err := f.manager.Submit(ctx, upgradehost.SubmitRequest{
		TaskID: prep.TaskID, Credential: prep.Credential, Confirm: true, Initiator: "admin",
	})
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if view.State != upgradehost.StateQueued {
		t.Fatalf("state after submit: %s", view.State)
	}
	if _, err := os.Stat(filepath.Join(f.manager.store.taskDir(prep.TaskID), "executor")); err != nil {
		t.Fatalf("executor not copied: %v", err)
	}
	if _, err := os.Stat(filepath.Join(f.manager.store.taskDir(prep.TaskID), "task.plist")); err != nil {
		t.Fatalf("plist not written: %v", err)
	}
	if f.runner.calls("bootstrap") != 1 {
		t.Fatalf("bootstrap calls: %d", f.runner.calls("bootstrap"))
	}
	// Idempotent double submit of the same secret: no second launch.
	if _, err := f.manager.Submit(ctx, upgradehost.SubmitRequest{
		TaskID: prep.TaskID, Credential: prep.Credential, Confirm: true, Initiator: "admin",
	}); err != nil {
		t.Fatalf("double submit: %v", err)
	}
	if f.runner.calls("bootstrap") != 1 {
		t.Fatalf("bootstrap re-launched: %d", f.runner.calls("bootstrap"))
	}
	// A different secret cannot ride the already-queued task.
	if _, err := f.manager.Submit(ctx, upgradehost.SubmitRequest{
		TaskID: prep.TaskID, Credential: "other-secret", Confirm: true, Initiator: "admin",
	}); !errors.Is(err, upgradehost.ErrInvalidCredential) {
		t.Fatalf("replay of a different secret: %v", err)
	}
	if f.runner.calls("bootstrap") != 1 {
		t.Fatalf("rejected replay launched again: %d", f.runner.calls("bootstrap"))
	}
	// The live digest retired on acceptance; only the accepted digest remains.
	task, _ := f.manager.store.Load(prep.TaskID)
	if task.CredentialDigest != "" || task.AcceptedDigest == "" {
		t.Fatal("credential digest was not retired into the accepted digest")
	}
}

func TestManagerSubmitUnknownBootstrapOutcome(t *testing.T) {
	f := newManagerFixture(t, nil)
	f.runner.fail["bootstrap"] = &TimeoutError{Argv: []string{"/bin/launchctl", "bootstrap"}}
	ctx := context.Background()
	prep, err := f.manager.Prepare(ctx, upgradehost.PrepareRequest{TargetVersion: "v1.1.0", Initiator: "admin"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.manager.Submit(ctx, upgradehost.SubmitRequest{
		TaskID: prep.TaskID, Credential: prep.Credential, Confirm: true, Initiator: "admin",
	})
	if !errors.Is(err, upgradehost.ErrNotAllowed) {
		t.Fatalf("expected not allowed, got %v", err)
	}
	task, _ := f.manager.store.Load(prep.TaskID)
	if task.State != upgradehost.StateNeedsAttention || !task.LaunchUnknown {
		t.Fatalf("expected needs_attention with unknown launch: %+v", task)
	}
	capability, _ := f.manager.Capability(ctx)
	if capability.Allowed {
		t.Fatal("capability must not allow a new task while unresolved")
	}
}

func TestManagerReconcilePaths(t *testing.T) {
	t.Run("expired confirmation retires lazily", func(t *testing.T) {
		base := time.Now()
		f := newManagerFixture(t, func() time.Time { return base })
		ctx := context.Background()
		prep, err := f.manager.Prepare(ctx, upgradehost.PrepareRequest{TargetVersion: "v1.1.0", Initiator: "admin"})
		if err != nil {
			t.Fatal(err)
		}
		f.manager.now = func() time.Time { return base.Add(confirmTTL + time.Minute) }
		if err := f.manager.Reconcile(); err != nil {
			t.Fatal(err)
		}
		task, _ := f.manager.store.Load(prep.TaskID)
		if task.State != upgradehost.StateConfirmationExpired {
			t.Fatalf("expected expired: %s", task.State)
		}
		capability, _ := f.manager.Capability(ctx)
		if !capability.Allowed {
			t.Fatal("expired confirmation must not block a new task")
		}
	})

	t.Run("cleanup verified before removal", func(t *testing.T) {
		f := newManagerFixture(t, nil)
		ctx := context.Background()
		prep, err := f.manager.Prepare(ctx, upgradehost.PrepareRequest{TargetVersion: "v1.1.0", Initiator: "admin"})
		if err != nil {
			t.Fatal(err)
		}
		task, _ := f.manager.store.Load(prep.TaskID)
		task.State = upgradehost.StateSucceeded
		task.CleanupPending = true
		if err := f.manager.store.Save(task); err != nil {
			t.Fatal(err)
		}
		if err := f.manager.Reconcile(); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(f.manager.store.taskDir(prep.TaskID)); !os.IsNotExist(err) {
			t.Fatalf("task dir not cleaned: %v", err)
		}
		view, err := f.manager.Task(ctx, upgradehost.TaskRequest{TaskID: prep.TaskID})
		if err != nil {
			t.Fatal(err)
		}
		if view.State != upgradehost.StateSucceeded || view.TaskID != prep.TaskID {
			t.Fatalf("cleaned task was not retained: %+v", view)
		}
		index, err := os.ReadFile(filepath.Join(f.root, "index.json"))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(index), prep.TaskID) {
			t.Fatalf("index missing task: %s", index)
		}
	})

	t.Run("installing without a live worker becomes attention", func(t *testing.T) {
		f := newManagerFixture(t, nil)
		taskID := mustPrepareAndSubmit(t, f)
		task, _ := f.manager.store.Load(taskID)
		task.State = upgradehost.StateInstalling
		task.Phase = "brew_upgrade"
		task.WorkerPID = 0
		if err := f.manager.store.Save(task); err != nil {
			t.Fatal(err)
		}
		if err := f.manager.Reconcile(); err != nil {
			t.Fatal(err)
		}
		task, _ = f.manager.store.Load(taskID)
		if task.State != upgradehost.StateNeedsAttention || task.ErrorKind != "worker_exited" {
			t.Fatalf("expected worker_exited: %+v", task)
		}
	})

	t.Run("accepted task past the total budget becomes attention", func(t *testing.T) {
		base := time.Now()
		f := newManagerFixture(t, func() time.Time { return base })
		taskID := mustPrepareAndSubmit(t, f)
		task, _ := f.manager.store.Load(taskID)
		task.State = upgradehost.StateVerifying
		task.WorkerPID = os.Getpid()
		ready, _ := json.Marshal(struct {
			PID     int       `json:"pid"`
			Started time.Time `json:"started"`
		}{PID: os.Getpid(), Started: base})
		if err := os.WriteFile(filepath.Join(f.manager.store.taskDir(taskID), "ready.json"), ready, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := f.manager.store.Save(task); err != nil {
			t.Fatal(err)
		}
		f.manager.now = func() time.Time { return base.Add(acceptedBudget + time.Minute) }
		if err := f.manager.Reconcile(); err != nil {
			t.Fatal(err)
		}
		task, _ = f.manager.store.Load(taskID)
		if task.State != upgradehost.StateNeedsAttention || task.ErrorKind != "budget_exhausted" {
			t.Fatalf("expected budget_exhausted: %+v", task)
		}
	})

	t.Run("queued without worker becomes attention after window", func(t *testing.T) {
		base := time.Now()
		f := newManagerFixture(t, func() time.Time { return base })
		taskID := mustPrepareAndSubmit(t, f)
		f.manager.now = func() time.Time { return base.Add(2*bootstrapTimeout + time.Minute) }
		if err := f.manager.Reconcile(); err != nil {
			t.Fatal(err)
		}
		task, _ := f.manager.store.Load(taskID)
		if task.State != upgradehost.StateNeedsAttention {
			t.Fatalf("expected needs_attention: %s", task.State)
		}
	})
}

func TestWorkerHappyPath(t *testing.T) {
	f := newManagerFixture(t, nil)
	ctx := context.Background()
	taskID := mustPrepareAndSubmit(t, f)

	// Health server proving the target build of the new keg.
	newKegBin := filepath.Join(f.prefix, "Cellar", "tokenlive", "1.1.0", "bin", "tokenlive")
	if err := os.MkdirAll(filepath.Dir(newKegBin), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(newKegBin, []byte("new-binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	server := newTestHealthServer(t, "1.1.0", newKegBin)
	task, _ := f.manager.store.Load(taskID)
	task.Port = portOf(t, server)
	_ = f.manager.store.Save(task)

	bootouts := 0
	worker := &Worker{
		Store: f.manager.store, Runner: f.runner, Launch: NewLaunchctl(f.runner),
		Health: NewHealthClient(), Prefix: f.prefix, Home: "/home", Now: time.Now,
		BootoutSelf: func(domain, label string) error { bootouts++; return nil },
	}
	if err := worker.Run(ctx, taskID); err != nil {
		t.Fatalf("worker run: %v", err)
	}
	task, _ = f.manager.store.Load(taskID)
	if task.State != upgradehost.StateSucceeded || !task.CleanupPending {
		t.Fatalf("task not succeeded: %+v", task)
	}
	if bootouts != 1 {
		t.Fatalf("bootout calls: %d", bootouts)
	}
	// The install target was the tap snapshot, never a bare upgrade.
	found := false
	for _, argv := range f.runner.outputs {
		if len(argv) >= 3 && argv[1] == "upgrade" && argv[2] == "--formula" {
			found = true
			snapshot := argv[3]
			if filepath.Dir(filepath.Dir(snapshot)) != filepath.Join(f.prefix, "Library", "Taps", "tokenlive", "homebrew-tokenlive", ".tokenlive-upgrade") {
				t.Fatalf("snapshot not in tap: %s", snapshot)
			}
		}
	}
	if !found {
		t.Fatal("no fixed brew upgrade observed")
	}
	if f.runner.calls("stop") != 1 || f.runner.calls("start") != 1 {
		t.Fatalf("service restart calls: stop=%d start=%d", f.runner.calls("stop"), f.runner.calls("start"))
	}
}

func TestWorkerFailures(t *testing.T) {
	t.Run("snapshot digest mismatch fails cleanly", func(t *testing.T) {
		f := newManagerFixture(t, nil)
		taskID := mustPrepareAndSubmit(t, f)
		snap := filepath.Join(f.manager.store.taskDir(taskID), "snapshot", "tokenlive.rb")
		if err := os.WriteFile(snap, []byte("tampered"), 0o600); err != nil {
			t.Fatal(err)
		}
		worker := &Worker{Store: f.manager.store, Runner: f.runner, Launch: NewLaunchctl(f.runner),
			Health: NewHealthClient(), Prefix: f.prefix, Home: "/home", Now: time.Now}
		if err := worker.Run(context.Background(), taskID); err != nil {
			t.Fatalf("worker returned: %v", err)
		}
		task, _ := f.manager.store.Load(taskID)
		if task.State != upgradehost.StateFailed || task.FailureStage != "tap_snapshot" {
			t.Fatalf("expected clean snapshot failure: %+v", task)
		}
		if f.runner.calls("upgrade") != 0 {
			t.Fatal("brew upgrade must not run after snapshot failure")
		}
	})

	t.Run("brew upgrade timeout is needs_attention", func(t *testing.T) {
		f := newManagerFixture(t, nil)
		taskID := mustPrepareAndSubmit(t, f)
		f.runner.fail["upgrade"] = &TimeoutError{Argv: []string{"brew", "upgrade"}}
		worker := &Worker{Store: f.manager.store, Runner: f.runner, Launch: NewLaunchctl(f.runner),
			Health: NewHealthClient(), Prefix: f.prefix, Home: "/home", Now: time.Now}
		_ = worker.Run(context.Background(), taskID)
		task, _ := f.manager.store.Load(taskID)
		if task.State != upgradehost.StateNeedsAttention || task.FailureStage != "brew_upgrade" {
			t.Fatalf("expected needs_attention at brew_upgrade: %+v", task)
		}
		if f.runner.calls("stop") != 0 {
			t.Fatal("service stop must not run after install timeout")
		}
	})

	t.Run("brew upgrade exit failure records failed", func(t *testing.T) {
		f := newManagerFixture(t, nil)
		taskID := mustPrepareAndSubmit(t, f)
		f.runner.fail["upgrade"] = &ExitError{Argv: []string{"brew", "upgrade"}, Code: 1, Output: "Error: permission denied\n"}
		worker := &Worker{Store: f.manager.store, Runner: f.runner, Launch: NewLaunchctl(f.runner),
			Health: NewHealthClient(), Prefix: f.prefix, Home: "/home", Now: time.Now}
		_ = worker.Run(context.Background(), taskID)
		task, _ := f.manager.store.Load(taskID)
		if task.State != upgradehost.StateFailed || task.ErrorKind != "brew_upgrade_failed" {
			t.Fatalf("expected failed/brew_upgrade_failed: %+v", task)
		}
		if task.Detail == "" {
			t.Fatal("expected bounded diagnostic detail")
		}
	})

	t.Run("service stop timeout never starts the service", func(t *testing.T) {
		f := newManagerFixture(t, nil)
		taskID := mustPrepareAndSubmit(t, f)
		f.runner.fail["stop"] = &TimeoutError{Argv: []string{"brew", "services", "stop"}}
		worker := &Worker{Store: f.manager.store, Runner: f.runner, Launch: NewLaunchctl(f.runner),
			Health: NewHealthClient(), Prefix: f.prefix, Home: "/home", Now: time.Now}
		_ = worker.Run(context.Background(), taskID)
		task, _ := f.manager.store.Load(taskID)
		if task.State != upgradehost.StateNeedsAttention || task.FailureStage != "service_stop" {
			t.Fatalf("expected needs_attention at service_stop: %+v", task)
		}
		if f.runner.calls("start") != 0 {
			t.Fatal("start must never follow an unresolved stop")
		}
	})

	t.Run("unchanged service pid never verifies", func(t *testing.T) {
		f := newManagerFixture(t, nil)
		f.runner.keepServicePID = true
		taskID := mustPrepareAndSubmit(t, f)
		newKegBin := filepath.Join(f.prefix, "Cellar", "tokenlive", "1.1.0", "bin", "tokenlive")
		if err := os.MkdirAll(filepath.Dir(newKegBin), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(newKegBin, []byte("new-binary"), 0o755); err != nil {
			t.Fatal(err)
		}
		server := newTestHealthServer(t, "1.1.0", newKegBin)
		task, _ := f.manager.store.Load(taskID)
		task.Port = portOf(t, server)
		_ = f.manager.store.Save(task)
		start := time.Now()
		worker := &Worker{Store: f.manager.store, Runner: f.runner, Launch: NewLaunchctl(f.runner),
			Health: NewHealthClient(), Prefix: f.prefix, Home: "/home",
			Now:       func() time.Time { return start.Add(time.Since(start) * 10000) },
			pollEvery: time.Millisecond,
		}
		_ = worker.Run(context.Background(), taskID)
		task, _ = f.manager.store.Load(taskID)
		if task.State != upgradehost.StateNeedsAttention || task.FailureStage != "verify" {
			t.Fatalf("expected needs_attention at verify: %+v", task)
		}
		if !strings.Contains(task.Detail, "service_pid_unchanged") {
			t.Fatalf("expected unchanged pid diagnostic: %+v", task)
		}
	})

	t.Run("wrong served version never verifies", func(t *testing.T) {
		f := newManagerFixture(t, nil)
		taskID := mustPrepareAndSubmit(t, f)
		// A same-port server claiming the wrong version and foreign binary.
		server := newTestHealthServer(t, "1.0.0", "/opt/other/bin/tokenlive")
		task, _ := f.manager.store.Load(taskID)
		task.Port = portOf(t, server)
		_ = f.manager.store.Save(task)
		start := time.Now()
		worker := &Worker{Store: f.manager.store, Runner: f.runner, Launch: NewLaunchctl(f.runner),
			Health: NewHealthClient(), Prefix: f.prefix, Home: "/home",
			Now:       func() time.Time { return start.Add(time.Since(start) * 10000) }, // accelerated clock
			pollEvery: time.Millisecond,
		}
		_ = worker.Run(context.Background(), taskID)
		task, _ = f.manager.store.Load(taskID)
		if task.State != upgradehost.StateNeedsAttention || task.FailureStage != "verify" {
			t.Fatalf("expected needs_attention at verify: %+v", task)
		}
	})
}

func TestFormulaValidation(t *testing.T) {
	if stable, raw, err := ValidateFormula([]byte(testFormula)); err != nil || stable != "v1.1.0" || raw != "1.1.0" {
		t.Fatalf("valid formula rejected: %q %q %v", stable, raw, err)
	}
	for name, mutate := range map[string]func(string) string{
		"depends_on": func(s string) string { return s + "\n  depends_on \"go\"\n" },
		"head":       func(s string) string { return s + "\n  head \"https://github.com/tokenlive/x.git\"\n" },
		"foreign_url": func(s string) string {
			return replaceOnce(s, "github.com/tokenlive/tokenlive-standalone/releases", "evil.example.com/x/releases")
		},
		"two_versions": func(s string) string { return s + "\n  version \"9.9.9\"\n" },
		"prerelease":   func(s string) string { return replaceOnce(s, `"1.1.0"`, `"1.1.0-rc1"`) },
		"missing_sha": func(s string) string {
			return replaceOnce(s, `sha256 "ebe7940542a5024e33b078e2ad1713bfdfe3065278c4f771d2430ee5b2b6f870"`, "")
		},
	} {
		if _, _, err := ValidateFormula([]byte(mutate(testFormula))); err == nil {
			t.Fatalf("%s: expected rejection", name)
		}
	}
}

func TestLaunchctlParsing(t *testing.T) {
	t.Run("running pid parsed", func(t *testing.T) {
		runner := &staticRunner{output: "  4242\t0\tcom.tokenlive.upgrade.x\n"}
		pid, running, err := NewLaunchctl(runner).ListPID(context.Background(), "x")
		if err != nil || !running || pid != 4242 {
			t.Fatalf("pid=%d running=%v err=%v", pid, running, err)
		}
	})
	t.Run("not running dash", func(t *testing.T) {
		runner := &staticRunner{output: "  -\t1\tcom.tokenlive.upgrade.x\n"}
		pid, running, err := NewLaunchctl(runner).ListPID(context.Background(), "x")
		if err != nil || running || pid != 0 {
			t.Fatalf("pid=%d running=%v err=%v", pid, running, err)
		}
	})
	t.Run("unknown label", func(t *testing.T) {
		runner := &staticRunner{exitErr: &ExitError{Argv: []string{"/bin/launchctl", "list"}, Code: 3,
			Output: "Could not find service \"x\" in domain\n"}}
		if pid, running, err := NewLaunchctl(runner).ListPID(context.Background(), "x"); err != nil || running || pid != 0 {
			t.Fatalf("pid=%d running=%v err=%v", pid, running, err)
		}
	})
	t.Run("garbage output rejected", func(t *testing.T) {
		runner := &staticRunner{output: "PID	Status	Label\n4242	0	x\n"}
		if _, _, err := NewLaunchctl(runner).ListPID(context.Background(), "x"); err == nil {
			t.Fatal("expected parse failure")
		}
	})
	t.Run("bootstrap tolerates already bootstrapped", func(t *testing.T) {
		runner := &staticRunner{exitErr: &ExitError{Argv: []string{"/bin/launchctl", "bootstrap"}, Code: 37,
			Output: "Bootstrap failed: 5: Input/output error\nalready bootstrapped\n"}}
		if err := NewLaunchctl(runner).Bootstrap(context.Background(), "gui/501", "/x.plist"); err != nil {
			t.Fatalf("bootstrap: %v", err)
		}
	})
}

func TestTaskLogCapMarksTruncation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "worker.log")
	body := strings.Repeat("x", maxTaskLogBytes+32)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := capLogFile(path); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) > maxTaskLogBytes {
		t.Fatalf("log still over cap: %d", len(got))
	}
	if !strings.HasPrefix(string(got), "\n[truncated]\n") {
		t.Fatalf("missing truncation marker: %q", got[:20])
	}
}

func TestStateStoreIntegrity(t *testing.T) {
	f := newManagerFixture(t, nil)
	prep, err := f.manager.Prepare(context.Background(), upgradehost.PrepareRequest{TargetVersion: "v1.1.0", Initiator: "admin"})
	if err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(f.manager.store.taskDir(prep.TaskID), "snapshot", "tokenlive.rb"))
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(body)
	stored, _ := f.manager.store.Load(prep.TaskID)
	if hex.EncodeToString(sum[:]) != stored.FormulaSHA256 {
		t.Fatal("stored digest mismatch")
	}
	// Corrupt record surfaces as needs_attention, never silence.
	dir := f.manager.store.taskDir("20260101T000000-cafecafe")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "task.json"), []byte("{broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	tasks, err := f.manager.store.List()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, task := range tasks {
		if task.TaskID == "20260101T000000-cafecafe" && task.State == upgradehost.StateNeedsAttention {
			found = true
		}
	}
	if !found {
		t.Fatal("corrupt record did not surface as needs_attention")
	}
}

func TestIdentityBits(t *testing.T) {
	now := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	id := newTaskID(now)
	if len(id) != len("20060102T150405")+1+16 {
		t.Fatalf("unexpected task id shape: %s", id)
	}
	label := taskLabel("abcdef", id)
	if label != "com.tokenlive.upgrade.abcdef."+id {
		t.Fatalf("unexpected label: %s", label)
	}
	if releaseURL("1.2.3") != "https://github.com/tokenlive/tokenlive-standalone/releases/tag/v1.2.3" {
		t.Fatalf("unexpected release url: %s", releaseURL("1.2.3"))
	}
	if releaseURL("v1.2.3") != "https://github.com/tokenlive/tokenlive-standalone/releases/tag/v1.2.3" {
		t.Fatalf("unexpected release url: %s", releaseURL("v1.2.3"))
	}
	secret, err := newSecret()
	if err != nil || len(secret) != 64 {
		t.Fatalf("secret: %v", err)
	}
	if hashCredential(secret) == hashCredential(secret+"x") {
		t.Fatal("credential digest collision")
	}
	if !upgradehost.StateTerminal(upgradehost.StateSucceeded) || upgradehost.StateTerminal(upgradehost.StateQueued) {
		t.Fatal("terminal classification broken")
	}
}

func mustPrepareAndSubmit(t *testing.T, f *managerFixture) string {
	t.Helper()
	prep, err := f.manager.Prepare(context.Background(), upgradehost.PrepareRequest{TargetVersion: "v1.1.0", Initiator: "admin"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.manager.Submit(context.Background(), upgradehost.SubmitRequest{
		TaskID: prep.TaskID, Credential: prep.Credential, Confirm: true, Initiator: "admin",
	}); err != nil {
		t.Fatal(err)
	}
	return prep.TaskID
}

func replaceOnce(s, old, new string) string {
	for i := 0; i+len(old) <= len(s); i++ {
		if s[i:i+len(old)] == old {
			return s[:i] + new + s[i+len(old):]
		}
	}
	return s
}

type staticRunner struct {
	output  string
	exitErr error
}

func (s *staticRunner) Run(context.Context, []string, []string, time.Duration) ([]byte, error) {
	if s.exitErr != nil {
		return []byte(s.output), s.exitErr
	}
	return []byte(s.output), nil
}
