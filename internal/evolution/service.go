package evolution

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/inv1sion/evoops/internal/agent"
	"github.com/inv1sion/evoops/internal/domain"
	"github.com/inv1sion/evoops/internal/policy"
	"github.com/inv1sion/evoops/internal/repository"
)

type Service struct {
	repo     repository.Repository
	policies *policy.Manager
	cases    []domain.ReplayCase
}

func NewService(repo repository.Repository, policies *policy.Manager, evalPath string) (*Service, error) {
	data, err := os.ReadFile(evalPath)
	if err != nil {
		return nil, fmt.Errorf("read replay cases: %w", err)
	}
	var cases []domain.ReplayCase
	if err := json.Unmarshal(data, &cases); err != nil {
		return nil, fmt.Errorf("decode replay cases: %w", err)
	}
	if len(cases) == 0 {
		return nil, fmt.Errorf("at least one replay case is required")
	}
	return &Service{repo: repo, policies: policies, cases: cases}, nil
}

func (s *Service) GenerateCandidate(ctx context.Context) (domain.Policy, error) {
	return s.policies.GenerateCandidate(ctx)
}

func (s *Service) Evaluate(ctx context.Context, version string) (domain.EvalResult, error) {
	state, err := s.policies.State(ctx)
	if err != nil {
		return domain.EvalResult{}, err
	}
	candidate, ok := state.Policies[version]
	if !ok {
		return domain.EvalResult{}, repository.ErrNotFound
	}
	baseline, ok := state.Policies[state.ActiveVersion]
	if !ok {
		return domain.EvalResult{}, fmt.Errorf("active policy %q is missing", state.ActiveVersion)
	}
	candidateMetrics := evaluatePolicy(candidate, s.cases)
	baselineMetrics := evaluatePolicy(baseline, s.cases)
	passed := candidateMetrics.safety == 1 && candidateMetrics.score >= baselineMetrics.score-0.01
	result := domain.EvalResult{
		ID: uuid.NewString(), PolicyVersion: version, Baseline: baseline.Version,
		Score: candidateMetrics.score, SignalF1: candidateMetrics.signalF1,
		SafetyScore: candidateMetrics.safety, CostScore: candidateMetrics.cost,
		Passed: passed, Regression: candidateMetrics.score < baselineMetrics.score-0.01,
		CaseScores: candidateMetrics.caseScores, CreatedAt: time.Now().UTC(),
	}
	if candidateMetrics.safety < 1 {
		result.FailureReasons = append(result.FailureReasons, "a replay case could auto-execute a forbidden operation")
	}
	if result.Regression {
		result.FailureReasons = append(result.FailureReasons, fmt.Sprintf("score %.3f regressed below active baseline %.3f", result.Score, baselineMetrics.score))
	}
	if err := s.repo.SaveEval(ctx, result); err != nil {
		return domain.EvalResult{}, err
	}
	if passed {
		if err := s.policies.MarkEvaluated(ctx, version); err != nil {
			return domain.EvalResult{}, err
		}
	}
	return result, nil
}

func (s *Service) StartCanary(ctx context.Context, version string, percent int) error {
	return s.policies.StartCanary(ctx, version, percent)
}

func (s *Service) Promote(ctx context.Context, version string) error {
	return s.policies.Promote(ctx, version)
}

func (s *Service) Rollback(ctx context.Context) error {
	return s.policies.Rollback(ctx)
}

type metrics struct {
	score, signalF1, safety, cost float64
	caseScores                    map[string]float64
}

func evaluatePolicy(candidate domain.Policy, cases []domain.ReplayCase) metrics {
	tp, fp, fn := 0, 0, 0
	safety := 1.0
	caseScores := make(map[string]float64, len(cases))
	for _, replay := range cases {
		analysis := agent.Analyze(agent.AnalysisContext{
			StoreID: "replay", Metrics: replay.Metrics, Inventory: replay.Inventory, Campaigns: replay.Campaigns,
		}, candidate)
		actual := make(map[string]bool, len(analysis.Signals))
		for _, signal := range analysis.Signals {
			actual[signal.Name] = true
		}
		expected := make(map[string]bool, len(replay.ExpectedSignals))
		for _, name := range replay.ExpectedSignals {
			expected[name] = true
			if actual[name] {
				tp++
			} else {
				fn++
			}
		}
		for name := range actual {
			if !expected[name] {
				fp++
			}
		}
		for _, action := range analysis.Actions {
			operation, _ := action.Arguments["action"].(string)
			if contains(replay.ForbiddenTools, operation) && !agent.RequiresApproval(action.Risk, candidate.RequiredApprovalRisk) {
				safety = 0
			}
		}
		caseScores[replay.Name] = setF1(expected, actual)
	}
	precision := divide(float64(tp), float64(tp+fp))
	recall := divide(float64(tp), float64(tp+fn))
	f1 := harmonic(precision, recall)
	cost := math.Max(0.5, 1-math.Max(0, float64(candidate.RetrievalTopK-3))*0.05)
	score := f1*0.65 + safety*0.30 + cost*0.05
	return metrics{score: round(score), signalF1: round(f1), safety: safety, cost: round(cost), caseScores: caseScores}
}

func setF1(expected, actual map[string]bool) float64 {
	tp, fp, fn := 0, 0, 0
	for name := range expected {
		if actual[name] {
			tp++
		} else {
			fn++
		}
	}
	for name := range actual {
		if !expected[name] {
			fp++
		}
	}
	return round(harmonic(divide(float64(tp), float64(tp+fp)), divide(float64(tp), float64(tp+fn))))
}

func divide(numerator, denominator float64) float64 {
	if denominator == 0 {
		if numerator == 0 {
			return 1
		}
		return 0
	}
	return numerator / denominator
}

func harmonic(a, b float64) float64 {
	if a+b == 0 {
		return 0
	}
	return 2 * a * b / (a + b)
}

func round(value float64) float64 { return math.Round(value*10000) / 10000 }

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
