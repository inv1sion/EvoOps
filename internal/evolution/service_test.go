package evolution

import (
	"context"
	"testing"
	"time"

	"github.com/inv1sion/evoops/internal/agent"
	"github.com/inv1sion/evoops/internal/dataset"
	"github.com/inv1sion/evoops/internal/domain"
	"github.com/inv1sion/evoops/internal/harness"
	"github.com/inv1sion/evoops/internal/policy"
	promptpolicy "github.com/inv1sion/evoops/internal/prompt"
	"github.com/inv1sion/evoops/internal/repository"
	"github.com/inv1sion/evoops/internal/tools"
)

type passingSemanticEvaluator struct{}

func (passingSemanticEvaluator) Evaluate(context.Context, domain.HarnessCase, *domain.Run) (domain.SemanticEvaluation, error) {
	return domain.SemanticEvaluation{Provider: "test", Model: "judge-test", Score: 1, Passed: true, Groundedness: 5, NumericAccuracy: 5, ActionSupport: 5, Completeness: 5, ApprovalDisclosure: 5}, nil
}

type recordingPromptOptimizer struct{ called bool }

func (o *recordingPromptOptimizer) Name() string      { return "test-prompt-optimizer" }
func (o *recordingPromptOptimizer) ModelName() string { return "optimizer-test" }
func (o *recordingPromptOptimizer) Optimize(_ context.Context, request promptpolicy.OptimizationRequest) (domain.PromptArtifact, error) {
	o.called = true
	return promptpolicy.NewArtifact(request.Active.Prompt, "回答前核对证据、数字和审批说明。", o.Name(), o.ModelName(), "测试 Prompt 演进闭环。", []string{"judge feedback"}, time.Unix(2, 0).UTC())
}

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

func TestCleanSemanticBaselineProducesAuditablePromptCandidateAndComparison(t *testing.T) {
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
	suite, err := harness.Load("../../data/harness/suite.json", engine, passingSemanticEvaluator{})
	if err != nil {
		t.Fatal(err)
	}
	optimizer := &recordingPromptOptimizer{}
	service := NewService(repo, manager, suite, optimizer)
	run, err := service.Evolve(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !optimizer.called || run.Candidate.Prompt.Generator != optimizer.Name() || !run.Candidate.Prompt.Validation.Passed {
		t.Fatalf("prompt candidate was not generated and validated: %#v", run.Candidate.Prompt)
	}
	if !run.Comparison.PromptChanged || run.Comparison.CaseCount != 4 || run.Comparison.CandidateCasePassRate != 1 {
		t.Fatalf("comparison result is incomplete: %#v", run.Comparison)
	}
}
