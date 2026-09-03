package config

import (
	"bufio"
	"os"
	"strconv"
	"strings"
)

const (
	DefaultOpenAIBaseURL = "https://dashscope.aliyuncs.com/compatible-mode/v1"
	DefaultOpenAIModel   = "qwen3.7-flash-2026-07-15"
	DefaultJudgeModel    = "qwen3.7-max-2026-06-08"
)

type Config struct {
	Address              string
	DataDir              string
	DemoDataPath         string
	HarnessDataPath      string
	OpenAIAPIKey         string
	OpenAIBaseURL        string
	OpenAIModel          string
	JudgeModel           string
	PromptOptimizerModel string
	LLMEvalEnabled       bool
	ToolCallingEnabled   bool
	MCPSSEURLs           []string
	MCPAllowlist         []string
}

func Load() Config {
	// Local developer credentials live in the gitignored .env file. Existing
	// process environment variables always take precedence.
	_ = loadDotEnv(".env")
	openAIModel := env("OPENAI_MODEL", DefaultOpenAIModel)
	return Config{
		Address:              env("EVOOPS_ADDR", ":8080"),
		DataDir:              env("EVOOPS_DATA_DIR", "data/runtime"),
		DemoDataPath:         env("EVOOPS_DEMO_DATA", "data/demo/store.json"),
		HarnessDataPath:      env("EVOOPS_HARNESS_DATA", "data/harness/suite.json"),
		OpenAIAPIKey:         os.Getenv("OPENAI_API_KEY"),
		OpenAIBaseURL:        env("OPENAI_BASE_URL", DefaultOpenAIBaseURL),
		OpenAIModel:          openAIModel,
		JudgeModel:           env("EVOOPS_JUDGE_MODEL", DefaultJudgeModel),
		PromptOptimizerModel: env("EVOOPS_PROMPT_OPTIMIZER_MODEL", openAIModel),
		LLMEvalEnabled:       boolEnv("EVOOPS_LLM_EVAL_ENABLED", true),
		ToolCallingEnabled:   boolEnv("EVOOPS_TOOL_CALLING_ENABLED", true),
		MCPSSEURLs:           csv(os.Getenv("EVOOPS_MCP_SSE_URLS")),
		MCPAllowlist:         csv(os.Getenv("EVOOPS_MCP_TOOL_ALLOWLIST")),
	}
}

func boolEnv(key string, fallback bool) bool {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func loadDotEnv(path string) error {
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimSpace(strings.TrimPrefix(line, "export "))
		key, value, ok := strings.Cut(line, "=")
		key = strings.TrimSpace(key)
		if !ok || key == "" {
			continue
		}
		if _, exists := os.LookupEnv(key); exists {
			continue
		}
		value = strings.TrimSpace(value)
		if len(value) >= 2 && ((value[0] == '"' && value[len(value)-1] == '"') || (value[0] == '\'' && value[len(value)-1] == '\'')) {
			value = value[1 : len(value)-1]
		}
		if err := os.Setenv(key, value); err != nil {
			return err
		}
	}
	return scanner.Err()
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
