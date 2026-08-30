package agent

import (
	"testing"
	"time"

	"github.com/inv1sion/evoops/internal/domain"
)

func TestMerchantMemoryPersonalizesWithoutChangingSafety(t *testing.T) {
	actions := []domain.Action{
		{ID: "pause", Title: "暂停低效广告计划", Risk: domain.RiskHigh, ExpectedImpact: "停止低效消耗。", Arguments: map[string]any{"action": "pause_campaign", "target": "cmp-1"}},
		{ID: "audit", Title: "发起履约质量审计", Risk: domain.RiskMedium, ExpectedImpact: "减少退款。", Arguments: map[string]any{"action": "open_quality_audit", "target": "recent_orders"}},
	}
	analysis := Analysis{Actions: actions}
	ApplyMerchantMemory(&analysis, domain.MerchantMemoryProfile{StoreID: "store-a", Memories: []domain.MerchantMemory{{
		ID: "mem-avoid", Kind: "action_preference", Operation: "pause_campaign", Target: "cmp-1",
		Polarity: "avoid", Statement: "商家曾拒绝暂停广告。", Confidence: .95, UpdatedAt: time.Now().UTC(),
	}}})
	if analysis.Actions[1].ID != "pause" || analysis.Actions[1].Preference != "deprioritized" {
		t.Fatalf("memory did not deprioritize matching action: %#v", analysis.Actions)
	}
	if analysis.Actions[1].Risk != domain.RiskHigh {
		t.Fatalf("memory changed risk: %s", analysis.Actions[1].Risk)
	}
	if len(analysis.Actions[1].MemoryRefs) != 1 || len(analysis.Evidence) != 1 || analysis.Evidence[0].Source != "merchant_memory" {
		t.Fatalf("memory lineage is missing: actions=%#v evidence=%#v", analysis.Actions, analysis.Evidence)
	}
}
