package agent

import (
	"strings"
	"testing"
)

func TestGroundingPromptRevisionChangesModelInstructions(t *testing.T) {
	base := synthesisSystemPrompt("ad-roi-v1")
	grounded := synthesisSystemPrompt("ad-roi-v1+grounding")
	if base == grounded {
		t.Fatal("grounding prompt revision must change the instructions used by the model")
	}
	if !strings.Contains(grounded, "逐句核验") || !strings.Contains(grounded, "evidence") {
		t.Fatalf("grounding revision is missing evidence verification: %q", grounded)
	}
}
