package app

import (
	"context"
	"fmt"
	"time"

	"github.com/inv1sion/evoops/internal/agent"
	"github.com/inv1sion/evoops/internal/config"
	"github.com/inv1sion/evoops/internal/dataset"
	"github.com/inv1sion/evoops/internal/evolution"
	"github.com/inv1sion/evoops/internal/harness"
	"github.com/inv1sion/evoops/internal/memory"
	"github.com/inv1sion/evoops/internal/policy"
	promptpolicy "github.com/inv1sion/evoops/internal/prompt"
	"github.com/inv1sion/evoops/internal/rag"
	"github.com/inv1sion/evoops/internal/repository"
	"github.com/inv1sion/evoops/internal/tools"
)

type App struct {
	Config    config.Config
	Repo      repository.Repository
	Policies  *policy.Manager
	Tools     *tools.Registry
	Agent     *agent.Engine
	Memory    *memory.Service
	Harness   *harness.Suite
	Evolution *evolution.Service
	RAG       *rag.Service
}

func New(ctx context.Context, cfg config.Config) (*App, error) {
	repo, err := repository.NewFile(cfg.DataDir)
	if err != nil {
		return nil, err
	}
	data, err := dataset.LoadFile(cfg.DemoDataPath)
	if err != nil {
		return nil, err
	}
	ragService, err := configureRAG(ctx, cfg, data)
	if err != nil {
		return nil, err
	}
	registry := tools.NewRegistry()
	if err := tools.RegisterLocal(ctx, registry, data); err != nil {
		closeRAG(ragService)
		return nil, fmt.Errorf("register local tools: %w", err)
	}
	for _, endpoint := range cfg.MCPSSEURLs {
		if err := registry.ConnectMCP(ctx, endpoint, cfg.MCPAllowlist); err != nil {
			registry.Close()
			closeRAG(ragService)
			return nil, err
		}
	}
	policies, err := policy.NewManager(ctx, repo)
	if err != nil {
		registry.Close()
		closeRAG(ragService)
		return nil, err
	}
	memoryService := memory.NewService(repo)
	var synthesizer agent.Synthesizer = agent.LocalSynthesizer{}
	if cfg.OpenAIAPIKey != "" {
		realModel, err := agent.NewEinoSynthesizer(ctx, cfg.OpenAIAPIKey, cfg.OpenAIBaseURL, cfg.OpenAIModel)
		if err != nil {
			registry.Close()
			closeRAG(ragService)
			return nil, fmt.Errorf("initialize model: %w", err)
		}
		synthesizer = realModel
	}
	var engine *agent.Engine
	if cfg.OpenAIAPIKey != "" && cfg.ToolCallingEnabled {
		planner, plannerErr := agent.NewEinoToolCallingPlanner(ctx, cfg.OpenAIAPIKey, cfg.OpenAIBaseURL, cfg.OpenAIModel)
		if plannerErr != nil {
			registry.Close()
			closeRAG(ragService)
			return nil, fmt.Errorf("initialize tool calling: %w", plannerErr)
		}
		engine, err = agent.NewToolCallingEngine(ctx, repo, policies, registry, synthesizer, planner, memoryService)
	} else {
		engine, err = agent.NewEngine(ctx, repo, policies, registry, synthesizer, memoryService)
	}
	if err != nil {
		registry.Close()
		closeRAG(ragService)
		return nil, err
	}
	var evaluators []harness.SemanticEvaluator
	var promptOptimizer promptpolicy.Optimizer = promptpolicy.RuleBasedOptimizer{}
	if cfg.OpenAIAPIKey != "" && cfg.LLMEvalEnabled {
		semanticEvaluator, err := harness.NewEinoSemanticEvaluator(ctx, cfg.OpenAIAPIKey, cfg.OpenAIBaseURL, cfg.JudgeModel)
		if err != nil {
			registry.Close()
			closeRAG(ragService)
			return nil, fmt.Errorf("initialize judge model: %w", err)
		}
		evaluators = append(evaluators, semanticEvaluator)
	}
	if cfg.OpenAIAPIKey != "" {
		modelName := cfg.PromptOptimizerModel
		if modelName == "" {
			modelName = cfg.OpenAIModel
		}
		generatedOptimizer, err := promptpolicy.NewEinoOptimizer(ctx, cfg.OpenAIAPIKey, cfg.OpenAIBaseURL, modelName)
		if err != nil {
			registry.Close()
			closeRAG(ragService)
			return nil, fmt.Errorf("initialize prompt optimizer: %w", err)
		}
		promptOptimizer = generatedOptimizer
	}
	harnessSuite, err := harness.Load(cfg.HarnessDataPath, engine, evaluators...)
	if err != nil {
		registry.Close()
		closeRAG(ragService)
		return nil, err
	}
	evolutionService := evolution.NewService(repo, policies, harnessSuite, promptOptimizer)
	return &App{Config: cfg, Repo: repo, Policies: policies, Tools: registry, Agent: engine, Memory: memoryService, Harness: harnessSuite, Evolution: evolutionService, RAG: ragService}, nil
}

