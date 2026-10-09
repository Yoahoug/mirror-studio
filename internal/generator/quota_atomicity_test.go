package generator

import (
	"context"
	"testing"
)

// ledgerRemark reads the remark the given task's unfreeze row carries.
func ledgerRemark(t *testing.T, env *testEnv, taskNo string) string {
	t.Helper()
	var remark string
	if err := env.handle.QueryRow(
		`SELECT remark FROM billing_quotaledger WHERE related_task_no = ? AND change_type = 'unfreeze'`,
		taskNo).Scan(&remark); err != nil {
		t.Fatalf("read unfreeze ledger row: %v", err)
	}
	return remark
}

func TestCancelLedgerUsesReferenceRemark(t *testing.T) {
	env := newTestEnv(t)
	task, err := env.service.Submit(context.Background(), SubmitInput{
		UserID: env.userID, Prompt: "取消台账备注", ClientIP: "127.0.0.1",
	})
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if _, err := env.service.Cancel(context.Background(), env.userID, task.TaskNo); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if got := ledgerRemark(t, env, task.TaskNo); got != "用户取消任务" {
		t.Fatalf("cancel remark = %q, want 用户取消任务", got)
	}
	if frozen := env.quotaValue("frozen_quota"); frozen != 0 {
		t.Fatalf("frozen_quota = %d, want 0", frozen)
	}
}

func TestDeleteRunningTaskLedgerUsesReferenceRemark(t *testing.T) {
	env := newTestEnv(t)
	task, err := env.service.Submit(context.Background(), SubmitInput{
		UserID: env.userID, Prompt: "删除台账备注", ClientIP: "127.0.0.1",
	})
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if _, err := env.service.deleteTaskHistory(context.Background(), env.userID, task.TaskNo); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if got := ledgerRemark(t, env, task.TaskNo); got != "用户删除运行中任务" {
		t.Fatalf("delete remark = %q, want 用户删除运行中任务", got)
	}
}

func TestFailureSettlesQuotaInTerminalTransaction(t *testing.T) {
	env := newTestEnv(t)
	task, err := env.service.Submit(context.Background(), SubmitInput{
		UserID: env.userID, Prompt: "失败退回原子性", ClientIP: "127.0.0.1",
	})
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	updated, err := env.service.failTask(context.Background(), env.taskRow(task.TaskNo),
		"generate_failed", "boom", "", nil)
	if err != nil {
		t.Fatalf("fail task: %v", err)
	}
	if !updated.QuotaFinalized {
		t.Fatal("task not finalized")
	}
	if got := ledgerRemark(t, env, task.TaskNo); got != "任务失败退回" {
		t.Fatalf("failure remark = %q, want 任务失败退回", got)
	}
	if frozen := env.quotaValue("frozen_quota"); frozen != 0 {
		t.Fatalf("frozen_quota = %d, want 0", frozen)
	}
	// The harness seeds available_quota = 1, so the release restores that
	// balance rather than the daily default.
	if available := env.quotaValue("available_quota"); available != 1 {
		t.Fatalf("available_quota = %d, want 1", available)
	}
}

func TestSuccessConsumesQuotaInsideTerminalTransaction(t *testing.T) {
	env := newTestEnv(t)
	task, err := env.service.Submit(context.Background(), SubmitInput{
		UserID: env.userID, Prompt: "成功扣减原子性", ClientIP: "127.0.0.1",
	})
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if err := env.runTask(task.TaskNo); err != nil {
		t.Fatalf("run task: %v", err)
	}
	if frozen := env.quotaValue("frozen_quota"); frozen != 0 {
		t.Fatalf("frozen_quota = %d, want 0", frozen)
	}
	var totalGenerated int
	if err := env.handle.QueryRow(
		`SELECT total_generated_count FROM billing_quotaaccount WHERE user_id = ?`,
		env.userID).Scan(&totalGenerated); err != nil {
		t.Fatalf("read total: %v", err)
	}
	if totalGenerated != 1 {
		t.Fatalf("total_generated_count = %d, want 1", totalGenerated)
	}
	var consumeRows int
	if err := env.handle.QueryRow(
		`SELECT COUNT(1) FROM billing_quotaledger WHERE related_task_no = ? AND change_type = 'consume'`,
		task.TaskNo).Scan(&consumeRows); err != nil {
		t.Fatalf("count consume ledger: %v", err)
	}
	if consumeRows != 1 {
		t.Fatalf("consume ledger rows = %d, want 1", consumeRows)
	}
}
