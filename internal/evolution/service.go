package evolution

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/google/uuid"
	"github.com/inv1sion/evoops/internal/domain"
	"github.com/inv1sion/evoops/internal/harness"
	"github.com/inv1sion/evoops/internal/policy"
	promptpolicy "github.com/inv1sion/evoops/internal/prompt"
	"github.com/inv1sion/evoops/internal/repository"
)

type Service struct {
	repo      repository.Repository
	policies  *policy.Manager
	harness   *harness.Suite
	optimizer promptpolicy.Optimizer
}

func NewService(repo repository.Repository, policies *policy.Manager, suite *harness.Suite, optimizers ...promptpolicy.Optimizer) *Service {
	var optimizer promptpolicy.Optimizer = promptpolicy.RuleBasedOptimizer{}
	if len(optimizers) > 0 && optimizers[0] != nil {
		optimizer = optimizers[0]
	}
	return &Service{repo: repo, policies: policies, harness: suite, optimizer: optimizer}
}

// RunHarness evaluates any stored policy. Candidate policies are compared
// against a freshly replayed active baseline from the same suite and runtime.
func (s *Service) RunHarness(ctx context.Context, version string) (domain.HarnessReport, error) {
	state, err := s.policies.State(ctx)
	if err != nil {
		return domain.HarnessReport{}, err
	}
	selected, ok := state.Policies[version]
	if !ok {
		return domain.HarnessReport{}, repository.ErrNotFound
	}
	active, ok := state.Policies[state.ActiveVersion]
	if !ok {
		return domain.HarnessReport{}, fmt.Errorf("active policy %q is missing", state.ActiveVersion)
	}
	baseline, err := s.harness.Evaluate(ctx, active, nil)
	if err != nil {
		return domain.HarnessReport{}, err
	}
	if err := s.repo.SaveHarnessReport(ctx, baseline); err != nil {
		return domain.HarnessReport{}, err
	}
	if selected.Version == active.Version {
		return baseline, nil
	}
	report, err := s.harness.Evaluate(ctx, selected, &baseline)
	if err != nil {
		return domain.HarnessReport{}, err
	}
	if err := s.repo.SaveHarnessReport(ctx, report); err != nil {
		return domain.HarnessReport{}, err
	}
	if report.Passed {
		if err := s.policies.MarkEvaluated(ctx, version, report); err != nil {
			return domain.HarnessReport{}, err
		}
	}
	return report, nil
}

// GenerateCandidate first replays the active policy so mutations are derived
// from current failures rather than only from free-form feedback.
func (s *Service) GenerateCandidate(ctx context.Context) (domain.Policy, error) {
	state, err := s.policies.State(ctx)
	if err != nil {
		return domain.Policy{}, err
	}
	active, ok := state.Policies[state.ActiveVersion]
	if !ok {
		return domain.Policy{}, fmt.Errorf("active policy %q is missing", state.ActiveVersion)
	}
	baseline, err := s.harness.Evaluate(ctx, active, nil)
	if err != nil {
		return domain.Policy{}, err
	}
	if err := s.repo.SaveHarnessReport(ctx, baseline); err != nil {
		return domain.Policy{}, err
	}
	return s.generateCandidate(ctx, active, baseline)
}

// Evolve runs the complete closed loop in one reproducible operation:
// baseline -> attribution -> candidate -> candidate Harness -> optional canary.
// It never promotes automatically.
func (s *Service) Evolve(ctx context.Context, canaryPercent int) (domain.EvolutionRun, error) {
	if canaryPercent < 0 || canaryPercent > 50 {
		return domain.EvolutionRun{}, fmt.Errorf("canary percent must be between 0 and 50")
	}
	state, err := s.policies.State(ctx)
	if err != nil {
		return domain.EvolutionRun{}, err
	}
	active, ok := state.Policies[state.ActiveVersion]
	if !ok {
		return domain.EvolutionRun{}, fmt.Errorf("active policy %q is missing", state.ActiveVersion)
	}
	baseline, err := s.harness.Evaluate(ctx, active, nil)
	if err != nil {
		return domain.EvolutionRun{}, err
	}
	if err := s.repo.SaveHarnessReport(ctx, baseline); err != nil {
		return domain.EvolutionRun{}, err
	}
	candidate, err := s.generateCandidate(ctx, active, baseline)
	if err != nil {
		return domain.EvolutionRun{}, err
	}
	candidateReport, err := s.harness.Evaluate(ctx, candidate, &baseline)
	if err != nil {
		return domain.EvolutionRun{}, err
	}
	if err := s.repo.SaveHarnessReport(ctx, candidateReport); err != nil {
		return domain.EvolutionRun{}, err
	}
	status := "blocked"
	if candidateReport.Passed {
		if err := s.policies.MarkEvaluated(ctx, candidate.Version, candidateReport); err != nil {
			return domain.EvolutionRun{}, err
		}
		status = "evaluated"
		if canaryPercent > 0 {
			if err := s.policies.StartCanary(ctx, candidate.Version, canaryPercent); err != nil {
				return domain.EvolutionRun{}, err
			}
			status = "canary"
		}
	}
	run := domain.EvolutionRun{
		ID: uuid.NewString(), BaselineReport: baseline, Candidate: candidate,
		CandidateReport: candidateReport, Attributions: baseline.FailureAttributions,
		Comparison:    compareReports(baseline, candidateReport, active, candidate),
		CanaryPercent: canaryPercent, Status: status, CreatedAt: time.Now().UTC(),
	}
	if err := s.repo.SaveEvolutionRun(ctx, run); err != nil {
		return domain.EvolutionRun{}, err
	}
	return run, nil
}

