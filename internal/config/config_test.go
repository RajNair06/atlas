package config

import (
	"log/slog"
	"testing"
)

func TestLoad(t *testing.T) {
	tests := []struct {
		name    string
		env     map[string]string
		want    Config
		wantErr bool
	}{
		{
			name: "defaults when nothing is set",
			want: Config{
				Port:        defaultPort,
				LogLevel:    slog.LevelInfo,
				DatabaseURL: defaultDSN,
			},
		},
		{
			name: "explicit values",
			env: map[string]string{
				"ATLAS_PORT":      "9000",
				"ATLAS_LOG_LEVEL": "debug",
			},
			want: Config{
				Port:        9000,
				LogLevel:    slog.LevelDebug,
				DatabaseURL: defaultDSN,
			},
		},
		{
			name: "empty port falls back to default",
			env: map[string]string{
				"ATLAS_PORT": "",
			},
			want: Config{
				Port:        defaultPort,
				LogLevel:    slog.LevelInfo,
				DatabaseURL: defaultDSN,
			},
		},
		{
			name: "case-insensitive log level",
			env: map[string]string{
				"ATLAS_LOG_LEVEL": "WARN",
			},
			want: Config{
				Port:        defaultPort,
				LogLevel:    slog.LevelWarn,
				DatabaseURL: defaultDSN,
			},
		},
		{
			name: "invalid port",
			env: map[string]string{
				"ATLAS_PORT": "not-a-number",
			},
			wantErr: true,
		},
		{
			name: "port out of range",
			env: map[string]string{
				"ATLAS_PORT": "70000",
			},
			wantErr: true,
		},
		{
			name: "invalid log level",
			env: map[string]string{
				"ATLAS_LOG_LEVEL": "chatty",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for k, v := range tt.env {
				t.Setenv(k, v)
			}

			got, err := Load()
			if tt.wantErr {
				if err == nil {
					t.Fatalf("Load() expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("Load() unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("Load() = %+v, want %+v", got, tt.want)
			}
		})
	}
}
