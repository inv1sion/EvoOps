package evolution

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/inv1sion/evoops/internal/domain"
	"github.com/inv1sion/evoops/internal/harness"
	"github.com/inv1sion/evoops/internal/policy"
	"github.com/inv1sion/evoops/internal/repository"
)

type Service struct {
	repo     repository.Repository
	policies *policy.Manager
	harness  *harness.Suite
}

func NewService(repo repository.Repository, policies *policy.Manager, suite *harness.Suite) *Service {
	return &Service{repo: repo, policies: policies, harness: suite}
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
	return s.policies.GenerateCandidateFrom(ctx, baseline.FailureAttributions)
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
	candidate, err := s.policies.GenerateCandidateFrom(ctx, baseline.FailureAttributions)
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
		CanaryPercent: canaryPercent, Status: status, CreatedAt: time.Now().UTC(),
	}
	if err := s.repo.SaveEvolutionRun(ctx, run); err != nil {
		return domain.EvolutionRun{}, err
	}
	return run, nil
}

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
