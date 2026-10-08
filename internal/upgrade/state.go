package upgrade

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/tokenlive/tokenlive-admin/pkg/upgradehost"
)

// Task is the persisted upgrade task record. It is the single source of truth
// shared by the manager (web API) and the worker (launchd job): both processes
// read and write it atomically, so every mutation is save-and-rename.
type Task struct {
	TaskID         string `json:"task_id"`
	InstallID      string `json:"install_id"`
	State          string `json:"state"`
	CurrentVersion string `json:"current_version"`
	TargetVersion  string `json:"target_version"`
	ReleaseURL     string `json:"release_url"`
	Initiator      string `json:"initiator"`

	// Target fixation: digest of the exact Formula bytes to install.
	FormulaSHA256 string `json:"formula_sha256"`
	// TargetKegVersion is the raw Formula version naming the Cellar keg
	// (usually identical to TargetVersion).
	TargetKegVersion string `json:"target_keg_version,omitempty"`

	// CredentialDigest is sha256(hex) of the one-time confirmation secret.
	// The plaintext is returned once by Prepare and never persisted.
	// After acceptance the digest moves to AcceptedDigest so a replay must
	// still present the same secret, and a different secret cannot pass
	// just because the task is already queued.
	CredentialDigest string    `json:"credential_digest,omitempty"`
	AcceptedDigest   string    `json:"accepted_digest,omitempty"`
	CredentialExpiry time.Time `json:"credential_expiry,omitempty"`

	// Runtime context captured at prepare time.
	Port         int    `json:"port"`
	ServiceLabel string `json:"service_label"`
	Domain       string `json:"domain"`
	// ServicePID is the brew-services PID observed before stop. Success
	// requires launchd to report a different PID after restart.
	ServicePID int `json:"service_pid,omitempty"`

	// Phase records the concrete step inside a non-terminal state.
	Phase string `json:"phase,omitempty"`
	// FailureStage/ErrorKind/Detail survive as the bounded diagnostic record.
	FailureStage string `json:"failure_stage,omitempty"`
	ErrorKind    string `json:"error_kind,omitempty"`
	Detail       string `json:"detail,omitempty"`

	// Worker heartbeat written by the one-shot job.
	WorkerPID     int       `json:"worker_pid,omitempty"`
	WorkerStarted time.Time `json:"worker_started,omitempty"`
	LaunchAttempt int       `json:"launch_attempt,omitempty"`
	LaunchUnknown bool      `json:"launch_unknown,omitempty"`

	// Cleanup marks the two-phase self-removal of the launchd job.
	CleanupPending bool `json:"cleanup_pending,omitempty"`
	// Cleaned is set only on the retained index copy, after working files
	// are gone. Reconciliation must not try to boot out that copy again.
	Cleaned     bool      `json:"cleaned,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
	SucceededAt time.Time `json:"succeeded_at,omitempty"`
}

func (t *Task) touch() { t.UpdatedAt = time.Now().UTC() }

// View converts the record into the safe admin-facing projection.
func (t *Task) View() upgradehost.TaskView {
	return upgradehost.TaskView{
		TaskID:         t.TaskID,
		State:          t.State,
		CurrentVersion: t.CurrentVersion,
		TargetVersion:  t.TargetVersion,
		ReleaseURL:     t.ReleaseURL,
		Initiator:      t.Initiator,
		Phase:          t.Phase,
		FailureStage:   t.FailureStage,
		ErrorKind:      t.ErrorKind,
		Detail:         t.Detail,
		CreatedAt:      t.CreatedAt.UTC().Format(time.RFC3339),
		UpdatedAt:      t.UpdatedAt.UTC().Format(time.RFC3339),
		SucceededAt:    tsOrEmpty(t.SucceededAt),
	}
}

func tsOrEmpty(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

// hashCredential digests a confirmation secret. Only the digest is stored.
func hashCredential(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}

// Store persists tasks under <upgrades-root>/<task-id>/task.json.
// Directories are 0700 and files 0600; writes are temp+rename+fsync.
type Store struct {
	root string
	// Now is the store clock; the manager injects its own so task
	// timestamps and reconcile comparisons share one time domain.
	Now func() time.Time
}

func NewStore(root string) *Store {
	return &Store{root: root, Now: time.Now}
}

func (s *Store) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Store) Root() string { return s.root }

// taskDir returns the directory of one task without creating it.
func (s *Store) taskDir(taskID string) string {
	return filepath.Join(s.root, taskID)
}

func (s *Store) taskPath(taskID string) string {
	return filepath.Join(s.taskDir(taskID), "task.json")
}

// Create writes the initial task record and its directory layout.
func (s *Store) Create(task *Task, formulaBytes []byte) error {
	dir := s.taskDir(task.TaskID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if len(formulaBytes) > 0 {
		snapDir := filepath.Join(dir, "snapshot")
		if err := os.MkdirAll(snapDir, 0o700); err != nil {
			return err
		}
		if err := writeFileAtomic(filepath.Join(snapDir, "tokenlive.rb"), formulaBytes, 0o600); err != nil {
			return err
		}
	}
	return s.Save(task)
}

// Save atomically persists the task record. UpdatedAt comes from the store
// clock so manager and worker share one time domain per process.
func (s *Store) Save(task *Task) error {
	task.UpdatedAt = s.now().UTC()
	data, err := json.MarshalIndent(task, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(s.taskPath(task.TaskID), data, 0o600)
}

// Load reads one task record. A missing task is (nil, nil).
func (s *Store) Load(taskID string) (*Task, error) {
	if taskID == "" || taskID != filepath.Base(taskID) {
		return nil, fmt.Errorf("invalid task id")
	}
	data, err := os.ReadFile(s.taskPath(taskID))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var task Task
	if err := json.Unmarshal(data, &task); err != nil {
		return nil, fmt.Errorf("corrupt task record %s: %w", taskID, err)
	}
	return &task, nil
}

// List returns all persisted task records sorted by creation time.
func (s *Store) List() ([]*Task, error) {
	entries, err := os.ReadDir(s.root)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var tasks []*Task
	indexed, indexErr := s.readIndex()
	if indexErr == nil {
		for i := range indexed {
			tasks = append(tasks, taskFromView(indexed[i]))
		}
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		task, err := s.Load(entry.Name())
		if err != nil {
			// Corrupt records surface as unresolved work, never as silence.
			tasks = append(tasks, &Task{
				TaskID: entry.Name(), State: upgradehost.StateNeedsAttention,
				ErrorKind: "corrupt_record", Detail: truncate(err.Error(), 200),
				UpdatedAt: time.Now().UTC(),
			})
			continue
		}
		if task != nil {
			tasks = append(tasks, task)
		}
	}
	sortTasks(tasks)
	return dedupeTasks(tasks), nil
}

func taskFromView(view upgradehost.TaskView) *Task {
	task := &Task{
		TaskID: view.TaskID, State: view.State,
		CurrentVersion: view.CurrentVersion, TargetVersion: view.TargetVersion,
		ReleaseURL: view.ReleaseURL, Initiator: view.Initiator,
		Phase: view.Phase, FailureStage: view.FailureStage,
		ErrorKind: view.ErrorKind, Detail: view.Detail,
	}
	task.Cleaned = true
	task.CreatedAt, _ = time.Parse(time.RFC3339, view.CreatedAt)
	task.UpdatedAt, _ = time.Parse(time.RFC3339, view.UpdatedAt)
	task.SucceededAt, _ = time.Parse(time.RFC3339, view.SucceededAt)
	return task
}

// dedupeTasks keeps the live directory record when the same id also exists
// in the retained index. Input must already be ordered by creation time.
func dedupeTasks(tasks []*Task) []*Task {
	seen := make(map[string]int, len(tasks))
	out := make([]*Task, 0, len(tasks))
	for _, task := range tasks {
		if at, ok := seen[task.TaskID]; ok {
			out[at] = task
			continue
		}
		seen[task.TaskID] = len(out)
		out = append(out, task)
	}
	sortTasks(out)
	return out
}

const (
	maxTerminalRecords = 20
	maxTaskLogBytes    = 5 << 20
	logTruncatedMarker = "\n[truncated]\n"
)

// indexPath holds the bounded terminal summaries that survive working-dir cleanup.
func (s *Store) indexPath() string { return filepath.Join(s.root, "index.json") }

// Cleanup removes the task's mutable working files after the launchd job has
// verifiably disappeared. The terminal summary is retained in index.json,
// bounded to the most recent records. A missing task is not an error.
func (s *Store) Cleanup(taskID string) error {
	dir := s.taskDir(taskID)
	if dir == s.root || !filepath.IsAbs(dir) || taskID != filepath.Base(taskID) || taskID == "" || taskID == "." {
		return fmt.Errorf("refusing to clean unsafe path %q", dir)
	}
	task, err := s.Load(taskID)
	if err != nil {
		return err
	}
	if task != nil {
		task.Cleaned = true
		task.CleanupPending = false
		if err := s.appendIndex(task); err != nil {
			return err
		}
	}
	return os.RemoveAll(dir)
}

func (s *Store) appendIndex(task *Task) error {
	records, err := s.readIndex()
	if err != nil {
		return err
	}
	view := task.View()
	replaced := false
	for i := range records {
		if records[i].TaskID == view.TaskID {
			records[i] = view
			replaced = true
			break
		}
	}
	if !replaced {
		records = append(records, view)
	}
	sort.Slice(records, func(i, j int) bool {
		return records[i].UpdatedAt > records[j].UpdatedAt
	})
	if len(records) > maxTerminalRecords {
		records = records[:maxTerminalRecords]
	}
	data, err := json.MarshalIndent(records, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(s.indexPath(), data, 0o600)
}

func (s *Store) readIndex() ([]upgradehost.TaskView, error) {
	data, err := os.ReadFile(s.indexPath())
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var records []upgradehost.TaskView
	if err := json.Unmarshal(data, &records); err != nil {
		return nil, fmt.Errorf("corrupt upgrade index: %w", err)
	}
	return records, nil
}

// capLogFile keeps one task log at or below the design limit and leaves a
// visible truncation marker. Missing logs are not an error.
func capLogFile(path string) error {
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Size() <= maxTaskLogBytes {
		return nil
	}
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	marker := []byte(logTruncatedMarker)
	keep := int64(maxTaskLogBytes - len(marker))
	if keep < 0 {
		keep = 0
	}
	buf := make([]byte, keep)
	if _, err := file.ReadAt(buf, info.Size()-keep); err != nil {
		return err
	}
	return writeFileAtomic(path, append(marker, buf...), 0o600)
}

func writeFileAtomic(path string, data []byte, mode os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, mode); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
