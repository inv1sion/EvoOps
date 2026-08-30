package memory

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/inv1sion/evoops/internal/domain"
	"github.com/inv1sion/evoops/internal/repository"
)

const (
	KindActionPreference = "action_preference"
	KindActionOutcome    = "action_outcome"
	KindDiagnosisEpisode = "diagnosis_episode"

	PolarityPrefer    = "prefer"
	PolarityAvoid     = "avoid"
	PolaritySuccess   = "success"
	PolarityFailure   = "failure"
	PolarityUseful    = "useful"
	PolarityNotUseful = "not_useful"

	SourceExplicitFeedback = "explicit_feedback"
	SourceObservedKPI      = "observed_kpi"
	maxMemoriesPerStore    = 200
)

type Service struct {
	repo repository.Repository
	mu   sync.Mutex
}

func NewService(repo repository.Repository) *Service {
	return &Service{repo: repo}
}

func (s *Service) Get(ctx context.Context, storeID string) (domain.MerchantMemoryProfile, error) {
	profile, err := s.repo.LoadMerchantMemory(ctx, storeID)
	if errors.Is(err, repository.ErrNotFound) {
		return domain.MerchantMemoryProfile{StoreID: storeID, Memories: []domain.MerchantMemory{}}, nil
	}
	return profile, err
}

func (s *Service) Validate(run *domain.Run, feedback domain.Feedback) error {
	_, _, err := validatedActions(run, feedback)
	return err
}

// Learn converts explicit feedback into typed memory facts. Action IDs must
// resolve to the referenced run, preventing clients from injecting arbitrary
// operations into a merchant profile.
func (s *Service) Learn(ctx context.Context, run *domain.Run, feedback domain.Feedback) ([]domain.MerchantMemory, error) {
	accepted, rejected, err := validatedActions(run, feedback)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	now := feedback.CreatedAt
	if now.IsZero() {
		now = time.Now().UTC()
	}
	updates := []domain.MerchantMemory{episodeMemory(run, feedback, now)}
	for _, id := range feedback.AcceptedActions {
		updates = append(updates, preferenceMemory(run, feedback, accepted[id], PolarityPrefer, now))
	}
	for _, id := range feedback.RejectedActions {
		updates = append(updates, preferenceMemory(run, feedback, rejected[id], PolarityAvoid, now))
	}
	if len(feedback.ObservedKPIs) > 0 {
		for _, id := range feedback.AcceptedActions {
			updates = append(updates, outcomeMemory(run, feedback, accepted[id], now))
		}
	}

	profile, err := s.Get(ctx, feedback.StoreID)
	if err != nil {
		return nil, err
	}
	byID := make(map[string]int, len(profile.Memories))
	for i, item := range profile.Memories {
		byID[item.ID] = i
	}
	for _, update := range updates {
		if index, ok := byID[update.ID]; ok {
			profile.Memories[index] = update
			continue
		}
		byID[update.ID] = len(profile.Memories)
		profile.Memories = append(profile.Memories, update)
	}
	sort.SliceStable(profile.Memories, func(i, j int) bool {
		return profile.Memories[i].UpdatedAt.After(profile.Memories[j].UpdatedAt)
	})
	if len(profile.Memories) > maxMemoriesPerStore {
		profile.Memories = profile.Memories[:maxMemoriesPerStore]
	}
	profile.UpdatedAt = now
	if err := s.repo.SaveMerchantMemory(ctx, profile); err != nil {
		return nil, err
	}
	return updates, nil
}

