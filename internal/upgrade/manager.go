package upgrade

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/tokenlive/tokenlive-admin/pkg/upgradehost"
)

// Budgets from the design defaults. They bound supervision only; exceeding
// one is "budget exhausted", never proof that external processes ended.
const (
	confirmTTL       = 5 * time.Minute
	fetchTimeout     = 15 * time.Second
	bootstrapTimeout = 15 * time.Second
	// acceptedBudget is the design's total limit for a task that has left
	// confirmation. Exceeding it does not prove the external command ended.
	acceptedBudget = 30 * time.Minute
)

// Manager implements upgradehost.Host on top of the verified installation.
// It owns prepare/submit/task flows and reconciliation; execution happens in
// the separate one-shot worker process.
type Manager struct {
	store   *Store
	install *Installation
	runner  Runner
	launch  *Launchctl
	fetch   func(ctx context.Context) (body []byte, stable, raw string, err error)
	now     func() time.Time

	installID  string
	executable string // current binary, copied into task dirs as the executor
}

// NewManager builds the host manager. upgradesRoot is
// <var>/tokenlive/upgrades/<install-id>; executable is the current binary.
func NewManager(upgradesRoot string, install *Installation, runner Runner, executable string) *Manager {
	if runner == nil {
		runner = ExecRunner{}
	}
	m := &Manager{
		store:      NewStore(upgradesRoot),
		install:    install,
		runner:     runner,
		launch:     NewLaunchctl(runner),
		fetch:      defaultFetch,
		now:        time.Now,
		installID:  installIDFor(install),
		executable: executable,
	}
	// One time domain: every persisted timestamp comes from m.now, so
	// reconciliation compares like with like even under test clocks.
	m.store.Now = func() time.Time { return m.now() }
	return m
}

// installIDFor derives the opaque, stable installation identifier from the
// install's own paths. It never contains usernames or full paths.
func installIDFor(install *Installation) string {
	sum := sha256.Sum256([]byte(install.Prefix + "\x00" + install.Keg))
	return hex.EncodeToString(sum[:8])
}

// InstallIDFor exposes the identifier to the CLI, which must locate the same
// store the running service uses.
func InstallIDFor(install *Installation) string { return installIDFor(install) }

// TaskSummary is the CLI-facing snapshot of one persisted task.
type TaskSummary struct {
	TaskID         string `json:"task_id"`
	State          string `json:"state"`
	CurrentVersion string `json:"current_version,omitempty"`
	TargetVersion  string `json:"target_version,omitempty"`
	FailureStage   string `json:"failure_stage,omitempty"`
	ErrorKind      string `json:"error_kind,omitempty"`
	Detail         string `json:"detail,omitempty"`
	UpdatedAt      string `json:"updated_at,omitempty"`
}

// Snapshot lists tasks for the CLI after reconciliation.
func (m *Manager) Snapshot() ([]TaskSummary, error) {
	tasks, err := m.reconciledTasks()
	if err != nil {
		return nil, err
	}
	out := make([]TaskSummary, 0, len(tasks))
	for _, t := range tasks {
		out = append(out, TaskSummary{
			TaskID:         t.TaskID,
			State:          t.State,
			CurrentVersion: t.CurrentVersion,
			TargetVersion:  t.TargetVersion,
			FailureStage:   t.FailureStage,
			ErrorKind:      t.ErrorKind,
			Detail:         t.Detail,
			UpdatedAt:      t.UpdatedAt.UTC().Format(time.RFC3339),
		})
	}
	return out, nil
}

func defaultFetch(ctx context.Context) (body []byte, stable, raw string, err error) {
	ctx, cancel := context.WithTimeout(ctx, fetchTimeout)
	defer cancel()
	return FetchFormula(ctx, nil)
}

// Capability answers the read-only capability question. It never mutates
// state except lazy expiry reconciliation.
func (m *Manager) Capability(ctx context.Context) (upgradehost.Capability, error) {
	tasks, err := m.reconciledTasks()
	if err != nil {
		return upgradehost.Capability{}, err
	}
	capability := upgradehost.Capability{Supported: true, Allowed: true}
	if active := ActiveTask(tasks, m.now()); active != nil {
		capability.Allowed = false
		if active.State == upgradehost.StateNeedsAttention {
			capability.Reasons = append(capability.Reasons, upgradehost.ReasonTaskPending)
		} else {
			capability.Reasons = append(capability.Reasons, upgradehost.ReasonTaskActive)
		}
		view := active.View()
		capability.ActiveTask = &view
	}
	return capability, nil
}

