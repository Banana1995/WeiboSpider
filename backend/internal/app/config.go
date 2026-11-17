package app

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/Banana1995/WeiboSpider/backend/internal/modules/ledger"
	"github.com/Banana1995/WeiboSpider/backend/internal/modules/options"
)

type Config struct {
	Address             string
	DataDir             string
	APIToken            string
	SourceURL           string
	RequestInterval     time.Duration
	SyncTimeout         time.Duration
	AutoSync            bool
	LedgerEnabled       bool
	LedgerMCPEnabled    bool
	LedgerWeeklyEnabled bool
	LedgerWeeklyTime    string
	OptionsEnabled      bool
	OptionsQuotesURL    string
	OptionsLimits       options.Limits
}

func DefaultConfig() Config {
	return Config{
		Address:          "127.0.0.1:5051",
		DataDir:          "data",
		SourceURL:        "https://business.cj.sina.cn/api/liquor_price",
		RequestInterval:  time.Second,
		SyncTimeout:      5 * time.Minute,
		LedgerWeeklyTime: ledger.DefaultWeeklyTime,
		OptionsQuotesURL: "http://127.0.0.1:5053",
		OptionsLimits:    options.DefaultLimits(),
	}
}

func LoadConfig() (Config, error) {
	cfg := DefaultConfig()
	for name, field := range map[string]*string{
		"BACKEND_ADDR": &cfg.Address, "BACKEND_DATA_DIR": &cfg.DataDir,
		"BACKEND_API_TOKEN": &cfg.APIToken, "LIQUOR_SOURCE_URL": &cfg.SourceURL,
		"LEDGER_WEEKLY_TIME": &cfg.LedgerWeeklyTime,
		"OPTIONS_QUOTES_URL": &cfg.OptionsQuotesURL,
	} {
		if value, exists := os.LookupEnv(name); exists {
			*field = value
		}
	}
	for name, field := range map[string]*time.Duration{
		"LIQUOR_REQUEST_INTERVAL": &cfg.RequestInterval, "LIQUOR_SYNC_TIMEOUT": &cfg.SyncTimeout,
		"OPTIONS_TASK_TIMEOUT":  &cfg.OptionsLimits.Timeout,
		"OPTIONS_DIRECTORY_TTL": &cfg.OptionsLimits.DirectoryTTL,
	} {
		if value, exists := os.LookupEnv(name); exists {
			parsed, err := time.ParseDuration(value)
			if err != nil {
				return Config{}, fmt.Errorf("%s: %w", name, err)
			}
			*field = parsed
		}
	}
	for name, field := range map[string]*bool{
		"LIQUOR_AUTO_SYNC": &cfg.AutoSync, "LEDGER_ENABLED": &cfg.LedgerEnabled,
		"LEDGER_MCP_ENABLED": &cfg.LedgerMCPEnabled,
		"OPTIONS_ENABLED":    &cfg.OptionsEnabled,
	} {
		if value, exists := os.LookupEnv(name); exists {
			parsed, err := strconv.ParseBool(value)
			if err != nil {
				return Config{}, fmt.Errorf("%s: %w", name, err)
			}
			*field = parsed
		}
	}
	if value, exists := os.LookupEnv("LEDGER_WEEKLY_ENABLED"); exists {
		if value != "true" && value != "false" {
			return Config{}, errors.New("LEDGER_WEEKLY_ENABLED must be true or false")
		}
		cfg.LedgerWeeklyEnabled = value == "true"
	}
	for name, field := range map[string]*int{"OPTIONS_MAX_WATCH": &cfg.OptionsLimits.MaxWatch, "OPTIONS_MAX_CONTRACTS": &cfg.OptionsLimits.MaxContracts} {
		if value, exists := os.LookupEnv(name); exists {
			parsed, err := strconv.Atoi(value)
			if err != nil {
				return Config{}, fmt.Errorf("%s: %w", name, err)
			}
			*field = parsed
		}
	}
	if value, exists := os.LookupEnv("OPTIONS_HISTORY_BUDGET_MB"); exists {
		n, err := strconv.ParseInt(value, 10, 64)
		if err != nil || n < 1 || n > 4096 {
			return Config{}, errors.New("OPTIONS_HISTORY_BUDGET_MB must be in 1..4096")
		}
		cfg.OptionsLimits.HistoryBytes = n << 20
	}
	return cfg, cfg.Validate()
}

func (c Config) Validate() error {
	if c.OptionsEnabled {
		if _, err := options.NewHTTPProvider(c.OptionsQuotesURL); err != nil {
			return err
		}
		v := c.OptionsLimits
		if v.MaxWatch < 1 || v.MaxWatch > 100 || v.MaxContracts < 1 || v.MaxContracts > 20000 || v.Timeout < time.Second || v.Timeout > time.Hour || v.DirectoryTTL < time.Minute || v.DirectoryTTL > 7*24*time.Hour || v.HistoryBytes < 1024 {
			return errors.New("options resource limits are invalid")
		}
	}
	if c.LedgerMCPEnabled && !c.LedgerEnabled {
		return errors.New("LEDGER_MCP_ENABLED requires LEDGER_ENABLED")
	}
	if c.LedgerWeeklyEnabled && !c.LedgerEnabled {
		return errors.New("LEDGER_WEEKLY_ENABLED requires LEDGER_ENABLED")
	}
	if err := (ledger.WeeklyConfig{Enabled: c.LedgerWeeklyEnabled, Time: c.LedgerWeeklyTime}).Validate(); err != nil {
		return err
	}
	host, port, err := net.SplitHostPort(c.Address)
	if err != nil {
		return fmt.Errorf("BACKEND_ADDR must be host:port: %w", err)
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 0 || n > 65535 {
		return errors.New("BACKEND_ADDR requires a port in 0..65535")
	}
	if !loopbackHost(host) && c.APIToken == "" {
		return errors.New("BACKEND_API_TOKEN is required for non-loopback listeners")
	}
	if c.APIToken != "" {
		if len(c.APIToken) < 32 || len(c.APIToken) > 512 || strings.IndexFunc(c.APIToken, func(r rune) bool { return r < 33 || r > 126 }) >= 0 {
			return errors.New("BACKEND_API_TOKEN must contain 32..512 non-whitespace ASCII characters")
		}
	}
	if strings.TrimSpace(c.DataDir) == "" {
		return errors.New("BACKEND_DATA_DIR must not be empty")
	}
	u, err := url.Parse(c.SourceURL)
	if err != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return errors.New("LIQUOR_SOURCE_URL must be an HTTP(S) base URL without credentials, query or fragment")
	}
	if c.RequestInterval < 0 || c.RequestInterval > time.Minute {
		return errors.New("LIQUOR_REQUEST_INTERVAL must be between 0 and 1m")
	}
	if c.SyncTimeout < time.Second || c.SyncTimeout > 30*time.Minute {
		return errors.New("LIQUOR_SYNC_TIMEOUT must be between 1s and 30m")
	}
	return nil
}

func loopbackHost(host string) bool {
	if name, _, err := net.SplitHostPort(host); err == nil {
		host = name
	}
	return host == "localhost" || net.ParseIP(host).IsLoopback()
}
