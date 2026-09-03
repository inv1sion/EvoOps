package rag

import (
	"fmt"
	"strings"

	"github.com/google/uuid"
)

type window struct {
	text       string
	start, end int
}

func ChunkDocument(doc Document, content string, cfg ChunkingConfig) (ChunkTree, error) {
	if err := validateChunkingConfig(cfg); err != nil {
		return ChunkTree{}, err
	}
	content = strings.TrimSpace(content)
	if content == "" {
		return ChunkTree{}, fmt.Errorf("document content is empty")
	}
	namespace, err := uuid.Parse(doc.ID)
	if err != nil {
		return ChunkTree{}, fmt.Errorf("document id must be a UUID: %w", err)
	}
	var tree ChunkTree
	l1Windows := slidingWindows(content, cfg.L1)
	for l1Index, l1Window := range l1Windows {
		l1 := newChunk(namespace, doc, 1, l1Index, l1Window, "", "", "")
		l2Windows := slidingWindows(l1Window.text, cfg.L2)
		l1.TotalChildren = len(l2Windows)
		tree.L1 = append(tree.L1, l1)
		for l2Index, relativeL2 := range l2Windows {
			absoluteL2 := relativeL2
			absoluteL2.start += l1Window.start
			absoluteL2.end += l1Window.start
			l2 := newChunk(namespace, doc, 2, len(tree.L2), absoluteL2, l1.ID, l1.ID, "")
			l3Windows := slidingWindows(relativeL2.text, cfg.L3)
			l2.TotalChildren = len(l3Windows)
			tree.L2 = append(tree.L2, l2)
			for _, relativeL3 := range l3Windows {
				absoluteL3 := relativeL3
				absoluteL3.start += absoluteL2.start
				absoluteL3.end += absoluteL2.start
				l3 := newChunk(namespace, doc, 3, len(tree.L3), absoluteL3, l2.ID, l1.ID, l2.ID)
				tree.L3 = append(tree.L3, l3)
			}
			_ = l2Index
		}
	}
	return tree, nil
}

func validateChunkingConfig(cfg ChunkingConfig) error {
	for name, level := range map[string]ChunkSize{"L1": cfg.L1, "L2": cfg.L2, "L3": cfg.L3} {
		if level.Size <= 0 || level.Overlap < 0 || level.Overlap >= level.Size {
			return fmt.Errorf("%s chunk size must be positive and overlap must be in [0,size)", name)
		}
	}
	if cfg.L1.Size < cfg.L2.Size || cfg.L2.Size < cfg.L3.Size {
		return fmt.Errorf("chunk sizes must satisfy L1 >= L2 >= L3")
	}
	return nil
}

func slidingWindows(value string, cfg ChunkSize) []window {
	runes := []rune(value)
	if len(runes) == 0 {
		return nil
	}
	stride := cfg.Size - cfg.Overlap
	result := make([]window, 0, (len(runes)+stride-1)/stride)
	for start := 0; start < len(runes); start += stride {
		end := min(start+cfg.Size, len(runes))
		left, right := trimRuneBounds(runes, start, end)
		if left < right {
			result = append(result, window{text: string(runes[left:right]), start: left, end: right})
		}
		if end == len(runes) {
			break
		}
	}
	return result
}

func trimRuneBounds(value []rune, start, end int) (int, int) {
	for start < end && isSpace(value[start]) {
		start++
	}
	for end > start && isSpace(value[end-1]) {
		end--
	}
	return start, end
}

func isSpace(r rune) bool { return strings.TrimSpace(string(r)) == "" }

func newChunk(namespace uuid.UUID, doc Document, level, index int, part window, parent, parentL1, parentL2 string) Chunk {
	id := uuid.NewSHA1(namespace, []byte(fmt.Sprintf("v%d:l%d:%d:%d:%d", doc.Version, level, index, part.start, part.end))).String()
	return Chunk{
		ID: id, DocID: doc.ID, StoreID: doc.StoreID, Scope: doc.Scope, DocumentVersion: doc.Version,
		Level: level, Content: part.text, ChunkIndex: index, StartChar: part.start, EndChar: part.end,
		ParentID: parent, ParentL1ID: parentL1, ParentL2ID: parentL2, Title: doc.Title, Metadata: cloneMap(doc.Metadata),
	}
}

func cloneMap(input map[string]any) map[string]any {
	if input == nil {
		return nil
	}
	result := make(map[string]any, len(input))
	for key, value := range input {
		result[key] = value
	}
	return result
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
