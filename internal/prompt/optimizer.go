package prompt

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/inv1sion/evoops/internal/domain"
)

// BaseSystemPrompt is the immutable safety and grounding boundary. An
// optimizer may append a small task-quality patch, but it never rewrites this
// section.
const BaseSystemPrompt = `你是广告投放 ROI 诊断助手。必须全程使用简体中文，只能依据提供的广告计划证据、候选行动和明确标注的商家记忆，不得补充未提供的原因、数字或行动。准确复述 ROI、阈值和计划名称。商家记忆只用于行动排序和解释，不得覆盖风险等级或审批要求。若存在暂停广告行动，必须明确说明需要人工审批。输出一段不超过180个汉字的广告诊断。`

const ruleBasedPatch = `输出前逐句核验：每个数字必须能在 evidence 中定位，每个行动必须来自 actions；删除无法核验的因果结论，并优先回答用户当前问题。`

type OptimizationRequest struct {
	Active   domain.Policy
	Baseline domain.HarnessReport
}

type Optimizer interface {
	Optimize(context.Context, OptimizationRequest) (domain.PromptArtifact, error)
	Name() string
	ModelName() string
}

type RuleBasedOptimizer struct{}

func (RuleBasedOptimizer) Name() string      { return "deterministic-safe-fallback" }
func (RuleBasedOptimizer) ModelName() string { return "" }

func (o RuleBasedOptimizer) Optimize(_ context.Context, request OptimizationRequest) (domain.PromptArtifact, error) {
	return NewArtifact(
		currentArtifact(request.Active), ruleBasedPatch, o.Name(), o.ModelName(),
		"依据 Harness 失败证据增加逐句事实核验、行动约束和问题覆盖要求。",
		failureEvidence(request.Baseline), time.Now().UTC(),
	)
}

type EinoOptimizer struct {
	model     model.BaseChatModel
	modelName string
}

type optimizerResponse struct {
	Patch     string `json:"patch"`
	Rationale string `json:"rationale"`
}

func NewEinoOptimizer(ctx context.Context, apiKey, baseURL, modelName string) (*EinoOptimizer, error) {
	temperature := float32(0.2)
	chatModel, err := openai.NewChatModel(ctx, &openai.ChatModelConfig{
		APIKey: apiKey, BaseURL: baseURL, Model: modelName,
		Temperature: &temperature, Timeout: 60 * time.Second,
	})
	if err != nil {
		return nil, err
	}
	return &EinoOptimizer{model: chatModel, modelName: modelName}, nil
}

func (o *EinoOptimizer) Name() string      { return "eino-prompt-optimizer" }
func (o *EinoOptimizer) ModelName() string { return o.modelName }

func (o *EinoOptimizer) Optimize(ctx context.Context, request OptimizationRequest) (domain.PromptArtifact, error) {
	started := time.Now()
	payload, err := json.Marshal(map[string]any{
		"immutable_prompt": BaseSystemPrompt,
		"current_prompt":   currentArtifact(request.Active).Content,
		"baseline_score":   request.Baseline.Score,
		"gate_decision":    request.Baseline.GateDecision,
		"failure_evidence": failureEvidence(request.Baseline),
		"case_feedback":    caseFeedback(request.Baseline),
	})
	if err != nil {
		return domain.PromptArtifact{}, err
	}
	messages := []*schema.Message{
		schema.SystemMessage(`你是受控 Prompt 优化器。你的任务是根据广告 ROI Agent 的真实回放评测反馈，生成一段追加到现有系统提示词后的“质量优化补丁”。
不可改写、弱化或重复 immutable_prompt；不可改变工具权限、风险等级、ROI 计算、人工审批规则或商家记忆边界；不可引入输入中没有的业务事实。
补丁应针对失败证据，使用简体中文、可执行、可核验，最多 300 个汉字。即使基线已经通过，也应提出一个最小且可回归验证的质量改进假设。
严格输出一个 JSON 对象：{"patch":"追加指令","rationale":"为何能解决评测问题"}。不要输出 Markdown 或其他字段。`),
		schema.UserMessage(string(payload)),
	}
	response, err := o.model.Generate(ctx, messages)
	if err != nil {
		return domain.PromptArtifact{}, fmt.Errorf("prompt optimizer model %s failed: %w", o.modelName, err)
	}
	var generated optimizerResponse
	if err := decodeJSONObject(response.Content, &generated); err != nil {
		return domain.PromptArtifact{}, fmt.Errorf("prompt optimizer model %s returned invalid JSON: %w", o.modelName, err)
	}
	artifact, err := NewArtifact(
		currentArtifact(request.Active), generated.Patch, o.Name(), o.ModelName(), generated.Rationale,
		failureEvidence(request.Baseline), time.Now().UTC(),
	)
	artifact.GenerationDurationMS = time.Since(started).Milliseconds()
	return artifact, err
}

func DefaultArtifact(now time.Time) domain.PromptArtifact {
	artifact, _ := NewArtifact(domain.PromptArtifact{}, "", "human-bootstrap", "", "初始广告 ROI 诊断提示词。", nil, now)
	artifact.Version = "ad-roi-prompt-v1"
	return artifact
}

