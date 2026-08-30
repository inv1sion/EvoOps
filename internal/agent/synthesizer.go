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
	model    model.BaseChatModel
	fallback LocalSynthesizer
}

func NewEinoSynthesizer(ctx context.Context, apiKey, baseURL, modelName string) (*EinoSynthesizer, error) {
	chatModel, err := openai.NewChatModel(ctx, &openai.ChatModelConfig{
		APIKey: apiKey, BaseURL: baseURL, Model: modelName, Timeout: 45 * time.Second,
	})
	if err != nil {
		return nil, err
	}
	return &EinoSynthesizer{model: chatModel}, nil
}

func (s *EinoSynthesizer) Name() string { return "eino-openai-compatible" }

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
		schema.SystemMessage("You are an operations copilot. Summarize only the supplied evidence. Never invent metrics. Mention that medium/high-risk actions require approval. Keep the response under 120 words."),
		schema.UserMessage(string(payload)),
	}
	response, err := s.model.Generate(ctx, messages)
	if err != nil {
		fallback, _ := s.fallback.Synthesize(ctx, request, analysis)
		return fallback, fmt.Errorf("model synthesis failed; deterministic fallback used: %w", err)
	}
	if strings.TrimSpace(response.Content) == "" {
		return s.fallback.Synthesize(ctx, request, analysis)
	}
	return response.Content, nil
}
