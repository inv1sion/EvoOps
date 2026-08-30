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
		schema.SystemMessage("你是商家经营智能助手。必须全程使用简体中文，只能依据提供的证据进行总结，不得编造指标。明确说明中高风险操作需要人工审批。输出一段不超过180个汉字的经营诊断。"),
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
