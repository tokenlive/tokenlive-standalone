package upgrade

import (
	"sort"
	"time"

	"github.com/tokenlive/tokenlive-admin/pkg/upgradehost"
)

func sortTasks(tasks []*Task) {
	sort.Slice(tasks, func(i, j int) bool {
		return tasks[i].CreatedAt.Before(tasks[j].CreatedAt)
	})
}

// ActiveTask returns the task that currently blocks a new upgrade, if any:
// anything non-terminal, plus terminal-but-unresolved needs_attention records
// (they require human recovery before a new task may start).
func ActiveTask(tasks []*Task, now time.Time) *Task {
	var pending *Task
	for _, task := range tasks {
		if task.State == upgradehost.StateNeedsAttention {
			return task
		}
		if !upgradehost.StateTerminal(task.State) {
			// Confirmations expire lazily; treat an expired awaiting task as
			// non-blocking so a later reconcile can retire it.
			if task.State == upgradehost.StateAwaitingConfirmation && task.CredentialExpiry.Before(now) {
				continue
			}
			pending = task
		}
	}
	return pending
}

// CleanupPendingTask returns the newest terminal task whose working files can
// be removed: the launchd job must have signalled cleanup_pending first.
func CleanupPendingTask(tasks []*Task) *Task {
	for i := len(tasks) - 1; i >= 0; i-- {
		t := tasks[i]
		if upgradehost.StateTerminal(t.State) && t.CleanupPending {
			return t
		}
	}
	return nil
}
