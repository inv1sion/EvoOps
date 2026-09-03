package agent

import (
	"fmt"
	"sort"

	"github.com/google/uuid"
	"github.com/inv1sion/evoops/internal/domain"
	"github.com/inv1sion/evoops/internal/tools"
)

type AnalysisContext struct {
	StoreID   string
	Campaigns []domain.Campaign
	Knowledge domain.RetrievalResult
	Memory    domain.MerchantMemoryProfile
}

type Analysis struct {
	ExecutionNote  string
	Summary        string
	Signals        []domain.Signal
	Evidence       []domain.Evidence
	Actions        []domain.Action
	PromptRevision string
	Prompt         domain.PromptArtifact
}

func Analyze(context AnalysisContext, policy domain.Policy) Analysis {
	analysis := Analysis{PromptRevision: policy.PromptRevision, Prompt: policy.Prompt}
	activeCampaigns := 0
	for _, campaign := range context.Campaigns {
		if campaign.Status != "active" {
			continue
		}
		activeCampaigns++
		id := "campaign:" + campaign.ID
		analysis.Evidence = append(analysis.Evidence, domain.Evidence{
			ID: id, Source: tools.ToolCampaigns, Ref: campaign.ID,
			Excerpt: fmt.Sprintf("广告计划“%s”当前 ROI %.2f，前期 ROI %.2f，策略阈值 %.2f，消耗 %.0f，广告成交额 %.0f，日预算 %.0f。", campaign.Name, campaign.ROI, campaign.PrevROI, policy.CampaignROIThreshold, campaign.Spend, campaign.Revenue, campaign.Budget),
		})
		if campaign.ROI >= policy.CampaignROIThreshold {
			continue
		}
		analysis.Signals = append(analysis.Signals, domain.Signal{
			Name: "campaign_roi_low", Severity: "high",
			Observation:  fmt.Sprintf("广告计划“%s”的 ROI 为 %.2f，低于策略阈值 %.2f。", campaign.Name, campaign.ROI, policy.CampaignROIThreshold),
			DeltaPercent: percentDelta(campaign.ROI, campaign.PrevROI), EvidenceIDs: []string{id},
		})
		analysis.Actions = append(analysis.Actions,
			action(context.StoreID, "创建广告归因复核任务", "create_campaign_diagnostic_task", campaign.ID, domain.RiskLow, "先核验转化归因、受众和素材效率，再决定是否调整投放。"),
			action(context.StoreID, "暂停低 ROI 广告计划", "pause_campaign", campaign.ID, domain.RiskHigh, "停止继续消耗；该操作必须人工审批并保留审计记录。"),
		)
	}

	analysis.Summary = summarizeCampaignROI(activeCampaigns, analysis.Signals, policy.CampaignROIThreshold)
	return analysis
}

func AddKnowledgeEvidence(analysis *Analysis, result domain.RetrievalResult) {
	for _, hit := range result.Hits {
		analysis.Evidence = append(analysis.Evidence, domain.Evidence{
			ID: "kb:" + hit.Chunk.ID, Source: tools.ToolKnowledge, Ref: hit.Chunk.Title, Excerpt: hit.Chunk.Text,
		})
	}
}

