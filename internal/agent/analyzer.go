package agent

import (
	"fmt"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/inv1sion/evoops/internal/domain"
	"github.com/inv1sion/evoops/internal/tools"
)

type AnalysisContext struct {
	StoreID   string
	Metrics   domain.MetricsSnapshot
	Inventory []domain.InventoryItem
	Campaigns []domain.Campaign
	Knowledge domain.RetrievalResult
	Memory    domain.MerchantMemoryProfile
}

type Analysis struct {
	Summary  string
	Signals  []domain.Signal
	Evidence []domain.Evidence
	Actions  []domain.Action
}

func Analyze(context AnalysisContext, policy domain.Policy) Analysis {
	analysis := Analysis{}
	metricEvidence := domain.Evidence{
		ID:     "metrics:period-comparison",
		Source: tools.ToolMetrics,
		Ref:    context.Metrics.Current.Label + " 对比 " + context.Metrics.Previous.Label,
		Excerpt: fmt.Sprintf("成交额 %.0f 对比 %.0f；访客数 %d 对比 %d；转化率 %.2f%% 对比 %.2f%%；退款率 %.2f%% 对比 %.2f%%。",
			context.Metrics.Current.Revenue, context.Metrics.Previous.Revenue,
			context.Metrics.Current.Visitors, context.Metrics.Previous.Visitors,
			context.Metrics.Current.ConversionRate*100, context.Metrics.Previous.ConversionRate*100,
			context.Metrics.Current.RefundRate*100, context.Metrics.Previous.RefundRate*100),
	}
	analysis.Evidence = append(analysis.Evidence, metricEvidence)

	conversionDelta := percentDelta(context.Metrics.Current.ConversionRate, context.Metrics.Previous.ConversionRate)
	if conversionDelta <= -policy.ConversionDropThreshold {
		analysis.Signals = append(analysis.Signals, domain.Signal{
			Name: "conversion_drop", Severity: severity(-conversionDelta, policy.ConversionDropThreshold),
			Observation:  fmt.Sprintf("转化率相对基线下降了 %.1f%%。", -conversionDelta),
			DeltaPercent: conversionDelta, EvidenceIDs: []string{metricEvidence.ID},
		})
		analysis.Actions = append(analysis.Actions, action(context.StoreID, "创建转化漏斗排查任务", "create_diagnostic_task", "conversion_funnel", domain.RiskLow, "调整流量分配前，先定位损失最大的漏斗环节。"))
	}

	trafficDelta := percentDelta(float64(context.Metrics.Current.Visitors), float64(context.Metrics.Previous.Visitors))
	if trafficDelta <= -policy.TrafficDropThreshold {
		analysis.Signals = append(analysis.Signals, domain.Signal{
			Name: "traffic_drop", Severity: severity(-trafficDelta, policy.TrafficDropThreshold),
			Observation:  fmt.Sprintf("访客数相对基线下降了 %.1f%%。", -trafficDelta),
			DeltaPercent: trafficDelta, EvidenceIDs: []string{metricEvidence.ID},
		})
		analysis.Actions = append(analysis.Actions, action(context.StoreID, "创建流量来源审计任务", "create_diagnostic_task", "traffic_sources", domain.RiskLow, "区分自然、付费和回访用户的流量损失。"))
	}

	if context.Metrics.Current.RefundRate >= policy.RefundRateThreshold {
		analysis.Signals = append(analysis.Signals, domain.Signal{
			Name: "refund_rate_spike", Severity: "high",
			Observation: fmt.Sprintf("退款率为 %.2f%%，超过 %.2f%% 的安全阈值。", context.Metrics.Current.RefundRate*100, policy.RefundRateThreshold*100),
			EvidenceIDs: []string{metricEvidence.ID},
		})
		analysis.Actions = append(analysis.Actions, action(context.StoreID, "发起履约质量审计", "open_quality_audit", "recent_orders", domain.RiskMedium, "在不掩盖需求信号的前提下减少可避免的退款。"))
	}

	for _, campaign := range context.Campaigns {
		if campaign.Status != "active" || campaign.ROI >= policy.CampaignROIThreshold {
			continue
		}
		id := "campaign:" + campaign.ID
		analysis.Evidence = append(analysis.Evidence, domain.Evidence{
			ID: id, Source: tools.ToolCampaigns, Ref: campaign.ID,
			Excerpt: fmt.Sprintf("%s 的 ROI 为 %.2f（前期 %.2f），消耗 %.0f，成交额 %.0f。", campaign.Name, campaign.ROI, campaign.PrevROI, campaign.Spend, campaign.Revenue),
		})
		analysis.Signals = append(analysis.Signals, domain.Signal{
			Name: "campaign_roi_low", Severity: "high",
			Observation:  fmt.Sprintf("广告计划“%s”的 ROI 为 %.2f，低于 %.2f。", campaign.Name, campaign.ROI, policy.CampaignROIThreshold),
			DeltaPercent: percentDelta(campaign.ROI, campaign.PrevROI), EvidenceIDs: []string{id},
		})
		analysis.Actions = append(analysis.Actions, action(context.StoreID, "暂停低效广告计划", "pause_campaign", campaign.ID, domain.RiskHigh, "停止低效消耗，同时保留可回滚的审计记录。"))
	}

	for _, item := range context.Inventory {
		if item.EstimatedDays >= policy.StockCoverDaysThreshold && item.StockoutHours == 0 {
			continue
		}
		id := "inventory:" + item.SKU
		analysis.Evidence = append(analysis.Evidence, domain.Evidence{
			ID: id, Source: tools.ToolInventory, Ref: item.SKU,
			Excerpt: fmt.Sprintf("%s 当前库存 %d 件，可售 %.1f 天，缺货 %d 小时，贡献 %.1f%% 的成交额。", item.Name, item.Available, item.EstimatedDays, item.StockoutHours, item.ContributionRate*100),
		})
		analysis.Signals = append(analysis.Signals, domain.Signal{
			Name: "stockout_risk", Severity: severity(policy.StockCoverDaysThreshold-item.EstimatedDays, policy.StockCoverDaysThreshold/2),
			Observation: fmt.Sprintf("商品 %s 仅剩 %.1f 天可售库存。", item.SKU, item.EstimatedDays),
			EvidenceIDs: []string{id},
		})
		analysis.Actions = append(analysis.Actions, action(context.StoreID, "创建补货单", "create_restock_order", item.SKU, domain.RiskHigh, "降低临近缺货造成的成交损失。"))
	}

	sort.SliceStable(analysis.Signals, func(i, j int) bool { return rank(analysis.Signals[i].Severity) > rank(analysis.Signals[j].Severity) })
	analysis.Summary = summarize(analysis.Signals)
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
	const maxSignalsPerRetrieval = 3
	limit := len(signals)
	if limit > maxSignalsPerRetrieval {
		limit = maxSignalsPerRetrieval
	}
	parts := make([]string, 0, limit)
	for _, signal := range signals[:limit] {
		parts = append(parts, strings.ReplaceAll(signal.Name, "_", " "))
	}
	if len(parts) == 0 {
		return "business health baseline 经营健康基线"
	}
	return strings.Join(parts, " ")
}

