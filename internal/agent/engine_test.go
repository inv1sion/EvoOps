package agent

import (
	"context"
	"testing"

	"github.com/inv1sion/evoops/internal/dataset"
	"github.com/inv1sion/evoops/internal/domain"
	"github.com/inv1sion/evoops/internal/policy"
	"github.com/inv1sion/evoops/internal/repository"
	"github.com/inv1sion/evoops/internal/tools"
)

type traceableSynthesizer struct{ LocalSynthesizer }

func (traceableSynthesizer) ModelName() string { return "qwen-test" }

func TestEngineEndToEndApprovalAndTrajectory(t *testing.T) {
	ctx := context.Background()
	repo, err := repository.NewFile(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	policies, err := policy.NewManager(ctx, repo)
	if err != nil {
		t.Fatal(err)
	}
	data, err := dataset.LoadFile("../../data/demo/store.json")
	if err != nil {
		t.Fatal(err)
	}
	registry := tools.NewRegistry()
	if err := tools.RegisterLocal(ctx, registry, data); err != nil {
		t.Fatal(err)
	}
	engine, err := NewEngine(ctx, repo, policies, registry, traceableSynthesizer{})
	if err != nil {
		t.Fatal(err)
	}
	run, err := engine.Run(ctx, domain.DiagnosisRequest{StoreID: "demo-store"})
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != domain.RunWaitingApproval {
		t.Fatalf("status=%s, want waiting_approval", run.Status)
	}
	if len(run.Steps) != 6 {
		t.Fatalf("steps=%d, want 6", len(run.Steps))
	}
	if modelName, _ := run.Steps[4].Input["model"].(string); modelName != "qwen-test" {
		t.Fatalf("synthesis model is missing from trajectory: %#v", run.Steps[4].Input)
	}
	if run.PendingApproval == nil || len(run.PendingApproval.ActionIDs) == 0 {
		t.Fatal("expected durable pending approval")
	}
	executedLowRisk := false
	for _, action := range run.Result.Actions {
		if action.Risk == domain.RiskLow && action.Status == "executed" {
			executedLowRisk = true
		}
	}
	if !executedLowRisk {
		t.Fatal("expected a low-risk action to execute automatically")
	}
	approved, err := engine.Approve(ctx, run.ID, domain.ApprovalDecision{Approved: true, Actor: "test-approver"})
	if err != nil {
		t.Fatal(err)
	}
	if approved.Status != domain.RunCompleted || approved.PendingApproval != nil {
		t.Fatalf("approval did not complete run: %#v", approved)
	}
	if len(approved.Steps) != 7 || approved.Steps[6].Kind != "approval" {
		t.Fatalf("approval resume was not appended to trajectory: %#v", approved.Steps)
	}
}
