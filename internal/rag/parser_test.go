package rag

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseTextMarkdownAndJSONL(t *testing.T) {
	dir := t.TempDir()
	cases := map[string]string{
		"playbook.txt": "低 ROI 先检查归因。",
		"playbook.md":  "# 广告手册\n先检查素材。",
		"playbook.jsonl": strings.Join([]string{
			`{"text":"第一条规则"}`,
			`{"question":"能否直接暂停？","answer":"必须审批。"}`,
		}, "\n"),
	}
	for name, body := range cases {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		parsed, err := ParseFile(path)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if parsed.Content == "" || parsed.Title != "playbook" {
			t.Fatalf("%s parsed incorrectly: %#v", name, parsed)
		}
	}
}

func TestParseRejectsUnsupportedAndInvalidJSONL(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.jsonl")
	if err := os.WriteFile(bad, []byte("{bad"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ParseFile(bad); err == nil {
		t.Fatal("expected malformed JSONL to fail")
	}
	unsupported := filepath.Join(dir, "bad.docx")
	if err := os.WriteFile(unsupported, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ParseFile(unsupported); err == nil {
		t.Fatal("expected unsupported format to fail")
	}
}
