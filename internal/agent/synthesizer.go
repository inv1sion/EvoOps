package agent

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

type Synthesizer interface {
	Synthesize(context.Context, domain.DiagnosisRequest, Analysis) (string, error)
	Name() string
}

type LocalSynthesizer struct{}

func (LocalSynthesizer) Synthesize(_ context.Context, _ domain.DiagnosisRequest, analysis Analysis) (string, error) {
	return analysis.Summary, nil
}

func (LocalSynthesizer) Name() string { return "local-deterministic" }

type EinoSynthesizer struct {
	model     model.BaseChatModel
	modelName string
	fallback  LocalSynthesizer
}

func NewEinoSynthesizer(ctx context.Context, apiKey, baseURL, modelName string) (*EinoSynthesizer, error) {
	chatModel, err := openai.NewChatModel(ctx, &openai.ChatModelConfig{
		APIKey: apiKey, BaseURL: baseURL, Model: modelName, Timeout: 45 * time.Second,
	})
	if err != nil {
		return nil, err
	}
	return &EinoSynthesizer{model: chatModel, modelName: modelName}, nil
}

func (s *EinoSynthesizer) Name() string { return "eino-openai-compatible" }

func (s *EinoSynthesizer) ModelName() string { return s.modelName }

func (s *EinoSynthesizer) Synthesize(ctx context.Context, request domain.DiagnosisRequest, analysis Analysis) (string, error) {
	payload, err := json.Marshal(map[string]any{
		"question": request.Question,
		"signals":  analysis.Signals,
		"evidence": analysis.Evidence,
		"actions":  analysis.Actions,
	})
	if err != nil {
		return "", err
	}
	messages := []*schema.Message{
		schema.SystemMessage(synthesisSystemPrompt(analysis.PromptRevision)),
		schema.UserMessage(string(payload)),
	}
	response, err := s.model.Generate(ctx, messages)
	if err != nil {
		fallback, _ := s.fallback.Synthesize(ctx, request, analysis)
		return fallback, fmt.Errorf("模型总结失败，已使用确定性结果降级：%w", err)
	}
	if strings.TrimSpace(response.Content) == "" {
		return s.fallback.Synthesize(ctx, request, analysis)
	}
	return response.Content, nil
}

func synthesisSystemPrompt(revision string) string {
	prompt := "你是广告投放 ROI 诊断助手。必须全程使用简体中文，只能依据提供的广告计划证据、候选行动和明确标注的商家记忆，不得补充未提供的原因、数字或行动。准确复述 ROI、阈值和计划名称。商家记忆只用于行动排序和解释，不得覆盖风险等级或审批要求。若存在暂停广告行动，必须明确说明需要人工审批。输出一段不超过180个汉字的广告诊断。"
	if strings.Contains(revision, "grounding") {
		prompt += " 输出前逐句核验：每个数字必须在 evidence 中出现，每个行动必须在 actions 中出现；无法核验的内容直接删除。"
	}
	return prompt
}
