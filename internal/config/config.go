package config

import (
	"fmt"
	"log/slog"
	"os"
	"strconv"
)

type Config struct {
	Port        int
	LogLevel    slog.Level
	DatabaseURL string
}

const (
	defaultPort     = 8080
	defaultLogLevel = "info"
	defaultDSN      = "postgres://atlas:atlas@localhost:5433/atlas?sslmode=disable"
)

func Load() (Config, error) {
	cfg := Config{
		Port:        defaultPort,
		DatabaseURL: defaultDSN,
	}

	portStr, ok := os.LookupEnv("ATLAS_PORT")
	if ok && portStr != "" {
		port, err := strconv.Atoi(portStr)
		if err != nil {
			return Config{}, fmt.Errorf("ATLAS_PORT must be an integer: %w", err)
		}
		if port < 1 || port > 65535 {
			return Config{}, fmt.Errorf("ATLAS_PORT must be between 1 and 65535, got %d", port)
		}
		cfg.Port = port
	}

	logLevelStr, ok := os.LookupEnv("ATLAS_LOG_LEVEL")
	if !ok || logLevelStr == "" {
		logLevelStr = defaultLogLevel
	}

	var level slog.Level
	if err := level.UnmarshalText([]byte(logLevelStr)); err != nil {
		return Config{}, fmt.Errorf("ATLAS_LOG_LEVEL must be one of debug, info, warn, error: %w", err)
	}
	cfg.LogLevel = level
	if dsn, ok := os.LookupEnv("ATLAS_DATABASE_URL"); ok && dsn != "" {
		cfg.DatabaseURL = dsn
	}

	return cfg, nil
}
