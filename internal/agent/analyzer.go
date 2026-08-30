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
	Knowledge []domain.KnowledgeArticle
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
		Ref:    context.Metrics.Current.Label + " vs " + context.Metrics.Previous.Label,
		Excerpt: fmt.Sprintf("Revenue %.0f vs %.0f; visitors %d vs %d; conversion %.2f%% vs %.2f%%; refund %.2f%% vs %.2f%%.",
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
			Observation:  fmt.Sprintf("Conversion rate fell %.1f%% relative to baseline.", -conversionDelta),
			DeltaPercent: conversionDelta, EvidenceIDs: []string{metricEvidence.ID},
		})
		analysis.Actions = append(analysis.Actions, action(context.StoreID, "Create conversion-funnel investigation", "create_diagnostic_task", "conversion_funnel", domain.RiskLow, "Identify the highest-loss funnel step before changing traffic allocation."))
	}

	trafficDelta := percentDelta(float64(context.Metrics.Current.Visitors), float64(context.Metrics.Previous.Visitors))
	if trafficDelta <= -policy.TrafficDropThreshold {
		analysis.Signals = append(analysis.Signals, domain.Signal{
			Name: "traffic_drop", Severity: severity(-trafficDelta, policy.TrafficDropThreshold),
			Observation:  fmt.Sprintf("Visitors fell %.1f%% relative to baseline.", -trafficDelta),
			DeltaPercent: trafficDelta, EvidenceIDs: []string{metricEvidence.ID},
		})
		analysis.Actions = append(analysis.Actions, action(context.StoreID, "Create traffic-source audit", "create_diagnostic_task", "traffic_sources", domain.RiskLow, "Separate organic, paid, and returning-user traffic loss."))
	}

	if context.Metrics.Current.RefundRate >= policy.RefundRateThreshold {
		analysis.Signals = append(analysis.Signals, domain.Signal{
			Name: "refund_rate_spike", Severity: "high",
			Observation: fmt.Sprintf("Refund rate is %.2f%%, above the %.2f%% guardrail.", context.Metrics.Current.RefundRate*100, policy.RefundRateThreshold*100),
			EvidenceIDs: []string{metricEvidence.ID},
		})
		analysis.Actions = append(analysis.Actions, action(context.StoreID, "Open fulfillment quality audit", "open_quality_audit", "recent_orders", domain.RiskMedium, "Reduce avoidable refunds without masking demand signals."))
	}

	for _, campaign := range context.Campaigns {
		if campaign.Status != "active" || campaign.ROI >= policy.CampaignROIThreshold {
			continue
		}
		id := "campaign:" + campaign.ID
		analysis.Evidence = append(analysis.Evidence, domain.Evidence{
			ID: id, Source: tools.ToolCampaigns, Ref: campaign.ID,
			Excerpt: fmt.Sprintf("%s ROI %.2f (previous %.2f), spend %.0f, revenue %.0f.", campaign.Name, campaign.ROI, campaign.PrevROI, campaign.Spend, campaign.Revenue),
		})
		analysis.Signals = append(analysis.Signals, domain.Signal{
			Name: "campaign_roi_low", Severity: "high",
			Observation:  fmt.Sprintf("Campaign %s ROI %.2f is below %.2f.", campaign.Name, campaign.ROI, policy.CampaignROIThreshold),
			DeltaPercent: percentDelta(campaign.ROI, campaign.PrevROI), EvidenceIDs: []string{id},
		})
		analysis.Actions = append(analysis.Actions, action(context.StoreID, "Pause inefficient campaign", "pause_campaign", campaign.ID, domain.RiskHigh, "Stop inefficient spend while retaining a reversible audit trail."))
	}

	for _, item := range context.Inventory {
		if item.EstimatedDays >= policy.StockCoverDaysThreshold && item.StockoutHours == 0 {
			continue
		}
		id := "inventory:" + item.SKU
		analysis.Evidence = append(analysis.Evidence, domain.Evidence{
			ID: id, Source: tools.ToolInventory, Ref: item.SKU,
			Excerpt: fmt.Sprintf("%s has %d units, %.1f cover days, %d stockout hours, %.1f%% revenue contribution.", item.Name, item.Available, item.EstimatedDays, item.StockoutHours, item.ContributionRate*100),
		})
		analysis.Signals = append(analysis.Signals, domain.Signal{
			Name: "stockout_risk", Severity: severity(policy.StockCoverDaysThreshold-item.EstimatedDays, policy.StockCoverDaysThreshold/2),
			Observation: fmt.Sprintf("SKU %s has only %.1f days of cover.", item.SKU, item.EstimatedDays),
			EvidenceIDs: []string{id},
		})
		analysis.Actions = append(analysis.Actions, action(context.StoreID, "Create replenishment order", "create_restock_order", item.SKU, domain.RiskHigh, "Protect revenue from imminent stockout."))
	}

	sort.SliceStable(analysis.Signals, func(i, j int) bool { return rank(analysis.Signals[i].Severity) > rank(analysis.Signals[j].Severity) })
	analysis.Summary = summarize(analysis.Signals)
	return analysis
}

func AddKnowledgeEvidence(analysis *Analysis, articles []domain.KnowledgeArticle) {
	for _, article := range articles {
		analysis.Evidence = append(analysis.Evidence, domain.Evidence{
			ID: "kb:" + article.ID, Source: tools.ToolKnowledge, Ref: article.Title, Excerpt: article.Content,
		})
	}
}

func signalQuery(signals []domain.Signal) string {
	parts := make([]string, 0, len(signals))
	for _, signal := range signals {
		parts = append(parts, strings.ReplaceAll(signal.Name, "_", " "))
	}
	if len(parts) == 0 {
		return "business health baseline"
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
		return "No material anomaly crossed the active policy thresholds. Continue monitoring the baseline."
	}
	names := make([]string, 0, len(signals))
	for _, signal := range signals {
		names = append(names, strings.ReplaceAll(signal.Name, "_", " "))
	}
	return fmt.Sprintf("Detected %d material operating signals: %s. Recommendations are evidence-linked and risky operations require approval.", len(signals), strings.Join(names, ", "))
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
