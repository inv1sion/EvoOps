package harness

import (
	"testing"

	"github.com/inv1sion/evoops/internal/domain"
	"github.com/inv1sion/evoops/internal/tools"
)

func TestToolCallingCostCountsModelRoundsWithoutDoubleChargingCache(t *testing.T) {
	run := &domain.Run{Steps: []domain.Step{
		{Kind: "agent", DurationMS: 100, ModelTurns: []domain.ModelTurn{{PromptTokens: 10, CompletionTokens: 5}, {PromptTokens: 20, CompletionTokens: 8}}, ToolCalls: []domain.ToolCall{
			{Name: tools.ToolCampaigns}, {Name: tools.ToolCampaigns, Cached: true}, {Name: tools.ToolKnowledge, ErrorCode: "invalid_arguments", Error: "bad query"},
		}}, {Kind: "model", DurationMS: 50},
	}}
	report := scoreCost(domain.HarnessCase{MaxCostUnits: 10, MaxLatencyMS: 1000}, domain.Policy{MaxCostUnits: 10}, run)
	if report.Metrics["model_call_count"] != 3 || report.Metrics["tool_execution_count"] != 1 || report.Metrics["cost_units"] != 5.6 || report.Metrics["latency_ms"] != 150 {
		t.Fatalf("incorrect cost accounting: %#v", report.Metrics)
	}
	if report.Metrics["planner_prompt_tokens"] != 30 || report.Metrics["planner_completion_tokens"] != 13 {
		t.Fatal("planner token metadata not counted")
	}
}

func TestToolCallingRetrievalMergesSuccessfulQueries(t *testing.T) {
	run := &domain.Run{Steps: []domain.Step{{ToolCalls: []domain.ToolCall{
		{Name: tools.ToolKnowledge, Error: "failed"},
		{Name: tools.ToolKnowledge, Result: domain.RetrievalResult{Cost: 1, Trace: domain.RetrievalTrace{FinalRanking: []string{"a"}}}},
		{Name: tools.ToolKnowledge, Cached: true, Result: domain.RetrievalResult{Cost: 1, Trace: domain.RetrievalTrace{FinalRanking: []string{"a"}}}},
		{Name: tools.ToolKnowledge, Result: domain.RetrievalResult{Cost: 2, Trace: domain.RetrievalTrace{FinalRanking: []string{"b", "a"}}}},
	}}}}
	result := retrievalResult(run)
	if result.Cost != 3 || len(result.Trace.FinalRanking) != 2 || result.Trace.FinalRanking[0] != "a" || result.Trace.FinalRanking[1] != "b" {
		t.Fatalf("invalid aggregate %#v", result)
	}
}

func TestToolCallingFingerprintIncludesTaskAndRoundCount(t *testing.T) {
	run := &domain.Run{ExecutionMode: "model_tool_calling", Result: &domain.DiagnosisResult{Task: "data_query"}, Steps: []domain.Step{{Name: "model_tool_collection", ModelTurns: []domain.ModelTurn{{Round: 1}}}}}
	base := trajectoryFingerprint(run)
	run.Result.Task = "knowledge_qa"
	if trajectoryFingerprint(run) == base {
		t.Fatal("different task produced same fingerprint")
	}
	run.Result.Task = "data_query"
	run.Steps[0].ModelTurns = append(run.Steps[0].ModelTurns, domain.ModelTurn{Round: 2})
	if trajectoryFingerprint(run) == base {
		t.Fatal("extra model round was hidden from replay")
	}
}
