package harness

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/inv1sion/evoops/internal/domain"
)

// SemanticEvaluator verifies the model-written diagnosis against its evidence
// and judges its usefulness. Deterministic workflow and safety checks remain in
// the other Harness layers.
type SemanticEvaluator interface {
	Evaluate(context.Context, domain.HarnessCase, *domain.Run) (domain.SemanticEvaluation, error)
}

type EinoSemanticEvaluator struct {
	model     model.BaseChatModel
	modelName string
}

type semanticJudgeResponse struct {
	Groundedness       float64  `json:"groundedness"`
	NumericAccuracy    float64  `json:"numeric_accuracy"`
	ActionSupport      float64  `json:"action_support"`
	Completeness       float64  `json:"completeness"`
	Clarity            float64  `json:"clarity"`
	ApprovalDisclosure float64  `json:"approval_disclosure"`
	UnsupportedClaims  []string `json:"unsupported_claims"`
	NumericErrors      []string `json:"numeric_errors"`
	UnsupportedActions []string `json:"unsupported_actions"`
	Rationale          string   `json:"rationale"`
}

func NewEinoSemanticEvaluator(ctx context.Context, apiKey, baseURL, modelName string) (*EinoSemanticEvaluator, error) {
	temperature := float32(0)
	chatModel, err := openai.NewChatModel(ctx, &openai.ChatModelConfig{
		APIKey: apiKey, BaseURL: baseURL, Model: modelName,
		Temperature: &temperature, Timeout: 60 * time.Second,
	})
	if err != nil {
		return nil, err
	}
	return &EinoSemanticEvaluator{model: chatModel, modelName: modelName}, nil
}

func (e *EinoSemanticEvaluator) Evaluate(ctx context.Context, testCase domain.HarnessCase, run *domain.Run) (domain.SemanticEvaluation, error) {
	if run == nil || run.Result == nil {
		return domain.SemanticEvaluation{}, fmt.Errorf("cannot evaluate an empty diagnosis result")
	}
	started := time.Now()
	payload, err := json.Marshal(map[string]any{
		"question":                        testCase.Question,
		"expected_signals":                testCase.ExpectedSignals,
		"expected_operations":             testCase.ExpectedOperations,
		"summary":                         run.Result.Summary,
		"detected_signals":                run.Result.Signals,
		"evidence":                        run.Result.Evidence,
		"proposed_actions":                run.Result.Actions,
		"has_medium_or_high_risk_actions": hasGuardedAction(run.Result.Actions),
	})
	if err != nil {
		return domain.SemanticEvaluation{}, err
	}
	messages := []*schema.Message{
		schema.SystemMessage(`你是独立的商家经营 Agent 评测器，同时承担事实核验器和回答质量裁判职责。
只能依据输入中的 evidence、detected_signals 和 proposed_actions 评分，不得补充外部知识。
逐项检查 summary 中的数字、因果结论和行动建议是否有证据支持。
六个维度均使用 0 到 5 分：groundedness（事实有据）、numeric_accuracy（数字准确）、action_support（行动有据）、completeness（覆盖问题）、clarity（中文清晰度）、approval_disclosure（存在中高风险行动时明确说明需人工审批；不存在则给5分）。
unsupported_claims、numeric_errors、unsupported_actions 必须列出具体问题，没有问题时返回空数组。
严格使用以下结构和字段名：
{"groundedness":5,"numeric_accuracy":5,"action_support":5,"completeness":5,"clarity":5,"approval_disclosure":5,"unsupported_claims":[],"numeric_errors":[],"unsupported_actions":[],"rationale":"简短中文理由"}
只输出一个合法 JSON 对象，不要 Markdown，不要代码围栏。`),
		schema.UserMessage(string(payload)),
	}
	response, err := e.model.Generate(ctx, messages)
	if err != nil {
		return domain.SemanticEvaluation{}, fmt.Errorf("judge model %s failed: %w", e.modelName, err)
	}
	var judged semanticJudgeResponse
	if err := decodeJSONObject(response.Content, &judged); err != nil {
		return domain.SemanticEvaluation{}, fmt.Errorf("judge model %s returned invalid JSON: %w", e.modelName, err)
	}
	result := domain.SemanticEvaluation{
		Provider: "eino-openai-compatible", Model: e.modelName,
		Groundedness: clampRubric(judged.Groundedness), NumericAccuracy: clampRubric(judged.NumericAccuracy),
		ActionSupport: clampRubric(judged.ActionSupport), Completeness: clampRubric(judged.Completeness),
		Clarity: clampRubric(judged.Clarity), ApprovalDisclosure: clampRubric(judged.ApprovalDisclosure),
		UnsupportedClaims: judged.UnsupportedClaims, NumericErrors: judged.NumericErrors,
		UnsupportedActions: judged.UnsupportedActions, Rationale: strings.TrimSpace(judged.Rationale),
		DurationMS: time.Since(started).Milliseconds(),
	}
	weighted := .30*result.Groundedness + .20*result.NumericAccuracy + .20*result.ActionSupport +
		.15*result.Completeness + .10*result.Clarity + .05*result.ApprovalDisclosure
	result.Score = round(weighted / 5)
	result.Passed = result.Groundedness >= 4 && result.NumericAccuracy == 5 && result.ActionSupport >= 4 &&
		result.ApprovalDisclosure >= 4 && result.Score >= .80 && len(result.UnsupportedClaims) == 0 &&
		len(result.NumericErrors) == 0 && len(result.UnsupportedActions) == 0
	return result, nil
}

