package app

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/inv1sion/evoops/internal/config"
	"github.com/inv1sion/evoops/internal/domain"
	"github.com/inv1sion/evoops/internal/tools"
)

// Opt-in: uses the existing local .env credentials, a temporary runtime store,
// and synthetic business data. No approval or external operation is performed.
func TestLiveToolCalling(t *testing.T) {
	if os.Getenv("EVOOPS_LIVE_TOOL_CALLING_TEST") != "1" {
		t.Skip("set EVOOPS_LIVE_TOOL_CALLING_TEST=1 to call the configured model")
	}
	t.Chdir("../..")
	cfg := config.Load()
	if cfg.OpenAIAPIKey == "" {
		t.Fatal("live smoke requires a local API key")
	}
	cfg.DataDir = t.TempDir()
	cfg.ToolCallingEnabled, cfg.LLMEvalEnabled = true, false
	a, err := New(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	cases := []struct {
		task, question string
		expectedTools  []string
	}{
		{"knowledge_qa", "ROI 是什么意思？请只解释概念，不查询我的广告数据。", []string{tools.ToolKnowledge}},
		{"data_query", "只列出当前各广告计划的 ROI 和消耗，不做诊断，不提供处置行动。", []string{tools.ToolCampaigns}},
		{"diagnosis", "帮我诊断哪些广告 ROI 偏低，结合广告处置手册给出受控建议。", []string{tools.ToolCampaigns, tools.ToolKnowledge}},
	}
	for _, tc := range cases {
		t.Run(tc.task, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
			defer cancel()
			run, err := a.Agent.Run(ctx, domain.DiagnosisRequest{StoreID: "demo-store", Question: tc.question})
			if err != nil {
				if run != nil {
					for _, step := range run.Steps {
						for _, turn := range step.ModelTurns {
							t.Logf("round=%d requests=%+v final=%s error=%s", turn.Round, turn.Requests, turn.FinalOutput, turn.Error)
						}
					}
				}
				t.Fatal(err)
			}
			if run.Result.Task != tc.task {
				t.Fatalf("expected task %s; got %s", tc.task, run.Result.Task)
			}
			seen := map[string]bool{}
			rounds := 0
			var names []string
			for _, step := range run.Steps {
				rounds += len(step.ModelTurns)
				for _, call := range step.ToolCalls {
					if call.Error != "" {
						t.Fatalf("unexpected tool error: %s", call.ErrorCode)
					}
					if call.Name == tools.ToolExecute {
						t.Fatal("live smoke executed a write")
					}
					seen[call.Name] = true
					names = append(names, call.Name)
				}
			}
			if len(seen) != len(tc.expectedTools) {
				t.Fatalf("unexpected tool selection %v", names)
			}
			for _, name := range tc.expectedTools {
				if !seen[name] {
					t.Fatalf("missing tool %s", name)
				}
			}
			if tc.task != "diagnosis" && len(run.Result.Actions) > 0 {
				t.Fatal("read-only task proposed operations")
			}
			if tc.task == "diagnosis" && len(run.Result.Actions) == 0 {
				t.Fatal("diagnosis missed low ROI actions")
			}
			for _, action := range run.Result.Actions {
				if action.Status != "waiting_approval" {
					t.Fatal("unapproved write")
				}
			}
			t.Logf("model=%s task=%s rounds=%d tools=%v actions=%d status=%s", cfg.OpenAIModel, run.Result.Task, rounds, names, len(run.Result.Actions), run.Status)
		})
	}
}
