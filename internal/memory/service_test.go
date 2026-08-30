package memory

import (
	"context"
	"testing"
	"time"

	"github.com/inv1sion/evoops/internal/domain"
	"github.com/inv1sion/evoops/internal/repository"
)

func TestLearnCreatesAuditableStoreScopedMemories(t *testing.T) {
	repo, err := repository.NewFile(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	service := NewService(repo)
	run := &domain.Run{
		ID: "run-1", Request: domain.DiagnosisRequest{StoreID: "store-a"},
		Result: &domain.DiagnosisResult{Actions: []domain.Action{{
			ID: "action-1", Title: "暂停低效广告计划",
			Arguments: map[string]any{"action": "pause_campaign", "target": "cmp-1"}, Risk: domain.RiskHigh,
		}}},
	}
	feedback := domain.Feedback{
		ID: "feedback-1", RunID: run.ID, StoreID: "store-a", Useful: true,
		RejectedActions: []string{"action-1"}, Comment: "希望先缩减预算，不直接暂停", CreatedAt: time.Now().UTC(),
	}
	updates, err := service.Learn(context.Background(), run, feedback)
	if err != nil {
		t.Fatal(err)
	}
	if len(updates) != 2 {
		t.Fatalf("updates=%d, want episode and preference", len(updates))
	}
	profile, err := service.Get(context.Background(), "store-a")
	if err != nil {
		t.Fatal(err)
	}
	if len(profile.Memories) != 2 || profile.Memories[1].Operation != "pause_campaign" || profile.Memories[1].Polarity != PolarityAvoid {
		t.Fatalf("unexpected profile: %#v", profile)
	}
	if profile.Memories[1].SourceRunID != run.ID || profile.Memories[1].SourceFeedbackID != feedback.ID {
		t.Fatalf("memory lost audit lineage: %#v", profile.Memories[1])
	}
	empty, err := service.Get(context.Background(), "store-b")
	if err != nil || len(empty.Memories) != 0 {
		t.Fatalf("tenant memory leaked: %#v err=%v", empty, err)
	}
}

func TestLearnRejectsForeignActionIDs(t *testing.T) {
	repo, _ := repository.NewFile(t.TempDir())
	service := NewService(repo)
	run := &domain.Run{ID: "run-1", Request: domain.DiagnosisRequest{StoreID: "store-a"}, Result: &domain.DiagnosisResult{}}
	_, err := service.Learn(context.Background(), run, domain.Feedback{
		ID: "feedback-1", StoreID: "store-a", AcceptedActions: []string{"injected-action"},
	})
	if err == nil {
		t.Fatal("expected foreign action ID to be rejected")
	}
}