// Prepare validates the target against a fresh candidate and creates the
// awaiting-confirmation task with a one-time credential.
func (m *Manager) Prepare(ctx context.Context, req upgradehost.PrepareRequest) (upgradehost.Preparation, error) {
	if req.TargetVersion == "" {
		return upgradehost.Preparation{}, upgradehost.ErrTargetChanged
	}
	lock, ok, err := TryLock(m.store.Root())
	if err != nil {
		return upgradehost.Preparation{}, err
	}
	defer lock.Release()
	if !ok {
		return upgradehost.Preparation{}, fmt.Errorf("%w: another upgrade task is active", upgradehost.ErrTaskConflict)
	}

	tasks, err := m.reconciledTasks()
	if err != nil {
		return upgradehost.Preparation{}, err
	}
	if ActiveTask(tasks, m.now()) != nil {
		return upgradehost.Preparation{}, fmt.Errorf("%w: an upgrade task already exists", upgradehost.ErrTaskConflict)
	}

	body, stable, raw, err := m.fetch(ctx)
	if err != nil {
		return upgradehost.Preparation{}, fmt.Errorf("candidate unavailable: %w", err)
	}
	if stable != req.TargetVersion {
		return upgradehost.Preparation{}, fmt.Errorf("%w: candidate is %s, not %s", upgradehost.ErrTargetChanged, stable, req.TargetVersion)
	}

	credential, err := newSecret()
	if err != nil {
		return upgradehost.Preparation{}, err
	}
	now := m.now()
	task := &Task{
		TaskID:           newTaskID(now),
		InstallID:        m.installID,
		State:            upgradehost.StateAwaitingConfirmation,
		CurrentVersion:   m.install.Current.Version,
		TargetVersion:    stable,
		ReleaseURL:       releaseURL(stable),
		Initiator:        req.Initiator,
		FormulaSHA256:    sha256Hex(body),
		CredentialDigest: hashCredential(credential),
		CredentialExpiry: now.Add(confirmTTL),
		Port:             m.install.Port,
		ServiceLabel:     m.install.ServiceLabel,
		Domain:           m.install.Domain,
		CreatedAt:        now.UTC(),
		UpdatedAt:        now.UTC(),
	}
	if raw != stable {
		task.TargetKegVersion = raw
	} else {
		task.TargetKegVersion = stable
	}
	if err := m.store.Create(task, body); err != nil {
		return upgradehost.Preparation{}, err
	}
	return upgradehost.Preparation{
		TaskID:           task.TaskID,
		CurrentVersion:   task.CurrentVersion,
		TargetVersion:    task.TargetVersion,
		ReleaseURL:       task.ReleaseURL,
		ConfirmExpiresIn: int(confirmTTL / time.Second),
		Credential:       credential,
		RestartWarning:   true,
	}, nil
}

