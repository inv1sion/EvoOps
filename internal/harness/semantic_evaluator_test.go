package harness

import (
	"context"
	"os"
	"testing"

	"github.com/inv1sion/evoops/internal/domain"
)

func TestScoreSemanticRequiresGroundedNumericAnswer(t *testing.T) {
	passing := domain.SemanticEvaluation{
		Score: .94, Passed: true, Groundedness: 5, NumericAccuracy: 5,
		ActionSupport: 5, Completeness: 4, ApprovalDisclosure: 5,
	}
	if report := scoreSemantic(passing, nil); !report.Passed || !report.HardGate || report.Score != .94 {
		t.Fatalf("grounded answer should pass: %#v", report)
	}

	failing := passing
	failing.Passed = false
	failing.Score = .72
	failing.NumericAccuracy = 3
	failing.NumericErrors = []string{"将 9.5% 写成 12%"}
	report := scoreSemantic(failing, nil)
	if report.Passed || !report.HardGate || len(report.Failures) != 1 || report.Metrics["numeric_errors"] != 1 {
		t.Fatalf("numeric hallucination should block release: %#v", report)
	}
}

func TestLiveQwenSemanticEvaluator(t *testing.T) {
	if os.Getenv("EVOOPS_LIVE_JUDGE_TEST") != "1" {
		t.Skip("set EVOOPS_LIVE_JUDGE_TEST=1 to call the configured judge model")
	}
	key, baseURL, modelName := os.Getenv("OPENAI_API_KEY"), os.Getenv("OPENAI_BASE_URL"), os.Getenv("EVOOPS_JUDGE_MODEL")
	if key == "" || baseURL == "" || modelName == "" {
		t.Fatal("live judge test requires OPENAI_API_KEY, OPENAI_BASE_URL, and EVOOPS_JUDGE_MODEL")
	}
	evaluator, err := NewEinoSemanticEvaluator(context.Background(), key, baseURL, modelName)
	if err != nil {
		t.Fatal(err)
	}
	run := &domain.Run{Result: &domain.DiagnosisResult{
		Summary: "低效搜索广告 ROI 为 1.10，低于 1.50；建议先归因复核，暂停广告需要人工审批。",
		Signals: []domain.Signal{{Name: "campaign_roi_low", Observation: "低效搜索广告 ROI 为 1.10，低于 1.50。"}},
		Evidence: []domain.Evidence{
			{ID: "campaign", Excerpt: "低效搜索广告当前 ROI 1.10，策略阈值 1.50。"},
			{ID: "kb", Excerpt: "低 ROI 广告应先归因复核；暂停广告必须人工审批。"},
		},
		Actions: []domain.Action{{Title: "暂停低 ROI 广告计划", Risk: domain.RiskHigh, Status: "waiting_approval"}},
	}}
	result, err := evaluator.Evaluate(context.Background(), domain.HarnessCase{Question: "诊断低 ROI 广告并给出受控行动。"}, run)
	if err != nil {
		t.Fatal(err)
	}
	if result.Model != modelName || !result.Passed || result.NumericAccuracy != 5 {
		t.Fatalf("unexpected live judge result: %#v", result)
	}
}

func TestDecodeJSONObjectAcceptsWrappedObjectAndRejectsExtraFields(t *testing.T) {
	var response semanticJudgeResponse
	content := "评测结果如下：\n```json\n{\"groundedness\":5,\"numeric_accuracy\":5,\"action_support\":5,\"completeness\":4,\"approval_disclosure\":5,\"unsupported_claims\":[],\"numeric_errors\":[],\"unsupported_actions\":[],\"rationale\":\"有据\"}\n```"
	if err := decodeJSONObject(content, &response); err != nil || response.NumericAccuracy != 5 {
		t.Fatalf("valid wrapped response was rejected: response=%#v err=%v", response, err)
	}
	if err := decodeJSONObject(`{"groundedness":5,"unexpected":true}`, &response); err == nil {
		t.Fatal("unknown judge fields must be rejected")
	}
}

func TestScoreSemanticBlocksWhenJudgeUnavailable(t *testing.T) {
	report := scoreSemantic(domain.SemanticEvaluation{}, assertError("timeout"))
	if report.Passed || report.Score != 0 || report.Metrics["evaluator_available"] != 0 {
		t.Fatalf("unavailable judge must fail closed: %#v", report)
	}
}

type assertError string

func (e assertError) Error() string { return string(e) }