func action(storeID, title, operation, target string, risk domain.RiskLevel, impact string) domain.Action {
	return domain.Action{
		ID: uuid.NewString(), Title: title, Tool: tools.ToolExecute, Risk: risk,
		ExpectedImpact: impact, Status: "proposed",
		Arguments: map[string]any{"store_id": storeID, "action": operation, "target": target},
	}
}

func summarize(signals []domain.Signal) string {
	if len(signals) == 0 {
		return "当前没有超过策略阈值的显著经营异常，建议继续观察基线变化。"
	}
	names := make([]string, 0, len(signals))
	for _, signal := range signals {
		names = append(names, signalLabel(signal.Name))
	}
	return fmt.Sprintf("检测到 %d 个显著经营信号：%s。所有建议均关联证据，高风险操作必须经过人工审批。", len(signals), strings.Join(names, "、"))
}

func signalLabel(name string) string {
	switch name {
	case "conversion_drop":
		return "转化率下降"
	case "traffic_drop":
		return "流量下降"
	case "refund_rate_spike":
		return "退款率升高"
	case "campaign_roi_low":
		return "广告投产偏低"
	case "stockout_risk":
		return "缺货风险"
	default:
		return strings.ReplaceAll(name, "_", " ")
	}
}

func percentDelta(current, previous float64) float64 {
	if previous == 0 {
		return 0
	}
	return (current - previous) / previous * 100
}

func severity(value, threshold float64) string {
	if threshold > 0 && value >= threshold*1.8 {
		return "high"
	}
	return "medium"
}

func rank(value string) int {
	switch value {
	case "high":
		return 3
	case "medium":
		return 2
	default:
		return 1
	}
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