// Submit consumes the confirmation and starts the one-shot worker job.
func (m *Manager) Submit(ctx context.Context, req upgradehost.SubmitRequest) (upgradehost.TaskView, error) {
	if !req.Confirm {
		return upgradehost.TaskView{}, fmt.Errorf("%w: missing confirm flag", upgradehost.ErrInvalidCredential)
	}
	lock, ok, err := TryLock(m.store.Root())
	if err != nil {
		return upgradehost.TaskView{}, err
	}
	defer lock.Release()
	if !ok {
		return upgradehost.TaskView{}, fmt.Errorf("%w: another upgrade task is active", upgradehost.ErrTaskConflict)
	}

	task, err := m.store.Load(req.TaskID)
	if err != nil {
		return upgradehost.TaskView{}, err
	}
	if task == nil {
		return upgradehost.TaskView{}, fmt.Errorf("%w: unknown task", upgradehost.ErrInvalidCredential)
	}
	if task.Initiator != req.Initiator {
		return upgradehost.TaskView{}, fmt.Errorf("%w: credential bound to another user", upgradehost.ErrInvalidCredential)
	}
	digest := hashCredential(req.Credential)
	if task.State == upgradehost.StateQueued {
		// Idempotent double submit only for the same one-time secret.
		// A different secret must not succeed just because the task was
		// already accepted.
		if task.AcceptedDigest == "" || digest != task.AcceptedDigest {
			return upgradehost.TaskView{}, fmt.Errorf("%w: credential already consumed", upgradehost.ErrInvalidCredential)
		}
		return task.View(), nil
	}
	if task.State != upgradehost.StateAwaitingConfirmation {
		return upgradehost.TaskView{}, fmt.Errorf("%w: task is %s", upgradehost.ErrTaskConflict, task.State)
	}
	if m.now().After(task.CredentialExpiry) {
		task.State = upgradehost.StateConfirmationExpired
		_ = m.store.Save(task)
		return upgradehost.TaskView{}, fmt.Errorf("%w: confirmation window elapsed", upgradehost.ErrConfirmationExpired)
	}
	if digest != task.CredentialDigest {
		return upgradehost.TaskView{}, fmt.Errorf("%w: credential mismatch", upgradehost.ErrInvalidCredential)
	}

	// queued precedes any external action so a crash here leaves a
	// reconcilable record, never a running job without a task.
	task.State = upgradehost.StateQueued
	task.Phase = "launching"
	task.AcceptedDigest = digest
	task.CredentialDigest = "" // one-time use; the digest retires with the state
	if err := m.store.Save(task); err != nil {
		return upgradehost.TaskView{}, err
	}
	if err := m.launchTask(ctx, task); err != nil {
		// Launch did not provably start a job: the task becomes an
		// unresolved, human-inspectable record; never silently retried.
		task.Phase = "launch"
		task.State = upgradehost.StateNeedsAttention
		task.ErrorKind = "launch_failed"
		task.Detail = truncate(err.Error(), 200)
		_ = m.store.Save(task)
		return upgradehost.TaskView{}, fmt.Errorf("%w: %v", upgradehost.ErrNotAllowed, err)
	}
	return task.View(), nil
}

// Task returns one task (or the latest) after reconciliation.
func (m *Manager) Task(ctx context.Context, req upgradehost.TaskRequest) (upgradehost.TaskView, error) {
	if req.TaskID == "" {
		tasks, err := m.reconciledTasks()
		if err != nil {
			return upgradehost.TaskView{}, err
		}
		if len(tasks) == 0 {
			return upgradehost.TaskView{}, fmt.Errorf("%w: no upgrade tasks recorded", upgradehost.ErrUnsupported)
		}
		return tasks[len(tasks)-1].View(), nil
	}
	tasks, err := m.reconciledTasks()
	if err != nil {
		return upgradehost.TaskView{}, err
	}
	for _, task := range tasks {
		if task.TaskID == req.TaskID {
			return task.View(), nil
		}
	}
	return upgradehost.TaskView{}, fmt.Errorf("%w: unknown task", upgradehost.ErrInvalidCredential)
}

func (m *Manager) reconciledTasks() ([]*Task, error) {
	tasks, err := m.store.List()
	if err != nil {
		return nil, err
	}
	m.reconcile(tasks, true)
	return m.store.List()
}

// Reconcile performs lazy expiry, unknown-outcome resolution and verified
// cleanup. Safe to call from API reads, startup and the CLI.
func (m *Manager) Reconcile() error {
	_, err := m.reconciledTasks()
	return err
}

func (m *Manager) reconcile(tasks []*Task, allowCleanup bool) {
	now := m.now()
	for _, task := range tasks {
		switch {
		case task.State == upgradehost.StateAwaitingConfirmation && now.After(task.CredentialExpiry):
			task.State = upgradehost.StateConfirmationExpired
			_ = m.store.Save(task)
		case task.State == upgradehost.StateQueued:
			m.reconcileLaunch(task, now)
		case inflightState(task.State):
			m.reconcileInflight(task, now)
		case allowCleanup && task.CleanupPending && !task.Cleaned && upgradehost.StateTerminal(task.State):
			// Verify the job is really gone before dropping mutable files.
			label := taskLabel(task.InstallID, task.TaskID)
			ctx, cancel := context.WithTimeout(context.Background(), bootstrapTimeout)
			err := m.launch.Bootout(ctx, m.install.Domain, label)
			cancel()
			if err == nil {
				_ = capLogFile(filepath.Join(m.store.taskDir(task.TaskID), "worker.log"))
				_ = m.store.Cleanup(task.TaskID)
			}
		}
	}
}

