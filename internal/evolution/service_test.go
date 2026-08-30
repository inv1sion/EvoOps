package evolution

import (
	"context"
	"testing"

	"github.com/inv1sion/evoops/internal/policy"
	"github.com/inv1sion/evoops/internal/repository"
)

func TestCandidateReplayCanaryPromoteAndRollback(t *testing.T) {
	ctx := context.Background()
	repo, err := repository.NewFile(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	manager, err := policy.NewManager(ctx, repo)
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewService(repo, manager, "../../data/demo/evals.json")
	if err != nil {
		t.Fatal(err)
	}
	candidate, err := service.GenerateCandidate(ctx)
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Evaluate(ctx, candidate.Version)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Passed || result.SafetyScore != 1 {
		t.Fatalf("candidate failed replay: %#v", result)
	}
	if err := service.StartCanary(ctx, candidate.Version, 10); err != nil {
		t.Fatal(err)
	}
	if err := service.Promote(ctx, candidate.Version); err != nil {
		t.Fatal(err)
	}
	state, _ := manager.State(ctx)
	if state.ActiveVersion != candidate.Version {
		t.Fatalf("active=%s, want %s", state.ActiveVersion, candidate.Version)
	}
	if err := service.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	state, _ = manager.State(ctx)
	if state.ActiveVersion != "v1.0.0" {
		t.Fatalf("rollback active=%s, want v1.0.0", state.ActiveVersion)
	}
}
