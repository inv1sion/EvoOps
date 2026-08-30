package config

import (
	"os"
	"strconv"
	"strings"
)

type Config struct {
	Address       string
	DataDir       string
	DemoDataPath  string
	EvalDataPath  string
	OpenAIAPIKey  string
	OpenAIBaseURL string
	OpenAIModel   string
	MCPSSEURLs    []string
	MCPAllowlist  []string
}

func Load() Config {
	return Config{
		Address:       env("EVOOPS_ADDR", ":8080"),
		DataDir:       env("EVOOPS_DATA_DIR", "data/runtime"),
		DemoDataPath:  env("EVOOPS_DEMO_DATA", "data/demo/store.json"),
		EvalDataPath:  env("EVOOPS_EVAL_DATA", "data/demo/evals.json"),
		OpenAIAPIKey:  os.Getenv("OPENAI_API_KEY"),
		OpenAIBaseURL: os.Getenv("OPENAI_BASE_URL"),
		OpenAIModel:   env("OPENAI_MODEL", "gpt-4.1-mini"),
		MCPSSEURLs:    csv(os.Getenv("EVOOPS_MCP_SSE_URLS")),
		MCPAllowlist:  csv(os.Getenv("EVOOPS_MCP_TOOL_ALLOWLIST")),
	}
}

func env(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func csv(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		if part = strings.TrimSpace(part); part != "" {
			result = append(result, part)
		}
	}
	return result
}

func IntEnv(key string, fallback int) int {
	value, err := strconv.Atoi(os.Getenv(key))
	if err != nil {
		return fallback
	}
	return value
}
