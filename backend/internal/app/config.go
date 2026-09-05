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
)

type Config struct {
	Address         string
	DataDir         string
	APIToken        string
	SourceURL       string
	RequestInterval time.Duration
	SyncTimeout     time.Duration
	AutoSync        bool
}

func DefaultConfig() Config {
	return Config{
		Address:         "127.0.0.1:5051",
		DataDir:         "data",
		SourceURL:       "https://business.cj.sina.cn/api/liquor_price",
		RequestInterval: time.Second,
		SyncTimeout:     5 * time.Minute,
	}
}

func LoadConfig() (Config, error) {
	cfg := DefaultConfig()
	for name, field := range map[string]*string{
		"BACKEND_ADDR": &cfg.Address, "BACKEND_DATA_DIR": &cfg.DataDir,
		"BACKEND_API_TOKEN": &cfg.APIToken, "LIQUOR_SOURCE_URL": &cfg.SourceURL,
	} {
		if value, exists := os.LookupEnv(name); exists {
			*field = value
		}
	}
	for name, field := range map[string]*time.Duration{
		"LIQUOR_REQUEST_INTERVAL": &cfg.RequestInterval, "LIQUOR_SYNC_TIMEOUT": &cfg.SyncTimeout,
	} {
		if value, exists := os.LookupEnv(name); exists {
			parsed, err := time.ParseDuration(value)
			if err != nil {
				return Config{}, fmt.Errorf("%s: %w", name, err)
			}
			*field = parsed
		}
	}
	if value, exists := os.LookupEnv("LIQUOR_AUTO_SYNC"); exists {
		parsed, err := strconv.ParseBool(value)
		if err != nil {
			return Config{}, fmt.Errorf("LIQUOR_AUTO_SYNC: %w", err)
		}
		cfg.AutoSync = parsed
	}
	return cfg, cfg.Validate()
}

func (c Config) Validate() error {
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