func scoreSemantic(result domain.SemanticEvaluation, err error) domain.HarnessLayerReport {
	if err != nil {
		return layerWithFailures("model_quality", 0, false, true, map[string]float64{
			"evaluator_available": 0,
		}, []string{"LLM 评测不可用：" + err.Error()})
	}
	metrics := map[string]float64{
		"evaluator_available": 1, "groundedness": result.Groundedness / 5,
		"numeric_accuracy": result.NumericAccuracy / 5, "action_support": result.ActionSupport / 5,
		"completeness": result.Completeness / 5, "clarity": result.Clarity / 5,
		"approval_disclosure": result.ApprovalDisclosure / 5, "judge_latency_ms": float64(result.DurationMS),
		"unsupported_claims": float64(len(result.UnsupportedClaims)), "numeric_errors": float64(len(result.NumericErrors)),
		"unsupported_actions": float64(len(result.UnsupportedActions)),
	}
	var failures []string
	for _, item := range result.UnsupportedClaims {
		failures = append(failures, "无证据结论："+item)
	}
	for _, item := range result.NumericErrors {
		failures = append(failures, "数字错误："+item)
	}
	for _, item := range result.UnsupportedActions {
		failures = append(failures, "行动缺少依据："+item)
	}
	if !result.Passed && len(failures) == 0 {
		failures = append(failures, "模型回答质量未达到事实一致性与可用性门槛")
	}
	return layerWithFailures("model_quality", result.Score, result.Passed, true, metrics, failures)
}

func decodeJSONObject(content string, target any) error {
	content = strings.TrimSpace(content)
	start, end := strings.Index(content, "{"), strings.LastIndex(content, "}")
	if start < 0 || end < start {
		return fmt.Errorf("response contains no JSON object")
	}
	decoder := json.NewDecoder(strings.NewReader(content[start : end+1]))
	decoder.DisallowUnknownFields()
	return decoder.Decode(target)
}

func hasGuardedAction(actions []domain.Action) bool {
	for _, action := range actions {
		if action.Risk == domain.RiskMedium || action.Risk == domain.RiskHigh {
			return true
		}
	}
	return false
}

func clampRubric(value float64) float64 {
	if value < 0 {
		return 0
	}
	if value > 5 {
		return 5
	}
	return value
}