func (s *Service) generateCandidate(ctx context.Context, active domain.Policy, baseline domain.HarnessReport) (domain.Policy, error) {
	var optimized *domain.PromptArtifact
	if shouldOptimizePrompt(baseline) {
		artifact, err := s.optimizer.Optimize(ctx, promptpolicy.OptimizationRequest{Active: active, Baseline: baseline})
		if err != nil {
			return domain.Policy{}, fmt.Errorf("generate prompt candidate: %w", err)
		}
		optimized = &artifact
	}
	return s.policies.GenerateCandidateWithPrompt(ctx, baseline.FailureAttributions, optimized)
}

func shouldOptimizePrompt(report domain.HarnessReport) bool {
	hasSemanticEvaluation := false
	for _, item := range report.Cases {
		if item.SemanticEvaluation != nil {
			hasSemanticEvaluation = true
			break
		}
	}
	for _, attribution := range report.FailureAttributions {
		if attribution.Category == "model_quality" {
			return true
		}
	}
	// A clean, semantically evaluated baseline may still run one minimal prompt
	// optimization hypothesis. Other failure classes remain isolated.
	return len(report.FailureAttributions) == 0 && hasSemanticEvaluation
}

func compareReports(baseline, candidate domain.HarnessReport, active, proposed domain.Policy) domain.EvolutionComparison {
	comparison := domain.EvolutionComparison{
		CaseCount: len(candidate.Cases), BaselineScore: baseline.Score, CandidateScore: candidate.Score,
		ScoreDelta:           round(candidate.Score - baseline.Score),
		BaselineCasePassRate: casePassRate(baseline), CandidateCasePassRate: casePassRate(candidate),
		BaselineModelQuality: layerScore(baseline, "model_quality"), CandidateModelQuality: layerScore(candidate, "model_quality"),
		BaselineGroundedness: layerMetric(baseline, "model_quality", "groundedness"), CandidateGroundedness: layerMetric(candidate, "model_quality", "groundedness"),
		BaselineNumericAccuracy: layerMetric(baseline, "model_quality", "numeric_accuracy"), CandidateNumericAccuracy: layerMetric(candidate, "model_quality", "numeric_accuracy"),
		BaselineAverageLatencyMS: layerMetric(baseline, "cost", "latency_ms"), CandidateAverageLatencyMS: layerMetric(candidate, "cost", "latency_ms"),
		BaselineAverageCostUnits: layerMetric(baseline, "cost", "cost_units"), CandidateAverageCostUnits: layerMetric(candidate, "cost", "cost_units"),
		BaselineSafetyViolations: layerMetric(baseline, "safety", "violations"), CandidateSafetyViolations: layerMetric(candidate, "safety", "violations"),
		PromptChanged:               active.PromptRevision != proposed.PromptRevision && active.Prompt.Content != proposed.Prompt.Content,
		CandidatePromptValidationOK: proposed.Prompt.Validation.Passed, GateDecision: candidate.GateDecision,
	}
	comparison.ModelQualityDelta = round(comparison.CandidateModelQuality - comparison.BaselineModelQuality)
	return comparison
}

func casePassRate(report domain.HarnessReport) float64 {
	if len(report.Cases) == 0 {
		return 0
	}
	passed := 0
	for _, item := range report.Cases {
		if item.Passed {
			passed++
		}
	}
	return round(float64(passed) / float64(len(report.Cases)))
}

func layerScore(report domain.HarnessReport, name string) float64 {
	for _, layer := range report.Layers {
		if layer.Name == name {
			return layer.Score
		}
	}
	return 0
}

func layerMetric(report domain.HarnessReport, layerName, metric string) float64 {
	for _, layer := range report.Layers {
		if layer.Name == layerName {
			return layer.Metrics[metric]
		}
	}
	return 0
}

func round(value float64) float64 { return math.Round(value*10000) / 10000 }

// Evaluate is retained as a concise alias for API and CLI compatibility.
func (s *Service) Evaluate(ctx context.Context, version string) (domain.HarnessReport, error) {
	return s.RunHarness(ctx, version)
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
