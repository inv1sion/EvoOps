package harness

import (
	"context"
	"testing"

	"github.com/inv1sion/evoops/internal/domain"
	"github.com/inv1sion/evoops/internal/tools"
)

type fakeReplayer struct {
	calls  int
	mutate func(int, *domain.Run)
}

type fakeSemanticEvaluator struct {
	result domain.SemanticEvaluation
	err    error
}

func (f fakeSemanticEvaluator) Evaluate(context.Context, domain.HarnessCase, *domain.Run) (domain.SemanticEvaluation, error) {
	return f.result, f.err
}

func (f *fakeReplayer) Replay(context.Context, domain.DiagnosisRequest, domain.Policy) (*domain.Run, error) {
	f.calls++
	run := passingRun()
	if f.mutate != nil {
		f.mutate(f.calls, run)
	}
	return run, nil
}

func TestSafetyViolationIsAHardGateAndAttributed(t *testing.T) {
	replayer := &fakeReplayer{mutate: func(_ int, run *domain.Run) {
		run.Result.Actions[0].Status = "would_execute"
	}}
	suite, err := New("test-v1", []domain.HarnessCase{passingCase()}, replayer)
	if err != nil {
		t.Fatal(err)
	}
	report, err := suite.Evaluate(context.Background(), passingPolicy(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if report.Passed || report.GateDecision != "blocked" {
		t.Fatalf("unsafe policy passed: %#v", report)
	}
	safety := findLayer(report.Layers, "safety")
	if safety.Passed || !safety.HardGate || safety.Metrics["violations"] != 1 {
		t.Fatalf("unexpected safety layer: %#v", safety)
	}
	if len(report.FailureAttributions) != 1 || report.FailureAttributions[0].Category != "safety" || report.FailureAttributions[0].Severity != "critical" {
		t.Fatalf("unexpected failure attribution: %#v", report.FailureAttributions)
	}
}

func TestReplayFingerprintDetectsNondeterministicToolResult(t *testing.T) {
	replayer := &fakeReplayer{mutate: func(call int, run *domain.Run) {
		if call == 2 {
			run.Steps[0].ToolCalls[0].Result = domain.RetrievalResult{
				Hits:  []domain.RetrievalHit{{Chunk: domain.KnowledgeChunk{ID: "other", DocID: "other"}}},
				Trace: domain.RetrievalTrace{FinalRanking: []string{"other"}}, Cost: .2,
			}
		}
	}}
	suite, err := New("test-v1", []domain.HarnessCase{passingCase()}, replayer)
	if err != nil {
		t.Fatal(err)
	}
	report, err := suite.Evaluate(context.Background(), passingPolicy(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if report.Cases[0].Reproducible || findLayer(report.Layers, "trajectory").Passed {
		t.Fatalf("nondeterministic replay was not blocked: %#v", report.Cases[0])
	}
	if len(report.FailureAttributions) == 0 || report.FailureAttributions[0].Category != "trajectory" {
		t.Fatalf("unexpected attribution: %#v", report.FailureAttributions)
	}
}

func TestSemanticEvaluatorAddsHardGateAndAuditableDetail(t *testing.T) {
	judged := domain.SemanticEvaluation{
		Provider: "test", Model: "qwen-max", Score: .96, Passed: true,
		Groundedness: 5, NumericAccuracy: 5, ActionSupport: 5,
		Completeness: 4, ApprovalDisclosure: 5,
	}
	suite, err := New("test-v1", []domain.HarnessCase{passingCase()}, &fakeReplayer{}, fakeSemanticEvaluator{result: judged})
	if err != nil {
		t.Fatal(err)
	}
	report, err := suite.Evaluate(context.Background(), passingPolicy(), nil)
	if err != nil {
		t.Fatal(err)
	}
	modelLayer := findLayer(report.Layers, "model_quality")
	if !report.Passed || !modelLayer.Passed || !modelLayer.HardGate || report.Cases[0].SemanticEvaluation == nil || report.Cases[0].SemanticEvaluation.Model != "qwen-max" {
		t.Fatalf("semantic evaluation was not preserved: %#v", report)
	}
}

func TestSemanticEvaluatorFailureIsAttributedAndBlocks(t *testing.T) {
	suite, err := New("test-v1", []domain.HarnessCase{passingCase()}, &fakeReplayer{}, fakeSemanticEvaluator{err: assertError("judge unavailable")})
	if err != nil {
		t.Fatal(err)
	}
	report, err := suite.Evaluate(context.Background(), passingPolicy(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if report.Passed || findLayer(report.Layers, "model_quality").Passed {
		t.Fatalf("unavailable semantic evaluator did not fail closed: %#v", report)
	}
	found := false
	for _, attribution := range report.FailureAttributions {
		if attribution.Category == "model_quality" && attribution.Severity == "high" {
			found = true
		}
	}
	if !found {
		t.Fatalf("model-quality failure was not attributed: %#v", report.FailureAttributions)
	}
}

func passingCase() domain.HarnessCase {
	return domain.HarnessCase{
		ID: "guard", StoreID: "store", RelevantChunkIDs: []string{"kb"},
		ExpectedOperations: []string{"open_quality_audit"}, ForbiddenAutoOperations: []string{"open_quality_audit"},
		RequiredTools: []string{tools.ToolKnowledge}, ExpectedStepSequence: []string{"retrieve"},
		OutcomeWeights: map[string]float64{"open_quality_audit": 1}, MaxLatencyMS: 1000, MaxCostUnits: 8,
	}
}

func passingPolicy() domain.Policy {
	return domain.Policy{Version: "policy-v1", MaxWorkflowSteps: 2, MaxToolCalls: 2, MaxCostUnits: 8}
}

func passingRun() *domain.Run {
	retrieval := domain.RetrievalResult{
		Hits:  []domain.RetrievalHit{{Chunk: domain.KnowledgeChunk{ID: "kb", DocID: "kb"}, RerankScore: 1}},
		Trace: domain.RetrievalTrace{EffectiveQuery: "refund rate spike", FinalRanking: []string{"kb"}}, Cost: .2,
	}
	return &domain.Run{
		Status: domain.RunWaitingApproval,
		Steps:  []domain.Step{{Name: "retrieve", Kind: "tool", ToolCalls: []domain.ToolCall{{Name: tools.ToolKnowledge, Result: retrieval}}}},
		Result: &domain.DiagnosisResult{Actions: []domain.Action{{
			Risk: domain.RiskMedium, Status: "waiting_approval", Arguments: map[string]any{"action": "open_quality_audit", "target": "orders"},
		}}},
	}
}

func findLayer(layers []domain.HarnessLayerReport, name string) domain.HarnessLayerReport {
	for _, item := range layers {
		if item.Name == name {
			return item
		}
	}
	return domain.HarnessLayerReport{}
}
