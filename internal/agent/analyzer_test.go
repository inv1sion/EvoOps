package agent

import (
	"testing"

	"github.com/inv1sion/evoops/internal/domain"
	"github.com/inv1sion/evoops/internal/policy"
)

func TestAnalyzeDetectsLowROIAndKeepsPauseGuarded(t *testing.T) {
	context := AnalysisContext{
		StoreID:   "store-1",
		Campaigns: []domain.Campaign{{ID: "cmp-1", Name: "search", Status: "active", ROI: 1.1, PrevROI: 2.0}},
	}
	result := Analyze(context, policy.DefaultPolicy())
	if len(result.Signals) != 1 || result.Signals[0].Name != "campaign_roi_low" {
		t.Fatalf("unexpected signals: %#v", result.Signals)
	}
	if len(result.Actions) != 2 {
		t.Fatalf("actions=%d, want diagnostic task and guarded pause", len(result.Actions))
	}
	for _, action := range result.Actions {
		if action.Risk != domain.RiskLow && !RequiresApproval(action.Risk, policy.DefaultPolicy().RequiredApprovalRisk) {
			t.Fatalf("action %q escaped the approval policy", action.Title)
		}
	}
}

func TestAnalyzeHealthyStoreHasNoSignals(t *testing.T) {
	result := Analyze(AnalysisContext{
		StoreID:   "healthy",
		Campaigns: []domain.Campaign{{ID: "safe", Status: "active", ROI: 2, PrevROI: 2}},
	}, policy.DefaultPolicy())
	if len(result.Signals) != 0 || len(result.Actions) != 0 {
		t.Fatalf("healthy store produced signals=%v actions=%v", result.Signals, result.Actions)
	}
}

func TestAnalyzeIgnoresPausedLowROICampaign(t *testing.T) {
	result := Analyze(AnalysisContext{
		StoreID:   "paused",
		Campaigns: []domain.Campaign{{ID: "paused", Status: "paused", ROI: .8, PrevROI: 1.2}},
	}, policy.DefaultPolicy())
	if len(result.Signals) != 0 || len(result.Actions) != 0 {
		t.Fatalf("paused campaign produced signals=%v actions=%v", result.Signals, result.Actions)
	}
}
