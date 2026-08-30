package policy

import (
	"context"
	"fmt"
	"hash/fnv"
	"math"
	"sort"
	"strings"
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
	if state, err := repo.LoadPolicyState(ctx); err == nil {
		changed := false
		for version, item := range state.Policies {
			normalized := normalizePolicy(item)
			if item.RetrievalCandidateK == 0 {
				state.Policies[version] = normalized
				changed = true
			}
		}
		if changed {
			if err := repo.SavePolicyState(ctx, state); err != nil {
				return nil, err
			}
		}
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
		RetrievalCandidateK:     12,
		DenseWeight:             0.55,
		SparseWeight:            0.45,
		RRFK:                    60,
		MergeThreshold:          0.5,
		RelevanceThreshold:      0.45,
		RerankEnabled:           true,
		QueryRewriteStrategy:    "step_back",
		MaxWorkflowSteps:        7,
		MaxToolCalls:            8,
		MaxCostUnits:            8,
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
	return normalizePolicy(selected), nil
}

func (m *Manager) State(ctx context.Context) (domain.PolicyState, error) {
	return m.repo.LoadPolicyState(ctx)
}

func (m *Manager) GenerateCandidate(ctx context.Context) (domain.Policy, error) {
	return m.GenerateCandidateFrom(ctx, nil)
}

// GenerateCandidateFrom applies only mutations allowed by Harness failure
// attribution. Safety findings can tighten approval policy but can never relax
// it. When the baseline is clean, feedback drives a small efficiency-only
// candidate so the evolution loop still has a measurable hypothesis to test.
func (m *Manager) GenerateCandidateFrom(ctx context.Context, attributions []domain.FailureAttribution) (domain.Policy, error) {
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
	base = normalizePolicy(base)
	candidate := base
	candidate.ParentVersion = base.Version
	candidate.Version = fmt.Sprintf("v2.0.%d-candidate", time.Now().UTC().UnixNano())
	candidate.CreatedAt = time.Now().UTC()
	candidate.Status = "candidate"
	candidate.Mutations = nil
	var reasons []string
	record := func(field string, before, after any, reason string) {
		if fmt.Sprint(before) == fmt.Sprint(after) {
			return
		}
		candidate.Mutations = append(candidate.Mutations, domain.PolicyMutation{Field: field, Before: before, After: after, Reason: reason})
		reasons = append(reasons, reason)
	}
	categories := map[string]bool{}
	allowedMutations := map[string]bool{}
	for _, item := range attributions {
		categories[item.Category] = true
		for _, field := range item.AllowedMutations {
			allowedMutations[field] = true
		}
	}
	canMutate := func(field string) bool {
		return len(attributions) == 0 || allowedMutations[field]
	}
	if categories["safety"] && canMutate("required_approval_risk") {
		before := candidate.RequiredApprovalRisk
		candidate.RequiredApprovalRisk = domain.RiskLow
		record("required_approval_risk", before, candidate.RequiredApprovalRisk, "Harness safety failure tightened every side-effecting action to human approval")
	}
	if categories["retrieval"] {
		if canMutate("retrieval_candidate_k") {
			beforeK := candidate.RetrievalCandidateK
			candidate.RetrievalCandidateK = min(candidate.RetrievalCandidateK+4, 30)
			record("retrieval_candidate_k", beforeK, candidate.RetrievalCandidateK, "Retrieval attribution expanded the hybrid recall pool")
		}
		if canMutate("query_rewrite_strategy") {
			beforeRewrite := candidate.QueryRewriteStrategy
			candidate.QueryRewriteStrategy = "hyde"
			record("query_rewrite_strategy", beforeRewrite, candidate.QueryRewriteStrategy, "Retrieval attribution enabled a higher-recall rewrite strategy")
		}
	}
	if categories["trajectory"] && canMutate("prompt_revision") {
		before := candidate.PromptRevision
		candidate.PromptRevision = nextRevision(candidate.PromptRevision, "routing")
		record("prompt_revision", before, candidate.PromptRevision, "Trajectory attribution created a new routing/prompt revision")
	}
	if categories["model_quality"] && canMutate("prompt_revision") {
		before := candidate.PromptRevision
		candidate.PromptRevision = nextRevision(candidate.PromptRevision, "grounding")
		record("prompt_revision", before, candidate.PromptRevision, "LLM verifier/judge attribution created a grounded synthesis prompt revision")
	}
	if categories["outcome"] {
		if canMutate("conversion_drop_threshold") {
			beforeConversion := candidate.ConversionDropThreshold
			candidate.ConversionDropThreshold = bounded(candidate.ConversionDropThreshold*.95, 5, 35)
			record("conversion_drop_threshold", beforeConversion, candidate.ConversionDropThreshold, "Outcome misses increased conversion anomaly sensitivity")
		}
		if canMutate("traffic_drop_threshold") {
			beforeTraffic := candidate.TrafficDropThreshold
			candidate.TrafficDropThreshold = bounded(candidate.TrafficDropThreshold*.95, 5, 40)
			record("traffic_drop_threshold", beforeTraffic, candidate.TrafficDropThreshold, "Outcome misses increased traffic anomaly sensitivity")
		}
	}
	if categories["cost"] && canMutate("retrieval_candidate_k") {
		before := candidate.RetrievalCandidateK
		candidate.RetrievalCandidateK = max(candidate.RetrievalTopK, candidate.RetrievalCandidateK-3)
		record("retrieval_candidate_k", before, candidate.RetrievalCandidateK, "Cost attribution reduced the hybrid candidate pool")
	}
	if len(attributions) == 0 {
		switch {
		case notUseful > useful:
			beforeConversion := candidate.ConversionDropThreshold
			candidate.ConversionDropThreshold = bounded(candidate.ConversionDropThreshold*1.10, 5, 35)
			record("conversion_drop_threshold", beforeConversion, candidate.ConversionDropThreshold, "Negative feedback raised anomaly thresholds to reduce false positives")
			beforeTraffic := candidate.TrafficDropThreshold
			candidate.TrafficDropThreshold = bounded(candidate.TrafficDropThreshold*1.10, 5, 40)
			record("traffic_drop_threshold", beforeTraffic, candidate.TrafficDropThreshold, "Negative feedback raised anomaly thresholds to reduce false positives")
		case useful > 0 && positiveOutcomes > 0:
			beforeConversion := candidate.ConversionDropThreshold
			candidate.ConversionDropThreshold = bounded(candidate.ConversionDropThreshold*.95, 5, 35)
			record("conversion_drop_threshold", beforeConversion, candidate.ConversionDropThreshold, "Positive outcome feedback increased diagnostic sensitivity")
			beforeTraffic := candidate.TrafficDropThreshold
			candidate.TrafficDropThreshold = bounded(candidate.TrafficDropThreshold*.95, 5, 40)
			record("traffic_drop_threshold", beforeTraffic, candidate.TrafficDropThreshold, "Positive outcome feedback increased diagnostic sensitivity")
		default:
			before := candidate.RetrievalCandidateK
			candidate.RetrievalCandidateK = max(candidate.RetrievalTopK, candidate.RetrievalCandidateK-2)
			record("retrieval_candidate_k", before, candidate.RetrievalCandidateK, "Clean Harness and sparse feedback proposed a lower-cost retrieval pool")
		}
	}
	if len(reasons) == 0 {
		reasons = append(reasons, "Harness attribution produced no eligible policy mutation; candidate preserves the active policy for gate verification")
	}
	candidate.Rationale = strings.Join(unique(reasons), "; ") + ". Candidate must pass the complete Harness before canary rollout."
	state.Policies[candidate.Version] = candidate
	if err := m.repo.SavePolicyState(ctx, state); err != nil {
		return domain.Policy{}, err
	}
	return candidate, nil
}

func nextRevision(current, suffix string) string {
	if current == "" {
		return "diagnosis-v2-" + suffix
	}
	return current + "+" + suffix
}

func unique(values []string) []string {
	seen := map[string]bool{}
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value != "" && !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	return result
}

func (m *Manager) MarkEvaluated(ctx context.Context, version string, report domain.HarnessReport) error {
	return m.update(ctx, func(state *domain.PolicyState) error {
		item, ok := state.Policies[version]
		if !ok {
			return repository.ErrNotFound
		}
		if !report.Passed || report.PolicyVersion != version {
			return fmt.Errorf("policy %s requires its own passing Harness report", version)
		}
		if report.BaselineVersion != state.ActiveVersion || item.ParentVersion != state.ActiveVersion {
			return fmt.Errorf("policy %s was not evaluated against current active policy %s", version, state.ActiveVersion)
		}
		now := time.Now().UTC()
		item.Status = "evaluated"
		item.EvaluatedAt = &now
		item.EvaluationReportID = report.ID
		item.EvaluatedAgainstVersion = report.BaselineVersion
		item.EvaluatedSuiteVersion = report.SuiteVersion
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
		if item.EvaluationReportID == "" || item.EvaluatedAgainstVersion != state.ActiveVersion {
			return fmt.Errorf("policy %s has a stale or missing Harness release credential", version)
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
		if item.Status != "canary" {
			return fmt.Errorf("policy %s must complete canary assignment before promotion", version)
		}
		if item.EvaluationReportID == "" || item.EvaluatedAgainstVersion != state.ActiveVersion || item.ParentVersion != state.ActiveVersion {
			return fmt.Errorf("policy %s has a stale or missing Harness release credential", version)
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

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func normalizePolicy(item domain.Policy) domain.Policy {
	defaults := DefaultPolicy()
	if item.RetrievalCandidateK == 0 {
		item.RetrievalCandidateK = defaults.RetrievalCandidateK
		item.DenseWeight = defaults.DenseWeight
		item.SparseWeight = defaults.SparseWeight
		item.RRFK = defaults.RRFK
		item.MergeThreshold = defaults.MergeThreshold
		item.RelevanceThreshold = defaults.RelevanceThreshold
		item.RerankEnabled = defaults.RerankEnabled
		item.QueryRewriteStrategy = defaults.QueryRewriteStrategy
		item.MaxWorkflowSteps = defaults.MaxWorkflowSteps
		item.MaxToolCalls = defaults.MaxToolCalls
		item.MaxCostUnits = defaults.MaxCostUnits
	}
	return item
}