// reconcileLaunch resolves queued tasks whose worker never reported in.
// Bootstrap errors are unknown outcomes: reconciliation decides by evidence,
// never by retrying the launch.
func inflightState(state string) bool {
	switch state {
	case upgradehost.StateDownloading, upgradehost.StateInstalling, upgradehost.StateRestarting, upgradehost.StateVerifying:
		return true
	}
	return false
}

// reconcileInflight marks a started task needs_attention when its worker has
// disappeared or the accepted-task budget has elapsed. It never kills the
// worker or deletes a Homebrew lock; an uncertain outcome stays uncertain.
func (m *Manager) reconcileInflight(task *Task, now time.Time) {
	if m.workerAlive(task) && now.Sub(task.CreatedAt) <= acceptedBudget {
		return
	}
	task.State = upgradehost.StateNeedsAttention
	task.Phase = ""
	if now.Sub(task.CreatedAt) > acceptedBudget {
		task.ErrorKind = "budget_exhausted"
		task.Detail = "accepted task exceeded 30 minutes without a proven terminal state"
	} else {
		task.ErrorKind = "worker_exited"
		task.Detail = "worker disappeared before the task reached a terminal state"
	}
	_ = m.store.Save(task)
}

func (m *Manager) reconcileLaunch(task *Task, now time.Time) {
	if m.workerAlive(task) {
		if task.LaunchUnknown {
			task.LaunchUnknown = false
			_ = m.store.Save(task)
		}
		return
	}
	if now.Sub(task.UpdatedAt) <= 2*bootstrapTimeout {
		return // launch window still open
	}
	task.State = upgradehost.StateNeedsAttention
	if task.LaunchUnknown {
		task.ErrorKind = "launch_unresolved"
		task.Detail = "bootstrap result unknown and no worker heartbeat appeared"
	} else {
		task.ErrorKind = "worker_never_started"
	}
	_ = m.store.Save(task)
}

// workerAlive checks the heartbeat record's PID with a signal-0 probe. It is
// evidence, not proof: only same-UID liveness, closed against records from a
// different task by re-reading ready.json.
func (m *Manager) workerAlive(task *Task) bool {
	if task.WorkerPID == 0 {
		return false
	}
	data, err := os.ReadFile(filepath.Join(m.store.taskDir(task.TaskID), "ready.json"))
	if err != nil {
		return false
	}
	var ready struct {
		PID     int       `json:"pid"`
		Started time.Time `json:"started"`
	}
	if err := json.Unmarshal(data, &ready); err != nil || ready.PID != task.WorkerPID {
		return false
	}
	return syscall.Kill(task.WorkerPID, 0) == nil
}

// launchTask copies the executor, writes the plist and bootstraps the job.
func (m *Manager) launchTask(ctx context.Context, task *Task) error {
	dir := m.store.taskDir(task.TaskID)
	executor := filepath.Join(dir, "executor")
	if err := copyFile(m.executable, executor, 0o700); err != nil {
		return fmt.Errorf("copy executor: %w", err)
	}
	label := taskLabel(task.InstallID, task.TaskID)
	plistBytes, err := taskPlist(label, executor,
		[]string{"upgrade-worker", "--task-id", task.TaskID},
		dir, filepath.Join(dir, "worker.log"))
	if err != nil {
		return err
	}
	plistPath := filepath.Join(dir, "task.plist")
	if err := writeFileAtomic(plistPath, plistBytes, 0o600); err != nil {
		return err
	}
	task.LaunchAttempt++
	bootCtx, cancel := context.WithTimeout(ctx, bootstrapTimeout)
	defer cancel()
	if err := m.launch.Bootstrap(bootCtx, m.install.Domain, plistPath); err != nil {
		task.LaunchUnknown = true
		_ = m.store.Save(task)
		return fmt.Errorf("bootstrap outcome unknown: %w", err)
	}
	return m.store.Save(task)
}

func taskLabel(installID, taskID string) string {
	return "com.tokenlive.upgrade." + installID + "." + taskID
}

func releaseURL(version string) string {
	return "https://github.com/tokenlive/tokenlive-standalone/releases/tag/v" +
		url.PathEscape(strings.TrimPrefix(version, "v"))
}

func newSecret() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

func newTaskID(now time.Time) string {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return now.UTC().Format("20060102T150405.000000000")
	}
	return now.UTC().Format("20060102T150405") + "-" + hex.EncodeToString(buf)
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func copyFile(src, dst string, mode os.FileMode) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	return writeFileAtomic(dst, data, mode)
}
