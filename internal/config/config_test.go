package config

import "testing"

func TestLoadUsesQwenWorkspaceDefaultsWithoutEmbeddingCredential(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("OPENAI_BASE_URL", "")
	t.Setenv("OPENAI_MODEL", "")

	cfg := Load()
	if cfg.OpenAIAPIKey != "" {
		t.Fatal("configuration must not contain a hard-coded API key")
	}
	if cfg.OpenAIBaseURL != DefaultOpenAIBaseURL || cfg.OpenAIModel != DefaultOpenAIModel {
		t.Fatalf("unexpected model defaults: base_url=%q model=%q", cfg.OpenAIBaseURL, cfg.OpenAIModel)
	}
}

func TestLoadAllowsModelEndpointOverride(t *testing.T) {
	t.Setenv("OPENAI_BASE_URL", "https://example.test/v1")
	t.Setenv("OPENAI_MODEL", "test-model")

	cfg := Load()
	if cfg.OpenAIBaseURL != "https://example.test/v1" || cfg.OpenAIModel != "test-model" {
		t.Fatalf("environment override was ignored: %#v", cfg)
	}
}