// ApplyMerchantMemory personalizes presentation and ordering only. Signal
// detection, tool arguments, risk and approval policy remain unchanged.
func ApplyMerchantMemory(analysis *Analysis, profile domain.MerchantMemoryProfile) {
	if analysis == nil || len(profile.Memories) == 0 {
		return
	}
	used := make(map[string]bool)
	for i := range analysis.Actions {
		action := &analysis.Actions[i]
		operation := actionArgument(*action, "action")
		target := actionArgument(*action, "target")
		selected, ok := selectActionMemory(profile.Memories, operation, target)
		if !ok {
			continue
		}
		action.MemoryRefs = []string{selected.ID}
		switch selected.Polarity {
		case "prefer", "success":
			action.Preference = "preferred"
			action.ExpectedImpact += " 结合该商家的历史正向反馈，本次优先展示此方案。"
		case "avoid", "failure":
			action.Preference = "deprioritized"
			action.ExpectedImpact += " 该商家曾拒绝该类操作或反馈效果不佳，本次降低展示优先级并保留再次确认。"
		default:
			continue
		}
		if !used[selected.ID] {
			analysis.Evidence = append(analysis.Evidence, domain.Evidence{
				ID: "memory:" + selected.ID, Source: "merchant_memory", Ref: selected.Statement,
				Excerpt: fmt.Sprintf("来源 %s；置信度 %.2f；关联运行 %s。", selected.Source, selected.Confidence, selected.SourceRunID),
			})
			used[selected.ID] = true
		}
	}
	sort.SliceStable(analysis.Actions, func(i, j int) bool {
		return preferenceRank(analysis.Actions[i].Preference) > preferenceRank(analysis.Actions[j].Preference)
	})
}

func selectActionMemory(memories []domain.MerchantMemory, operation, target string) (domain.MerchantMemory, bool) {
	var candidates []domain.MerchantMemory
	for _, item := range memories {
		if item.Operation != operation || (item.Kind != "action_preference" && item.Kind != "action_outcome") {
			continue
		}
		if item.Target != "" && target != "" && item.Target != target {
			continue
		}
		candidates = append(candidates, item)
	}
	if len(candidates) == 0 {
		return domain.MerchantMemory{}, false
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		leftExact := candidates[i].Target != "" && candidates[i].Target == target
		rightExact := candidates[j].Target != "" && candidates[j].Target == target
		if leftExact != rightExact {
			return leftExact
		}
		leftExplicit := candidates[i].Kind == "action_preference"
		rightExplicit := candidates[j].Kind == "action_preference"
		if leftExplicit != rightExplicit {
			return leftExplicit
		}
		if !candidates[i].UpdatedAt.Equal(candidates[j].UpdatedAt) {
			return candidates[i].UpdatedAt.After(candidates[j].UpdatedAt)
		}
		return candidates[i].Confidence > candidates[j].Confidence
	})
	return candidates[0], true
}

func actionArgument(action domain.Action, key string) string {
	value, _ := action.Arguments[key].(string)
	return value
}

func preferenceRank(preference string) int {
	switch preference {
	case "preferred":
		return 2
	case "deprioritized":
		return 0
	default:
		return 1
	}
}

func signalQuery(signals []domain.Signal) string {
	if len(signals) == 0 {
		return "advertising ROI health 广告投产健康"
	}
	return "campaign ROI low advertising optimization 广告低投产诊断"
}

func action(storeID, title, operation, target string, risk domain.RiskLevel, impact string) domain.Action {
	return domain.Action{
		ID: uuid.NewString(), Title: title, Tool: tools.ToolExecute, Risk: risk,
		ExpectedImpact: impact, Status: "proposed",
		Arguments: map[string]any{"store_id": storeID, "action": operation, "target": target},
	}
}

func summarizeCampaignROI(activeCampaigns int, signals []domain.Signal, threshold float64) string {
	if len(signals) == 0 {
		return fmt.Sprintf("已检查 %d 个投放中的广告计划，没有计划低于 ROI 阈值 %.2f。", activeCampaigns, threshold)
	}
	return fmt.Sprintf("已检查 %d 个投放中的广告计划，发现 %d 个低于 ROI 阈值 %.2f。系统先创建归因复核任务；暂停广告必须人工审批。", activeCampaigns, len(signals), threshold)
}

func percentDelta(current, previous float64) float64 {
	if previous == 0 {
		return 0
	}
	return (current - previous) / previous * 100
}

func RequiresApproval(risk, minimum domain.RiskLevel) bool {
	return riskRank(risk) >= riskRank(minimum)
}

func riskRank(value domain.RiskLevel) int {
	switch value {
	case domain.RiskHigh:
		return 3
	case domain.RiskMedium:
		return 2
	default:
		return 1
	}
}
