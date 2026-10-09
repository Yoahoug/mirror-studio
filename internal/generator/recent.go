package generator

import (
	"context"
)

// RecentTasks returns the user's three newest generation tasks for
// /api/account/summary, each reduced to the three fields the reference emits.
//
// It mirrors apps/users/views.py AccountSummaryAPIView:
//
//	GenerateTask.objects.filter(user=...).select_related("channel")
//	    .defer("provider_response_json", "reference_image_data_url")
//	    .order_by("-created_at")[:3]
//	reconcile_recent_tasks(...)
//
// Two details differ from the workspace history query and are reproduced
// deliberately: rows are NOT filtered on is_deleted, and the ordering is
// created_at descending only (no id tiebreak). The two deferred columns are
// never read here, so they are simply left out of the projection.
func (s *Service) RecentTasks(ctx context.Context, userID int64, limit int) ([]map[string]any, error) {
	if limit <= 0 {
		return nil, nil
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+taskColumns+` FROM generator_generatetask WHERE user_id = ?
		 ORDER BY created_at DESC LIMIT ?`,
		userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	tasks := []*Task{}
	for rows.Next() {
		task, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, task)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	items := make([]map[string]any, 0, len(tasks))
	for _, task := range tasks {
		// The lease timeout uses the channel's own settings, so the channel has
		// to be loaded before reconciliation — the reference's select_related.
		// A channel row that no longer exists is not an error here: Django would
		// leave the relation None and fall back to the default timeout.
		if task.Channel == nil {
			if channel, err := s.channelByIDTxIn(ctx, task.ChannelID); err == nil {
				task.Channel = channel
			} else {
				s.log.Warn("任务渠道加载失败", "task_no", task.TaskNo, "error", err)
			}
		}
		task = s.reconcileTaskRuntimeState(ctx, task)
		items = append(items, map[string]any{
			"task_no":    task.TaskNo,
			"status":     task.Status,
			"created_at": isoTimeOrNil(task.CreatedAt),
		})
	}
	return items, nil
}
