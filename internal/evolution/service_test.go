package evolution

import (
	"context"
	"testing"

	"github.com/inv1sion/evoops/internal/agent"
	"github.com/inv1sion/evoops/internal/dataset"
	"github.com/inv1sion/evoops/internal/harness"
	"github.com/inv1sion/evoops/internal/policy"
	"github.com/inv1sion/evoops/internal/repository"
	"github.com/inv1sion/evoops/internal/tools"
)

func TestEvolutionClosedLoopCanaryPromoteAndRollback(t *testing.T) {
	ctx := context.Background()
	repo, err := repository.NewFile(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	manager, err := policy.NewManager(ctx, repo)
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
	engine, err := agent.NewEngine(ctx, repo, manager, registry, agent.LocalSynthesizer{})
	if err != nil {
		t.Fatal(err)
	}
	suite, err := harness.Load("../../data/harness/suite.json", engine)
	if err != nil {
		t.Fatal(err)
	}
	service := NewService(repo, manager, suite)
	evolutionRun, err := service.Evolve(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if !evolutionRun.BaselineReport.Passed || !evolutionRun.CandidateReport.Passed {
		t.Fatalf("Harness blocked evolution: baseline=%#v candidate=%#v", evolutionRun.BaselineReport, evolutionRun.CandidateReport)
	}
	if evolutionRun.Status != "canary" || evolutionRun.CanaryPercent != 10 {
		t.Fatalf("unexpected evolution status: %#v", evolutionRun)
	}
	if len(evolutionRun.Candidate.Mutations) == 0 {
		t.Fatal("candidate contains no auditable mutations")
	}
	if err := service.Promote(ctx, evolutionRun.Candidate.Version); err != nil {
		t.Fatal(err)
	}
	state, _ := manager.State(ctx)
	if state.ActiveVersion != evolutionRun.Candidate.Version {
		t.Fatalf("active=%s, want %s", state.ActiveVersion, evolutionRun.Candidate.Version)
	}
	if err := service.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	state, _ = manager.State(ctx)
	if state.ActiveVersion != "v1.0.0" {
		t.Fatalf("rollback active=%s, want v1.0.0", state.ActiveVersion)
	}
}
