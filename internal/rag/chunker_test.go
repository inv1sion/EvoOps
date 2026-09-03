package rag

import (
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestMedQAThreeLevelSlidingWindows(t *testing.T) {
	doc := Document{ID: uuid.NewString(), StoreID: "store-a", Scope: ScopeStore, Title: "投放手册", Version: 3}
	content := strings.Repeat("投", 1500)
	tree, err := ChunkDocument(doc, content, MedQAChunkingConfig())
	if err != nil {
		t.Fatal(err)
	}
	if len(tree.L1) != 2 || len(tree.L2) != 4 || len(tree.L3) != 9 {
		t.Fatalf("unexpected counts L1=%d L2=%d L3=%d", len(tree.L1), len(tree.L2), len(tree.L3))
	}
	if got := len([]rune(tree.L1[0].Content)); got != 1200 {
		t.Fatalf("L1 length=%d", got)
	}
	if tree.L1[1].StartChar != 960 {
		t.Fatalf("L1 overlap is wrong: start=%d", tree.L1[1].StartChar)
	}
	leaf := tree.L3[0]
	if leaf.ParentL1ID == "" || leaf.ParentL2ID == "" || leaf.ParentID != leaf.ParentL2ID {
		t.Fatalf("leaf hierarchy is incomplete: %#v", leaf)
	}
	if tree.L2[0].TotalChildren != 3 || tree.L1[0].TotalChildren != 3 {
		t.Fatalf("child counts are wrong: L1=%d L2=%d", tree.L1[0].TotalChildren, tree.L2[0].TotalChildren)
	}
}

func TestChunkIDsAreStableForSameDocumentVersion(t *testing.T) {
	doc := Document{ID: uuid.NewString(), Scope: ScopePlatform, Title: "规则", Version: 1}
	first, err := ChunkDocument(doc, strings.Repeat("a", 800), MedQAChunkingConfig())
	if err != nil {
		t.Fatal(err)
	}
	second, err := ChunkDocument(doc, strings.Repeat("a", 800), MedQAChunkingConfig())
	if err != nil {
		t.Fatal(err)
	}
	if first.L3[0].ID != second.L3[0].ID {
		t.Fatalf("chunk IDs are not stable: %s != %s", first.L3[0].ID, second.L3[0].ID)
	}
}

func TestInvalidChunkConfigIsRejected(t *testing.T) {
	_, err := ChunkDocument(Document{ID: uuid.NewString()}, "text", ChunkingConfig{L1: ChunkSize{Size: 10, Overlap: 10}})
	if err == nil {
		t.Fatal("expected invalid overlap to fail")
	}
}