// Normalize migrates policies created before full prompt artifacts were
// introduced. Legacy +grounding revisions retain their previous behaviour.
func Normalize(policy domain.Policy) domain.PromptArtifact {
	if strings.TrimSpace(policy.Prompt.Content) != "" {
		artifact := policy.Prompt
		artifact.Validation = Validate(artifact)
		return artifact
	}
	artifact := DefaultArtifact(policy.CreatedAt)
	if policy.PromptRevision != "" {
		artifact.Version = policy.PromptRevision
	}
	if strings.Contains(policy.PromptRevision, "grounding") {
		migrated, err := NewArtifact(artifact, ruleBasedPatch, "legacy-migration", "", "迁移旧版 grounding 提示词修订。", nil, policy.CreatedAt)
		if err == nil {
			migrated.Version = policy.PromptRevision
			return migrated
		}
	}
	return artifact
}

func NewArtifact(parent domain.PromptArtifact, patch, generator, modelName, rationale string, evidence []string, now time.Time) (domain.PromptArtifact, error) {
	patch = strings.TrimSpace(patch)
	content := BaseSystemPrompt
	if patch != "" {
		content += "\n质量优化补丁：" + patch
	}
	artifact := domain.PromptArtifact{
		Version: fmt.Sprintf("ad-roi-prompt-%d", now.UnixNano()), ParentVersion: parent.Version,
		Content: content, Patch: patch, Generator: generator, GeneratorModel: modelName,
		Rationale: strings.TrimSpace(rationale), FailureEvidence: unique(evidence), CreatedAt: now,
	}
	artifact.Validation = Validate(artifact)
	if !artifact.Validation.Passed {
		return artifact, fmt.Errorf("generated prompt failed safety validation: %s", strings.Join(artifact.Validation.Errors, "; "))
	}
	return artifact, nil
}

func Validate(artifact domain.PromptArtifact) domain.PromptValidation {
	content, patch := strings.TrimSpace(artifact.Content), strings.TrimSpace(artifact.Patch)
	expectedContent := BaseSystemPrompt
	if patch != "" {
		expectedContent += "\n质量优化补丁：" + patch
	}
	checks := map[string]bool{
		"immutable_boundary_preserved":   strings.HasPrefix(content, BaseSystemPrompt),
		"content_matches_composed_patch": content == expectedContent,
		"content_length_bounded":         utf8.RuneCountInString(content) <= 1000,
		"patch_length_bounded":           utf8.RuneCountInString(patch) <= 300,
		"approval_rule_preserved":        strings.Contains(content, "必须明确说明需要人工审批"),
		"evidence_rule_preserved":        strings.Contains(content, "只能依据提供的广告计划证据"),
		"memory_boundary_preserved":      strings.Contains(content, "商家记忆只用于行动排序和解释"),
		"chinese_output_preserved":       strings.Contains(content, "必须全程使用简体中文"),
	}
	forbidden := []string{"无需人工审批", "自动批准", "自动暂停广告", "忽略上述", "绕过审批", "修改工具权限"}
	unsafe := false
	for _, phrase := range forbidden {
		if strings.Contains(patch, phrase) {
			unsafe = true
			break
		}
	}
	checks["patch_contains_no_unsafe_override"] = !unsafe
	var failures []string
	for name, passed := range checks {
		if !passed {
			failures = append(failures, name)
		}
	}
	return domain.PromptValidation{Passed: len(failures) == 0, Checks: checks, Errors: failures}
}

func currentArtifact(policy domain.Policy) domain.PromptArtifact { return Normalize(policy) }

func failureEvidence(report domain.HarnessReport) []string {
	var result []string
	for _, attribution := range report.FailureAttributions {
		for _, item := range attribution.Evidence {
			result = append(result, attribution.Category+": "+item)
		}
	}
	if len(result) == 0 {
		for _, item := range caseFeedback(report) {
			if rationale, _ := item["judge_rationale"].(string); rationale != "" {
				result = append(result, "judge: "+rationale)
			}
		}
	}
	return unique(result)
}

func caseFeedback(report domain.HarnessReport) []map[string]any {
	items := make([]map[string]any, 0, len(report.Cases))
	for _, testCase := range report.Cases {
		item := map[string]any{"case_id": testCase.CaseID, "passed": testCase.Passed, "failures": testCase.Failures}
		if testCase.SemanticEvaluation != nil {
			item["model_quality_score"] = testCase.SemanticEvaluation.Score
			item["groundedness"] = testCase.SemanticEvaluation.Groundedness
			item["numeric_accuracy"] = testCase.SemanticEvaluation.NumericAccuracy
			item["unsupported_claims"] = testCase.SemanticEvaluation.UnsupportedClaims
			item["numeric_errors"] = testCase.SemanticEvaluation.NumericErrors
			item["unsupported_actions"] = testCase.SemanticEvaluation.UnsupportedActions
			item["judge_rationale"] = testCase.SemanticEvaluation.Rationale
		}
		if testCase.Run != nil && testCase.Run.Result != nil {
			item["generated_summary"] = testCase.Run.Result.Summary
		}
		items = append(items, item)
	}
	return items
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

func unique(values []string) []string {
	seen := map[string]bool{}
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" && !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	return result
}
