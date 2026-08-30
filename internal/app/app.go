package app

import (
	"context"
	"fmt"

	"github.com/inv1sion/evoops/internal/agent"
	"github.com/inv1sion/evoops/internal/config"
	"github.com/inv1sion/evoops/internal/dataset"
	"github.com/inv1sion/evoops/internal/evolution"
	"github.com/inv1sion/evoops/internal/harness"
	"github.com/inv1sion/evoops/internal/policy"
	"github.com/inv1sion/evoops/internal/repository"
	"github.com/inv1sion/evoops/internal/tools"
)

type App struct {
	Config    config.Config
	Repo      repository.Repository
	Policies  *policy.Manager
	Tools     *tools.Registry
	Agent     *agent.Engine
	Harness   *harness.Suite
	Evolution *evolution.Service
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
	registry := tools.NewRegistry()
	if err := tools.RegisterLocal(ctx, registry, data); err != nil {
		return nil, fmt.Errorf("register local tools: %w", err)
	}
	for _, endpoint := range cfg.MCPSSEURLs {
		if err := registry.ConnectMCP(ctx, endpoint, cfg.MCPAllowlist); err != nil {
			registry.Close()
			return nil, err
		}
	}
	policies, err := policy.NewManager(ctx, repo)
	if err != nil {
		registry.Close()
		return nil, err
	}
	var synthesizer agent.Synthesizer = agent.LocalSynthesizer{}
	if cfg.OpenAIAPIKey != "" {
		realModel, err := agent.NewEinoSynthesizer(ctx, cfg.OpenAIAPIKey, cfg.OpenAIBaseURL, cfg.OpenAIModel)
		if err != nil {
			registry.Close()
			return nil, fmt.Errorf("initialize model: %w", err)
		}
		synthesizer = realModel
	}
	engine, err := agent.NewEngine(ctx, repo, policies, registry, synthesizer)
	if err != nil {
		registry.Close()
		return nil, err
	}
	harnessSuite, err := harness.Load(cfg.HarnessDataPath, engine)
	if err != nil {
		registry.Close()
		return nil, err
	}
	evolutionService := evolution.NewService(repo, policies, harnessSuite)
	return &App{Config: cfg, Repo: repo, Policies: policies, Tools: registry, Agent: engine, Harness: harnessSuite, Evolution: evolutionService}, nil
}

func (a *App) Close() error { return a.Tools.Close() }