func configureRAG(ctx context.Context, cfg config.Config, data *dataset.MemoryRepository) (*rag.Service, error) {
	switch cfg.RAGBackend {
	case "", "local":
		return nil, nil
	case "external":
	default:
		return nil, fmt.Errorf("unsupported EVOOPS_RAG_BACKEND %q; use local or external", cfg.RAGBackend)
	}
	if cfg.OpenAIAPIKey == "" {
		return nil, fmt.Errorf("EVOOPS_RAG_BACKEND=external requires OPENAI_API_KEY")
	}
	if cfg.RAGRerankURL == "" {
		return nil, fmt.Errorf("EVOOPS_RAG_BACKEND=external requires the workspace-specific EVOOPS_RAG_RERANK_URL")
	}
	service, err := rag.Open(ctx, rag.RuntimeConfig{
		PostgresURL: cfg.RAGPostgresURL, RedisAddress: cfg.RAGRedisAddress, RedisPassword: cfg.RAGRedisPassword,
		RedisDB: cfg.RAGRedisDB, RedisTTL: time.Duration(cfg.RAGRedisTTLSeconds) * time.Second,
		MilvusAddress: cfg.RAGMilvusAddress, MilvusToken: cfg.RAGMilvusToken, MilvusDatabase: cfg.RAGMilvusDatabase,
		MilvusCollection: cfg.RAGMilvusCollection, EmbeddingAPIKey: cfg.OpenAIAPIKey, EmbeddingBaseURL: cfg.RAGEmbeddingBaseURL,
		EmbeddingModel: cfg.RAGEmbeddingModel, EmbeddingDimension: cfg.RAGEmbeddingDimensions,
		RerankAPIKey: cfg.OpenAIAPIKey, RerankURL: cfg.RAGRerankURL, RerankModel: cfg.RAGRerankModel,
		MaxContextChars: cfg.RAGMaxContextChars,
	})
	if err != nil {
		return nil, fmt.Errorf("initialize external RAG: %w", err)
	}
	if cfg.RAGAutoSeed {
		for _, article := range data.PlatformKnowledge() {
			_, err := service.Ingest(ctx, rag.IngestInput{Scope: rag.ScopePlatform, Title: article.Title,
				SourceURI: "demo://knowledge/" + article.ID, MediaType: "text/plain", Content: article.Content,
				Metadata: map[string]any{"article_id": article.ID, "tags": article.Tags}})
			if err != nil {
				_ = service.Close(context.Background())
				return nil, fmt.Errorf("seed demo knowledge %s: %w", article.ID, err)
			}
		}
	}
	data.SetKnowledgeSearcher(service)
	return service, nil
}

func closeRAG(service *rag.Service) {
	if service != nil {
		_ = service.Close(context.Background())
	}
}

func (a *App) Close() error {
	toolsErr := a.Tools.Close()
	if a.RAG != nil {
		if ragErr := a.RAG.Close(context.Background()); toolsErr == nil {
			return ragErr
		}
	}
	return toolsErr
}
