package policy

import (
	"context"
	"fmt"
	"hash/fnv"
	"math"
	"sort"
	"sync"
	"time"

	"github.com/inv1sion/evoops/internal/domain"
	"github.com/inv1sion/evoops/internal/repository"
)

type Manager struct {
	repo repository.Repository
	mu   sync.Mutex
}

func NewManager(ctx context.Context, repo repository.Repository) (*Manager, error) {
	m := &Manager{repo: repo}
	if _, err := repo.LoadPolicyState(ctx); err == nil {
		return m, nil
	} else if err != repository.ErrNotFound {
		return nil, err
	}
	initial := DefaultPolicy()
	state := domain.PolicyState{
		ActiveVersion: initial.Version,
		Policies:      map[string]domain.Policy{initial.Version: initial},
	}
	if err := repo.SavePolicyState(ctx, state); err != nil {
		return nil, err
	}
	return m, nil
}

func DefaultPolicy() domain.Policy {
	return domain.Policy{
		Version:                 "v1.0.0",
		ConversionDropThreshold: 10,
		TrafficDropThreshold:    15,
		RefundRateThreshold:     0.08,
		CampaignROIThreshold:    1.50,
		StockCoverDaysThreshold: 5,
		RequiredApprovalRisk:    domain.RiskMedium,
		RetrievalTopK:           3,
		PromptRevision:          "diagnosis-v1",
		Status:                  "active",
		CreatedAt:               time.Now().UTC(),
		Rationale:               "Conservative bootstrap policy validated by deterministic replay cases.",
	}
}

func (m *Manager) Select(ctx context.Context, storeID string) (domain.Policy, error) {
	state, err := m.repo.LoadPolicyState(ctx)
	if err != nil {
		return domain.Policy{}, err
	}
	version := state.ActiveVersion
	if state.CanaryVersion != "" && bucket(storeID) < state.CanaryPercent {
		version = state.CanaryVersion
	}
	selected, ok := state.Policies[version]
	if !ok {
		return domain.Policy{}, fmt.Errorf("selected policy %q is missing", version)
	}
	return selected, nil
}

func (m *Manager) State(ctx context.Context) (domain.PolicyState, error) {
	return m.repo.LoadPolicyState(ctx)
}

func (m *Manager) GenerateCandidate(ctx context.Context) (domain.Policy, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	state, err := m.repo.LoadPolicyState(ctx)
	if err != nil {
		return domain.Policy{}, err
	}
	base, ok := state.Policies[state.ActiveVersion]
	if !ok {
		return domain.Policy{}, fmt.Errorf("active policy %q is missing", state.ActiveVersion)
	}
	feedback, err := m.repo.ListFeedback(ctx)
	if err != nil {
		return domain.Policy{}, err
	}
	useful, notUseful, positiveOutcomes := 0, 0, 0
	for _, item := range feedback {
		if item.Useful {
			useful++
		} else {
			notUseful++
		}
		for _, value := range item.ObservedKPIs {
			if value > 0 {
				positiveOutcomes++
			}
		}
	}
	candidate := base
	candidate.ParentVersion = base.Version
	candidate.Version = fmt.Sprintf("v1.0.%d-candidate", time.Now().UTC().UnixNano())
	candidate.CreatedAt = time.Now().UTC()
	candidate.Status = "candidate"
	if notUseful > useful {
		candidate.ConversionDropThreshold = bounded(base.ConversionDropThreshold*1.10, 5, 35)
		candidate.TrafficDropThreshold = bounded(base.TrafficDropThreshold*1.10, 5, 40)
		candidate.CampaignROIThreshold = bounded(base.CampaignROIThreshold*0.95, 0.8, 3)
		candidate.Rationale = "Negative feedback dominated; raise anomaly thresholds to reduce false positives. Replay evaluation is required before rollout."
	} else if useful > 0 && positiveOutcomes > 0 {
		candidate.ConversionDropThreshold = bounded(base.ConversionDropThreshold*0.95, 5, 35)
		candidate.TrafficDropThreshold = bounded(base.TrafficDropThreshold*0.95, 5, 40)
		candidate.Rationale = "Useful diagnoses and positive outcomes dominated; slightly increase sensitivity. Replay evaluation is required before rollout."
	} else {
		candidate.RetrievalTopK = min(base.RetrievalTopK+1, 5)
		candidate.Rationale = "Feedback is sparse; only expand evidence recall while keeping action and approval thresholds unchanged."
	}
	state.Policies[candidate.Version] = candidate
	if err := m.repo.SavePolicyState(ctx, state); err != nil {
		return domain.Policy{}, err
	}
	return candidate, nil
}

