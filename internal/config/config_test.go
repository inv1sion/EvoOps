package config

import (
	"os"
	"path/filepath"
	"testing"
)

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

func TestLoadDotEnvUsesFileWithoutOverridingProcessEnvironment(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, []byte("EVOOPS_TEST_FILE_VALUE=from-file\nEVOOPS_TEST_EXISTING=from-file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("EVOOPS_TEST_EXISTING", "from-process")
	if err := loadDotEnv(path); err != nil {
		t.Fatal(err)
	}
	if os.Getenv("EVOOPS_TEST_FILE_VALUE") != "from-file" {
		t.Fatal("dotenv value was not loaded")
	}
	if os.Getenv("EVOOPS_TEST_EXISTING") != "from-process" {
		t.Fatal("dotenv overrode an existing process variable")
	}
	t.Cleanup(func() { _ = os.Unsetenv("EVOOPS_TEST_FILE_VALUE") })
}
