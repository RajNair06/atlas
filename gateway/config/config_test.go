package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// validYAML is a complete, valid config used as the base for every test.
// Validation-failure cases mutate exactly one line of it via strings.Replace,
// so each error message can be pinned to a single cause.
const validYAML = `server:
  port: 8080
  auto_heal: true
  require_approval: false
  approval_timeout: 120s
  healing_budget: 5s
  breaker_threshold: 3
  breaker_reset: 30s

llm:
  provider: gemini
  model: gemini-test-model
  api_key: test-key-123
  timeout: 2s
  max_healing_attempts: 3

routes:
  - path: /buy
    upstream: http://localhost:8081
    fallback: http://localhost:9091
    timeout: 10s
    retries: 3
    skip_healing: false
  - path: /health
    upstream: http://localhost:8091
    timeout: 120s
    retries: 0
    skip_healing: true
`

// writeConfig writes content to gateway.yaml inside a fresh t.TempDir()
// and returns the path.
func writeConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "gateway.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("writing test config: %v", err)
	}
	return path
}

func TestLoadValidConfigRoundTrip(t *testing.T) {
	cfg, err := Load(writeConfig(t, validYAML))
	if err != nil {
		t.Fatalf("unexpected error loading a valid config: %v", err)
	}

	// Server block, including duration parsing ("120s", "5s", "30s")
	if cfg.Server.Port != 8080 {
		t.Errorf("port = %d, want 8080", cfg.Server.Port)
	}
	if !cfg.Server.AutoHeal {
		t.Error("auto_heal = false, want true")
	}
	if cfg.Server.RequireApproval {
		t.Error("require_approval = true, want false")
	}
	if cfg.Server.ApprovalTimeout != 120*time.Second {
		t.Errorf("approval_timeout = %v, want 120s", cfg.Server.ApprovalTimeout)
	}
	if cfg.Server.HealingBudget != 5*time.Second {
		t.Errorf("healing_budget = %v, want 5s", cfg.Server.HealingBudget)
	}
	if cfg.Server.BreakerThreshold != 3 {
		t.Errorf("breaker_threshold = %d, want 3", cfg.Server.BreakerThreshold)
	}
	if cfg.Server.BreakerReset != 30*time.Second {
		t.Errorf("breaker_reset = %v, want 30s", cfg.Server.BreakerReset)
	}

	// LLM block
	if cfg.LLM.Provider != "gemini" {
		t.Errorf("provider = %q, want gemini", cfg.LLM.Provider)
	}
	if cfg.LLM.Model != "gemini-test-model" {
		t.Errorf("model = %q, want gemini-test-model", cfg.LLM.Model)
	}
	if cfg.LLM.APIKey != "test-key-123" {
		t.Errorf("api_key = %q, want test-key-123", cfg.LLM.APIKey)
	}
	if cfg.LLM.Timeout != 2*time.Second {
		t.Errorf("llm timeout = %v, want 2s", cfg.LLM.Timeout)
	}
	if cfg.LLM.MaxHealingAttempts != 3 {
		t.Errorf("max_healing_attempts = %d, want 3", cfg.LLM.MaxHealingAttempts)
	}

	// Routes block
	if len(cfg.Routes) != 2 {
		t.Fatalf("routes = %d, want 2", len(cfg.Routes))
	}
	r0 := cfg.Routes[0]
	if r0.Path != "/buy" || r0.Upstream != "http://localhost:8081" || r0.Fallback != "http://localhost:9091" {
		t.Errorf("route[0] = %+v, want /buy with upstream and fallback", r0)
	}
	if r0.Timeout != 10*time.Second || r0.Retries != 3 || r0.SkipHealing {
		t.Errorf("route[0] = %+v, want timeout 10s, retries 3, skip_healing false", r0)
	}
	r1 := cfg.Routes[1]
	if r1.Path != "/health" || r1.Timeout != 120*time.Second || !r1.SkipHealing {
		t.Errorf("route[1] = %+v, want /health with timeout 120s and skip_healing true", r1)
	}
}

func TestLoadDefaultsApprovalTimeout(t *testing.T) {
	yaml := strings.Replace(validYAML, "  approval_timeout: 120s\n", "", 1)

	cfg, err := Load(writeConfig(t, yaml))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Server.ApprovalTimeout != defaultApprovalTimeout {
		t.Fatalf("approval_timeout = %v, want the %v default", cfg.Server.ApprovalTimeout, defaultApprovalTimeout)
	}
}

func TestLoadSubstitutesEnvVar(t *testing.T) {
	t.Setenv("ATLAS_TEST_API_KEY", "key-from-env")
	yaml := strings.Replace(validYAML, "api_key: test-key-123", "api_key: ${ATLAS_TEST_API_KEY}", 1)

	cfg, err := Load(writeConfig(t, yaml))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.LLM.APIKey != "key-from-env" {
		t.Fatalf("api_key = %q, want the environment value", cfg.LLM.APIKey)
	}
}

func TestLoadEnvSubstitutionEmptyVar(t *testing.T) {
	// A set-but-empty variable substitutes to "" (LookupEnv reports it as
	// existing), which then fails api_key validation.
	t.Setenv("ATLAS_TEST_API_KEY", "")
	yaml := strings.Replace(validYAML, "api_key: test-key-123", "api_key: ${ATLAS_TEST_API_KEY}", 1)

	_, err := Load(writeConfig(t, yaml))
	if err == nil {
		t.Fatal("expected validation error for empty substituted api_key, got nil")
	}
	if !strings.Contains(err.Error(), "api_key") {
		t.Fatalf("error = %q, want it to mention api_key", err)
	}
}