func (m *Manager) MarkEvaluated(ctx context.Context, version string) error {
	return m.update(ctx, func(state *domain.PolicyState) error {
		item, ok := state.Policies[version]
		if !ok {
			return repository.ErrNotFound
		}
		item.Status = "evaluated"
		state.Policies[version] = item
		return nil
	})
}

func (m *Manager) StartCanary(ctx context.Context, version string, percent int) error {
	if percent < 1 || percent > 50 {
		return fmt.Errorf("canary percent must be between 1 and 50")
	}
	return m.update(ctx, func(state *domain.PolicyState) error {
		item, ok := state.Policies[version]
		if !ok {
			return repository.ErrNotFound
		}
		if item.Status != "evaluated" && item.Status != "canary" {
			return fmt.Errorf("policy %s must pass evaluation before canary", version)
		}
		item.Status = "canary"
		state.Policies[version] = item
		state.CanaryVersion = version
		state.CanaryPercent = percent
		return nil
	})
}

func (m *Manager) Promote(ctx context.Context, version string) error {
	return m.update(ctx, func(state *domain.PolicyState) error {
		item, ok := state.Policies[version]
		if !ok {
			return repository.ErrNotFound
		}
		if item.Status != "canary" && item.Status != "evaluated" {
			return fmt.Errorf("policy %s is not eligible for promotion", version)
		}
		previous := state.Policies[state.ActiveVersion]
		previous.Status = "retired"
		state.Policies[previous.Version] = previous
		state.PreviousVersion = state.ActiveVersion
		state.ActiveVersion = version
		state.CanaryVersion = ""
		state.CanaryPercent = 0
		item.Status = "active"
		state.Policies[version] = item
		return nil
	})
}

func (m *Manager) Rollback(ctx context.Context) error {
	return m.update(ctx, func(state *domain.PolicyState) error {
		if state.PreviousVersion == "" {
			return fmt.Errorf("no previous policy to roll back to")
		}
		current := state.Policies[state.ActiveVersion]
		current.Status = "rolled_back"
		state.Policies[current.Version] = current
		previous := state.Policies[state.PreviousVersion]
		previous.Status = "active"
		state.Policies[previous.Version] = previous
		state.ActiveVersion, state.PreviousVersion = state.PreviousVersion, state.ActiveVersion
		state.CanaryVersion = ""
		state.CanaryPercent = 0
		return nil
	})
}

func (m *Manager) Versions(ctx context.Context) ([]domain.Policy, error) {
	state, err := m.repo.LoadPolicyState(ctx)
	if err != nil {
		return nil, err
	}
	items := make([]domain.Policy, 0, len(state.Policies))
	for _, item := range state.Policies {
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].CreatedAt.After(items[j].CreatedAt) })
	return items, nil
}

func (m *Manager) update(ctx context.Context, fn func(*domain.PolicyState) error) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	state, err := m.repo.LoadPolicyState(ctx)
	if err != nil {
		return err
	}
	if err := fn(&state); err != nil {
		return err
	}
	return m.repo.SavePolicyState(ctx, state)
}

func bucket(value string) int {
	h := fnv.New32a()
	_, _ = h.Write([]byte(value))
	return int(h.Sum32() % 100)
}

func bounded(value, low, high float64) float64 {
	return math.Round(math.Max(low, math.Min(high, value))*100) / 100
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
