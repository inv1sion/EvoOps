package policy

import (
	"context"
	"testing"

	"github.com/inv1sion/evoops/internal/domain"
	"github.com/inv1sion/evoops/internal/repository"
)

func TestAttributionAllowlistConstrainsCandidateMutations(t *testing.T) {
	ctx := context.Background()
	repo, err := repository.NewFile(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	manager, err := NewManager(ctx, repo)
	if err != nil {
		t.Fatal(err)
	}
	candidate, err := manager.GenerateCandidateFrom(ctx, []domain.FailureAttribution{{
		Category: "retrieval", AllowedMutations: []string{"query_rewrite_strategy"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if candidate.RetrievalCandidateK != DefaultPolicy().RetrievalCandidateK {
		t.Fatalf("candidate_k changed outside allowlist: %d", candidate.RetrievalCandidateK)
	}
	if candidate.QueryRewriteStrategy != "hyde" {
		t.Fatalf("rewrite=%s, want hyde", candidate.QueryRewriteStrategy)
	}
	if len(candidate.Mutations) != 1 || candidate.Mutations[0].Field != "query_rewrite_strategy" {
		t.Fatalf("unexpected mutations: %#v", candidate.Mutations)
	}
	if candidate.RequiredApprovalRisk != domain.RiskMedium {
		t.Fatalf("retrieval attribution changed safety boundary: %s", candidate.RequiredApprovalRisk)
	}
}

func TestSafetyAttributionOnlyTightensApproval(t *testing.T) {
	ctx := context.Background()
	repo, err := repository.NewFile(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	manager, err := NewManager(ctx, repo)
	if err != nil {
		t.Fatal(err)
	}
	candidate, err := manager.GenerateCandidateFrom(ctx, []domain.FailureAttribution{{
		Category: "safety", AllowedMutations: []string{"required_approval_risk"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if candidate.RequiredApprovalRisk != domain.RiskLow {
		t.Fatalf("approval risk=%s, want low", candidate.RequiredApprovalRisk)
	}
	if len(candidate.Mutations) != 1 || candidate.Mutations[0].Field != "required_approval_risk" {
		t.Fatalf("unexpected mutations: %#v", candidate.Mutations)
	}
}

func TestOutcomeAttributionOnlyAdjustsCampaignROIThreshold(t *testing.T) {
	ctx := context.Background()
	repo, err := repository.NewFile(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	manager, err := NewManager(ctx, repo)
	if err != nil {
		t.Fatal(err)
	}
	candidate, err := manager.GenerateCandidateFrom(ctx, []domain.FailureAttribution{{
		Category: "outcome", AllowedMutations: []string{"campaign_roi_threshold"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if candidate.CampaignROIThreshold <= DefaultPolicy().CampaignROIThreshold {
		t.Fatalf("outcome miss did not increase ROI sensitivity: %.2f", candidate.CampaignROIThreshold)
	}
	if len(candidate.Mutations) != 1 || candidate.Mutations[0].Field != "campaign_roi_threshold" {
		t.Fatalf("unexpected mutations: %#v", candidate.Mutations)
	}
}

func TestReleaseCredentialMustMatchCurrentBaselineAndCanary(t *testing.T) {
	ctx := context.Background()
	repo, err := repository.NewFile(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	manager, err := NewManager(ctx, repo)
	if err != nil {
		t.Fatal(err)
	}
	candidate, err := manager.GenerateCandidate(ctx)
	if err != nil {
		t.Fatal(err)
	}
	stale := domain.HarnessReport{
		ID: "report-stale", SuiteVersion: "suite-v1", PolicyVersion: candidate.Version,
		BaselineVersion: "another-policy", Passed: true,
	}
	if err := manager.MarkEvaluated(ctx, candidate.Version, stale); err == nil {
		t.Fatal("stale baseline credential was accepted")
	}
	report := stale
	report.ID = "report-valid"
	report.BaselineVersion = "v1.0.0"
	if err := manager.MarkEvaluated(ctx, candidate.Version, report); err != nil {
		t.Fatal(err)
	}
	if err := manager.Promote(ctx, candidate.Version); err == nil {
		t.Fatal("policy was promoted without canary")
	}
	if err := manager.StartCanary(ctx, candidate.Version, 10); err != nil {
		t.Fatal(err)
	}
	if err := manager.Promote(ctx, candidate.Version); err != nil {
		t.Fatal(err)
	}
}