func validatedActions(run *domain.Run, feedback domain.Feedback) (map[string]domain.Action, map[string]domain.Action, error) {
	if run == nil || run.Result == nil {
		return nil, nil, fmt.Errorf("feedback requires a completed diagnosis result")
	}
	if feedback.StoreID == "" || feedback.StoreID != run.Request.StoreID {
		return nil, nil, fmt.Errorf("feedback store does not match the referenced run")
	}
	actions := make(map[string]domain.Action, len(run.Result.Actions))
	for _, action := range run.Result.Actions {
		actions[action.ID] = action
	}
	accepted, err := resolveActions(feedback.AcceptedActions, actions)
	if err != nil {
		return nil, nil, err
	}
	rejected, err := resolveActions(feedback.RejectedActions, actions)
	if err != nil {
		return nil, nil, err
	}
	for id := range accepted {
		if _, conflict := rejected[id]; conflict {
			return nil, nil, fmt.Errorf("action %q cannot be both accepted and rejected", id)
		}
	}
	return accepted, rejected, nil
}

func resolveActions(ids []string, available map[string]domain.Action) (map[string]domain.Action, error) {
	result := make(map[string]domain.Action, len(ids))
	for _, id := range ids {
		action, ok := available[id]
		if !ok {
			return nil, fmt.Errorf("action %q does not belong to the referenced run", id)
		}
		result[id] = action
	}
	return result, nil
}

func episodeMemory(run *domain.Run, feedback domain.Feedback, now time.Time) domain.MerchantMemory {
	polarity := PolarityNotUseful
	statement := "商家将本次经营诊断标记为无用。"
	if feedback.Useful {
		polarity = PolarityUseful
		statement = "商家将本次经营诊断标记为有用。"
	}
	if comment := strings.TrimSpace(feedback.Comment); comment != "" {
		statement += " 反馈：" + comment
	}
	return baseMemory(run, feedback, "episode", KindDiagnosisEpisode, "", "", polarity, statement, .85, SourceExplicitFeedback, now)
}

func preferenceMemory(run *domain.Run, feedback domain.Feedback, action domain.Action, polarity string, now time.Time) domain.MerchantMemory {
	verb := "接受"
	if polarity == PolarityAvoid {
		verb = "拒绝"
	}
	statement := fmt.Sprintf("商家曾%s行动“%s”。", verb, action.Title)
	if polarity == PolarityAvoid && strings.TrimSpace(feedback.Comment) != "" {
		statement += " 原因：" + strings.TrimSpace(feedback.Comment)
	}
	return baseMemory(run, feedback, "preference:"+action.ID+":"+polarity, KindActionPreference,
		argument(action, "action"), argument(action, "target"), polarity, statement, .95, SourceExplicitFeedback, now)
}

func outcomeMemory(run *domain.Run, feedback domain.Feedback, action domain.Action, now time.Time) domain.MerchantMemory {
	keys := make([]string, 0, len(feedback.ObservedKPIs))
	sum := 0.0
	for key, value := range feedback.ObservedKPIs {
		keys = append(keys, key)
		sum += value
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, fmt.Sprintf("%s=%+.3f", key, feedback.ObservedKPIs[key]))
	}
	polarity := PolaritySuccess
	if sum < 0 {
		polarity = PolarityFailure
	}
	statement := fmt.Sprintf("执行行动“%s”后的标准化经营改善量：%s（正值表示改善）。", action.Title, strings.Join(parts, "，"))
	return baseMemory(run, feedback, "outcome:"+action.ID, KindActionOutcome,
		argument(action, "action"), argument(action, "target"), polarity, statement, .9, SourceObservedKPI, now)
}

func baseMemory(run *domain.Run, feedback domain.Feedback, discriminator, kind, operation, target, polarity, statement string, confidence float64, source string, now time.Time) domain.MerchantMemory {
	digest := sha256.Sum256([]byte(feedback.ID + "\x00" + discriminator))
	return domain.MerchantMemory{
		ID: fmt.Sprintf("mem-%x", digest[:12]), StoreID: feedback.StoreID, Kind: kind,
		Operation: operation, Target: target, Polarity: polarity, Statement: statement,
		Confidence: confidence, Source: source, SourceRunID: run.ID, SourceFeedbackID: feedback.ID,
		EvidenceRefs: []string{"run:" + run.ID, "feedback:" + feedback.ID}, CreatedAt: now, UpdatedAt: now,
	}
}

func argument(action domain.Action, key string) string {
	value, _ := action.Arguments[key].(string)
	return value
}
