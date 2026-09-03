package rag

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	pdf "github.com/ledongthuc/pdf"
)

const (
	maxSourceBytes = 20 << 20
	maxParsedBytes = 32 << 20
)

type ParsedFile struct {
	Title     string
	Content   string
	MediaType string
	Metadata  map[string]any
}

func ParseFile(path string) (parsed ParsedFile, err error) {
	info, err := os.Stat(path)
	if err != nil {
		return ParsedFile{}, fmt.Errorf("stat document: %w", err)
	}
	if !info.Mode().IsRegular() {
		return ParsedFile{}, fmt.Errorf("document must be a regular file")
	}
	if info.Size() > maxSourceBytes {
		return ParsedFile{}, fmt.Errorf("document exceeds %d MiB", maxSourceBytes>>20)
	}
	extension := strings.ToLower(filepath.Ext(path))
	parsed = ParsedFile{Title: strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)), Metadata: map[string]any{"filename": filepath.Base(path)}}
	switch extension {
	case ".txt":
		parsed.MediaType = "text/plain"
		parsed.Content, err = readUTF8(path)
	case ".md", ".markdown":
		parsed.MediaType = "text/markdown"
		parsed.Content, err = readUTF8(path)
	case ".jsonl":
		parsed.MediaType = "application/x-ndjson"
		parsed.Content, err = readJSONL(path)
	case ".pdf":
		parsed.MediaType = "application/pdf"
		parsed.Content, err = readPDF(path)
	default:
		return ParsedFile{}, fmt.Errorf("unsupported document extension %q; use PDF, TXT, Markdown or JSONL", extension)
	}
	if err != nil {
		return ParsedFile{}, err
	}
	parsed.Content = strings.TrimSpace(parsed.Content)
	if parsed.Content == "" {
		return ParsedFile{}, fmt.Errorf("parsed document is empty")
	}
	if len(parsed.Content) > maxParsedBytes {
		return ParsedFile{}, fmt.Errorf("parsed text exceeds %d MiB", maxParsedBytes>>20)
	}
	return parsed, nil
}

func readUTF8(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read document: %w", err)
	}
	data = bytes.TrimPrefix(data, []byte{0xEF, 0xBB, 0xBF})
	if !utf8.Valid(data) {
		return "", fmt.Errorf("document must be UTF-8 encoded")
	}
	return string(data), nil
}

func readJSONL(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open JSONL: %w", err)
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64<<10), 2<<20)
	var sections []string
	for lineNumber := 1; scanner.Scan(); lineNumber++ {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var item map[string]any
		if err := json.Unmarshal(line, &item); err != nil {
			return "", fmt.Errorf("decode JSONL line %d: %w", lineNumber, err)
		}
		if text := jsonlText(item); text != "" {
			sections = append(sections, text)
		}
	}
	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("scan JSONL: %w", err)
	}
	if len(sections) == 0 {
		return "", fmt.Errorf("JSONL contains none of text, content, question or answer")
	}
	return strings.Join(sections, "\n\n"), nil
}

func jsonlText(item map[string]any) string {
	for _, field := range []string{"text", "content"} {
		if value, ok := item[field].(string); ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	question, _ := item["question"].(string)
	answer, _ := item["answer"].(string)
	question, answer = strings.TrimSpace(question), strings.TrimSpace(answer)
	switch {
	case question != "" && answer != "":
		return "问题：" + question + "\n回答：" + answer
	case question != "":
		return "问题：" + question
	default:
		return answer
	}
}

func readPDF(path string) (content string, err error) {
	// PDF files are untrusted input. Convert parser panics into an ingest error;
	// size limits above and the caller's context timeout bound the public path.
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("parse PDF: %v", recovered)
		}
	}()
	file, reader, err := pdf.Open(path)
	if err != nil {
		return "", fmt.Errorf("open PDF: %w", err)
	}
	defer file.Close()
	plain, err := reader.GetPlainText()
	if err != nil {
		return "", fmt.Errorf("extract PDF text: %w", err)
	}
	data, err := io.ReadAll(io.LimitReader(plain, maxParsedBytes+1))
	if err != nil {
		return "", fmt.Errorf("read PDF text: %w", err)
	}
	if len(data) > maxParsedBytes {
		return "", fmt.Errorf("parsed PDF text exceeds %d MiB", maxParsedBytes>>20)
	}
	if !utf8.Valid(data) {
		return "", fmt.Errorf("PDF text extraction returned invalid UTF-8")
	}
	return string(data), nil
}
