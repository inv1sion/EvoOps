package agent

import (
	"testing"

	"github.com/inv1sion/evoops/internal/domain"
	"github.com/inv1sion/evoops/internal/policy"
)

func TestAnalyzeDetectsSignalsAndKeepsRiskyActionsGuarded(t *testing.T) {
	context := AnalysisContext{
		StoreID: "store-1",
		Metrics: domain.MetricsSnapshot{
			Current:  domain.MetricPeriod{Visitors: 700, ConversionRate: .02, RefundRate: .10},
			Previous: domain.MetricPeriod{Visitors: 1000, ConversionRate: .03, RefundRate: .03},
		},
		Inventory: []domain.InventoryItem{{SKU: "sku-1", EstimatedDays: 2}},
		Campaigns: []domain.Campaign{{ID: "cmp-1", Name: "search", Status: "active", ROI: 1.1, PrevROI: 2.0}},
	}
	result := Analyze(context, policy.DefaultPolicy())
	want := map[string]bool{"conversion_drop": true, "traffic_drop": true, "refund_rate_spike": true, "campaign_roi_low": true, "stockout_risk": true}
	for _, signal := range result.Signals {
		delete(want, signal.Name)
	}
	if len(want) != 0 {
		t.Fatalf("missing signals: %v", want)
	}
	for _, action := range result.Actions {
		if action.Risk != domain.RiskLow && !RequiresApproval(action.Risk, policy.DefaultPolicy().RequiredApprovalRisk) {
			t.Fatalf("action %q escaped the approval policy", action.Title)
		}
	}
}

func TestAnalyzeHealthyStoreHasNoSignals(t *testing.T) {
	result := Analyze(AnalysisContext{
		StoreID: "healthy",
		Metrics: domain.MetricsSnapshot{
			Current:  domain.MetricPeriod{Visitors: 1010, ConversionRate: .031, RefundRate: .02},
			Previous: domain.MetricPeriod{Visitors: 1000, ConversionRate: .03, RefundRate: .025},
		},
		Inventory: []domain.InventoryItem{{SKU: "safe", EstimatedDays: 20}},
		Campaigns: []domain.Campaign{{ID: "safe", Status: "active", ROI: 2, PrevROI: 2}},
	}, policy.DefaultPolicy())
	if len(result.Signals) != 0 || len(result.Actions) != 0 {
		t.Fatalf("healthy store produced signals=%v actions=%v", result.Signals, result.Actions)
	}
}
