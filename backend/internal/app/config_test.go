package app

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestConfig_DefaultsAreLocalAndDoNotStartExternalJobs(t *testing.T) {
	cfg := DefaultConfig()
	require.NoError(t, cfg.Validate())
	require.Equal(t, "127.0.0.1:5051", cfg.Address)
	require.False(t, cfg.AutoSync)
	require.Equal(t, time.Second, cfg.RequestInterval)
}

func TestConfig_RejectsInvalidSettings(t *testing.T) {
	tests := []struct {
		name string
		set  func(*Config)
	}{
		{"address", func(c *Config) { c.Address = "localhost" }},
		{"port", func(c *Config) { c.Address = "localhost:70000" }},
		{"short token", func(c *Config) { c.APIToken = "short" }},
		{"whitespace token", func(c *Config) { c.APIToken = strings.Repeat(" ", 32) }},
		{"directory", func(c *Config) { c.DataDir = " " }},
		{"source scheme", func(c *Config) { c.SourceURL = "file:///tmp/source.json" }},
		{"source credentials", func(c *Config) { c.SourceURL = "https://user:secret@example.com" }},
		{"source query", func(c *Config) { c.SourceURL += "?key=secret" }},
		{"negative interval", func(c *Config) { c.RequestInterval = -time.Second }},
		{"zero timeout", func(c *Config) { c.SyncTimeout = 0 }},
		{"unbounded timeout", func(c *Config) { c.SyncTimeout = time.Hour }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := DefaultConfig()
			test.set(&cfg)
			require.Error(t, cfg.Validate())
		})
	}
}

func TestLoadConfig_UsesSeparateBackendEnvironment(t *testing.T) {
	t.Setenv("BACKEND_ADDR", "127.0.0.1:12345")
	t.Setenv("BACKEND_DATA_DIR", "/tmp/platform")
	t.Setenv("BACKEND_API_TOKEN", strings.Repeat("x", 32))
	t.Setenv("LIQUOR_SOURCE_URL", "https://example.com/api/liquor")
	t.Setenv("LIQUOR_REQUEST_INTERVAL", "2s")
	t.Setenv("LIQUOR_SYNC_TIMEOUT", "1m")
	t.Setenv("LIQUOR_AUTO_SYNC", "true")
	t.Setenv("DB_PATH", "/tmp/must-not-open-weibo.db")
	cfg, err := LoadConfig()
	require.NoError(t, err)
	require.Equal(t, "/tmp/platform", cfg.DataDir)
	require.Equal(t, "127.0.0.1:12345", cfg.Address)
	require.Equal(t, "https://example.com/api/liquor", cfg.SourceURL)
	require.Equal(t, 2*time.Second, cfg.RequestInterval)
	require.Equal(t, time.Minute, cfg.SyncTimeout)
	require.True(t, cfg.AutoSync)
}

func TestLoadConfig_RejectsMalformedEnvironment(t *testing.T) {
	for _, name := range []string{"LIQUOR_REQUEST_INTERVAL", "LIQUOR_SYNC_TIMEOUT", "LIQUOR_AUTO_SYNC"} {
		t.Run(name, func(t *testing.T) {
			t.Setenv(name, "invalid")
			_, err := LoadConfig()
			require.ErrorContains(t, err, name)
		})
	}
}
