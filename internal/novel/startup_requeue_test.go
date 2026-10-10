package novel

import (
	"context"
	"testing"
	"time"

	"mirror-studio/internal/db"
)

// A deploy stops the worker by canceling its context, so the terminal write of
// every in-flight task fails and the row keeps its running status. The periodic
// stall rescue skips any task that already emitted an event (its predicate is
// last_event_id = 0) and retry only accepts failed or canceled rows, so without
// a startup pass such a task stays running forever: the stream never ends and
// the UI spins until the row is deleted by hand.
func TestRequeueInterruptedTasksOnStartup(t *testing.T) {
	env := newTestEnvironment(t)
	ctx := context.Background()
	now := time.Now().UTC()
	env.service.now = func() time.Time { return now }

	interrupted := env.insertTask(StatusRunning, map[string]any{
		"started_at":    db.FormatTime(now.Add(-30 * time.Second)),
		"last_event_id": 5,
		"progress":      62,
		"logs":          `[{"id": 5, "type": "task_started", "payload": {"message": "已启动"}, "created_at": "2026-10-10T00:00:00+00:00"}]`,
	})
	finished := env.insertTask(StatusSucceeded, map[string]any{
		"finished_at":   db.FormatTime(now),
		"last_event_id": 9,
		"progress":      100,
	})

	requeued, err := env.service.RequeueInterruptedTasks(ctx)
	if err != nil {
		t.Fatalf("requeue: %v", err)
	}
	if requeued != 1 {
		t.Fatalf("requeued = %d, want 1", requeued)
	}

	task, err := env.service.loadTask(ctx, interrupted)
	if err != nil {
		t.Fatalf("load requeued task: %v", err)
	}
	if task.Status != StatusQueued {
		t.Fatalf("status = %s, want queued", task.Status)
	}
	if task.StartedAt != nil || task.FinishedAt != nil {
		t.Fatalf("runtime fields not cleared: %+v", task)
	}
	// Keeping the plan, progress and log is what makes the worker resume image
	// generation instead of redoing the analysis.
	if task.Progress != 62 {
		t.Fatalf("progress = %d, want the retained 62", task.Progress)
	}
	if len(task.Logs) != 1 {
		t.Fatalf("logs = %+v, want the retained event", task.Logs)
	}

	untouched, err := env.service.loadTask(ctx, finished)
	if err != nil {
		t.Fatalf("load finished task: %v", err)
	}
	if untouched.Status != StatusSucceeded {
		t.Fatalf("finished task status = %s, want untouched succeeded", untouched.Status)
	}
}