func TestLoadEnvSubstitutionUnsetVarStaysLiteral(t *testing.T) {
	// Pin the documented behavior: when the variable is not set at all, the
	// ${VAR} placeholder is left as-is rather than substituted with "".
	const unsetVar = "ATLAS_TEST_DEFINITELY_UNSET_9F3A"
	if _, exists := os.LookupEnv(unsetVar); exists {
		t.Skipf("%s is unexpectedly set in the environment", unsetVar)
	}
	yaml := strings.Replace(validYAML, "api_key: test-key-123", "api_key: ${"+unsetVar+"}", 1)

	cfg, err := Load(writeConfig(t, yaml))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.LLM.APIKey != "${"+unsetVar+"}" {
		t.Fatalf("api_key = %q, want the untouched placeholder", cfg.LLM.APIKey)
	}
}

func TestLoadValidationErrors(t *testing.T) {
	cases := []struct {
		name      string
		mutation  func(yaml string) string
		wantError string // substring the returned error must contain
	}{
		{
			name:      "port zero",
			mutation:  replace("port: 8080", "port: 0"),
			wantError: "server.port must be between 1 and 65535",
		},
		{
			name:      "port too large",
			mutation:  replace("port: 8080", "port: 65536"),
			wantError: "server.port must be between 1 and 65535",
		},
		{
			name:      "negative healing budget",
			mutation:  replace("healing_budget: 5s", "healing_budget: -1s"),
			wantError: "healing_budget must not be negative",
		},
		{
			name:      "negative breaker threshold",
			mutation:  replace("breaker_threshold: 3", "breaker_threshold: -1"),
			wantError: "breaker_threshold must not be negative",
		},
		{
			name:      "negative breaker reset",
			mutation:  replace("breaker_reset: 30s", "breaker_reset: -1s"),
			wantError: "breaker_reset must not be negative",
		},
		{
			name:      "negative approval timeout",
			mutation:  replace("approval_timeout: 120s", "approval_timeout: -1s"),
			wantError: "approval_timeout must not be negative",
		},
		{
			name:      "missing provider",
			mutation:  drop("  provider: gemini\n"),
			wantError: "llm.provider is required",
		},
		{
			name:      "missing model",
			mutation:  drop("  model: gemini-test-model\n"),
			wantError: "llm.model is required",
		},
		{
			name:      "missing api key",
			mutation:  drop("  api_key: test-key-123\n"),
			wantError: "llm.api_key is required",
		},
		{
			name:      "zero llm timeout",
			mutation:  replace("timeout: 2s", "timeout: 0s"),
			wantError: "llm.timeout must be positive",
		},
		{
			name:      "negative max healing attempts",
			mutation:  replace("max_healing_attempts: 3", "max_healing_attempts: -1"),
			wantError: "max_healing_attempts must be non-negative",
		},
		{
			name:      "no routes",
			mutation:  func(y string) string { return y[:strings.Index(y, "routes:")] + "routes: []\n" },
			wantError: "at least one route is required",
		},
		{
			name:      "route missing path",
			mutation:  replace("  - path: /buy\n    upstream:", "  - upstream:"),
			wantError: "routes[0].path is required",
		},
		{
			name:      "route missing upstream",
			mutation:  drop("    upstream: http://localhost:8081\n"),
			wantError: "routes[0].upstream is required",
		},
		{
			name:      "route zero timeout",
			mutation:  replace("timeout: 10s", "timeout: 0s"),
			wantError: "routes[0].timeout must be positive",
		},
		{
			name:      "route negative retries",
			mutation:  replace("retries: 3\n    skip_healing: false", "retries: -1\n    skip_healing: false"),
			wantError: "routes[0].retries must be non-negative",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			mutated := c.mutation(validYAML)
			if mutated == validYAML {
				t.Fatalf("mutation %q did not change the config — test is vacuous", c.name)
			}

			_, err := Load(writeConfig(t, mutated))
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", c.wantError)
			}
			if !strings.Contains(err.Error(), c.wantError) {
				t.Fatalf("error = %q, want it to contain %q", err, c.wantError)
			}
		})
	}
}

// replace swaps old for newText exactly once; the mutation check in the test
// catches a missing anchor.
func replace(old, newText string) func(string) string {
	return func(y string) string { return strings.Replace(y, old, newText, 1) }
}

// drop removes a whole line from the config.
func drop(line string) func(string) string {
	return func(y string) string { return strings.Replace(y, line, "", 1) }
}

func TestLoadMalformedYAML(t *testing.T) {
	_, err := Load(writeConfig(t, "server: [this is not: valid yaml\n  :::"))
	if err == nil {
		t.Fatal("expected parse error for malformed YAML, got nil")
	}
	if !strings.Contains(err.Error(), "parsing config") {
		t.Fatalf("error = %q, want it to mention parsing", err)
	}
}

func TestLoadMissingFile(t *testing.T) {
	_, err := Load(filepath.Join(t.TempDir(), "does-not-exist.yaml"))
	if err == nil {
		t.Fatal("expected error for missing file, got nil")
	}
	if !strings.Contains(err.Error(), "reading config file") {
		t.Fatalf("error = %q, want it to mention reading the file", err)
	}
}
