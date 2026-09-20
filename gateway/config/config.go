package config

import (
	"fmt"
	"os"
	"regexp"
	"time"

	"gopkg.in/yaml.v3"
)

// Config is the root configuration structure
type Config struct {
	Server ServerConfig `yaml:"server"`
	LLM    LLMConfig    `yaml:"llm"`
	Routes []RouteConfig `yaml:"routes"`
}

// ServerConfig holds gateway server settings
type ServerConfig struct {
	Port             int           `yaml:"port"`
	AutoHeal         bool          `yaml:"auto_heal"`
	HealingBudget    time.Duration `yaml:"healing_budget"`
	BreakerThreshold int           `yaml:"breaker_threshold"`
	BreakerReset     time.Duration `yaml:"breaker_reset"`
}

// LLMConfig holds LLM provider settings
type LLMConfig struct {
	Provider           string        `yaml:"provider"`
	Model              string        `yaml:"model"`
	APIKey             string        `yaml:"api_key"`
	Timeout            time.Duration `yaml:"timeout"`
	MaxHealingAttempts int           `yaml:"max_healing_attempts"`
}

// RouteConfig defines how to route a specific path
type RouteConfig struct {
	Path        string        `yaml:"path"`
	Upstream    string        `yaml:"upstream"`
	Fallback    string        `yaml:"fallback"`
	Timeout     time.Duration `yaml:"timeout"`
	Retries     int           `yaml:"retries"`
	SkipHealing bool          `yaml:"skip_healing"`
}

// Load reads and parses the gateway.yaml file
// It also substitutes environment variables in the format ${VAR}
func Load(path string) (*Config, error) {
	// Read the file
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config file: %w", err)
	}

	// Substitute environment variables
	content := substituteEnvVars(string(data))

	// Parse YAML into Config struct
	var cfg Config
	if err := yaml.Unmarshal([]byte(content), &cfg); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}

	// Validate the config
	if err := cfg.validate(); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}

	return &cfg, nil
}

// substituteEnvVars replaces ${VAR} with the value of environment variable VAR
func substituteEnvVars(content string) string {
	// Regex to match ${VAR} patterns
	re := regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)
	
	return re.ReplaceAllStringFunc(content, func(match string) string {
		// Extract variable name (remove ${ and })
		varName := match[2 : len(match)-1]
		
		// Get value from environment
		if value, exists := os.LookupEnv(varName); exists {
			return value
		}
		
		// If not set, leave the placeholder as-is
		return match
	})
}

// validate checks that the config is valid
func (c *Config) validate() error {
	// Server validation
	if c.Server.Port <= 0 || c.Server.Port > 65535 {
		return fmt.Errorf("server.port must be between 1 and 65535, got %d", c.Server.Port)
	}
	if c.Server.HealingBudget < 0 {
		return fmt.Errorf("server.healing_budget must not be negative, got %v", c.Server.HealingBudget)
	}
	if c.Server.BreakerThreshold < 0 {
		return fmt.Errorf("server.breaker_threshold must not be negative, got %d", c.Server.BreakerThreshold)
	}
	if c.Server.BreakerReset < 0 {
		return fmt.Errorf("server.breaker_reset must not be negative, got %v", c.Server.BreakerReset)
	}

	// LLM validation
	if c.LLM.Provider == "" {
		return fmt.Errorf("llm.provider is required")
	}
	if c.LLM.Model == "" {
		return fmt.Errorf("llm.model is required")
	}
	if c.LLM.APIKey == "" {
		return fmt.Errorf("llm.api_key is required (set GEMINI_API_KEY environment variable)")
	}
	if c.LLM.Timeout <= 0 {
		return fmt.Errorf("llm.timeout must be positive, got %v", c.LLM.Timeout)
	}
	if c.LLM.MaxHealingAttempts < 0 {
		return fmt.Errorf("llm.max_healing_attempts must be non-negative, got %d", c.LLM.MaxHealingAttempts)
	}

	// Routes validation
	if len(c.Routes) == 0 {
		return fmt.Errorf("at least one route is required")
	}

	for i, route := range c.Routes {
		if route.Path == "" {
			return fmt.Errorf("routes[%d].path is required", i)
		}
		if route.Upstream == "" {
			return fmt.Errorf("routes[%d].upstream is required", i)
		}
		if route.Timeout <= 0 {
			return fmt.Errorf("routes[%d].timeout must be positive, got %v", i, route.Timeout)
		}
		if route.Retries < 0 {
			return fmt.Errorf("routes[%d].retries must be non-negative, got %d", i, route.Retries)
		}
	}

	return nil
}
