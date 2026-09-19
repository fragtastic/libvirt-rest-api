// Package config loads and validates process configuration for the API server.
package config

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode"
)

const (
	defaultLibvirtURI = "qemu:///system"
	defaultListenAddr = "127.0.0.1:8080"
)

// HTTPTimeouts are server-side limits used when constructing an http.Server.
// They bound slow or stalled clients without placing a deadline on libvirt work.
type HTTPTimeouts struct {
	ReadHeader time.Duration
	Read       time.Duration
	Write      time.Duration
	Idle       time.Duration
}

// Config is the complete process configuration. An empty APIToken is accepted
// only when an explicit insecure-listen override is enabled.
type Config struct {
	LibvirtURI   string
	ListenAddr   string
	APIToken     string
	ReadToken    string
	ControlToken string
	AdminToken   string
	CORSOrigins  []string
	Timeouts     HTTPTimeouts
}

// Load reads configuration from the environment.
//
// LIBVIRT_URI is the preferred libvirt connection URI. QEMU_URI is accepted as
// a compatibility fallback while older deployments are migrated. Setting both
// uses LIBVIRT_URI. CORS_ORIGINS is a comma-separated list of exact HTTP(S)
// origins. An unset or empty value rejects browser cross-origin requests.
func Load() (Config, error) {
	libvirtURI := strings.TrimSpace(os.Getenv("LIBVIRT_URI"))
	if libvirtURI == "" {
		libvirtURI = strings.TrimSpace(os.Getenv("QEMU_URI"))
	}
	if libvirtURI == "" {
		libvirtURI = defaultLibvirtURI
	}

	token := os.Getenv("API_BEARER_TOKEN")
	cfg := Config{
		LibvirtURI:   libvirtURI,
		ListenAddr:   valueOrDefault("LISTEN_ADDR", defaultListenAddr),
		APIToken:     token,
		ReadToken:    os.Getenv("API_READ_TOKEN"),
		ControlToken: os.Getenv("API_CONTROL_TOKEN"),
		AdminToken:   os.Getenv("API_ADMIN_TOKEN"),
		Timeouts: HTTPTimeouts{
			ReadHeader: 5 * time.Second,
			Read:       15 * time.Second,
			Write:      30 * time.Second,
			Idle:       60 * time.Second,
		},
	}

	var err error
	if cfg.CORSOrigins, err = parseOrigins(os.Getenv("CORS_ORIGINS")); err != nil {
		return Config{}, err
	}
	if err := validateTokens(cfg); err != nil {
		return Config{}, err
	}
	if err := validateListenAddr(cfg.ListenAddr); err != nil {
		return Config{}, err
	}
	allowInsecure, err := boolEnv("ALLOW_INSECURE_LISTEN")
	if err != nil {
		return Config{}, err
	}
	allowInsecureLoopback, err := boolEnv("ALLOW_INSECURE_LOOPBACK_LISTEN")
	if err != nil {
		return Config{}, err
	}
	if cfg.hasCredentials() || allowInsecure {
		return cfg, nil
	}
	if isLoopbackListenAddr(cfg.ListenAddr) {
		if allowInsecureLoopback {
			return cfg, nil
		}
		return Config{}, fmt.Errorf("API_BEARER_TOKEN is required; for an unauthenticated loopback listener explicitly set ALLOW_INSECURE_LOOPBACK_LISTEN=true or ALLOW_INSECURE_LISTEN=true")
	}
	return Config{}, fmt.Errorf("API_BEARER_TOKEN is required for non-loopback LISTEN_ADDR %q; ALLOW_INSECURE_LOOPBACK_LISTEN does not permit external listeners, so explicitly set ALLOW_INSECURE_LISTEN=true to disable authentication", cfg.ListenAddr)
}

func (c Config) hasCredentials() bool {
	return c.APIToken != "" || c.ReadToken != "" || c.ControlToken != "" || c.AdminToken != ""
}

func valueOrDefault(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func validateTokens(cfg Config) error {
	tokens := map[string]string{
		"API_BEARER_TOKEN":  cfg.APIToken,
		"API_READ_TOKEN":    cfg.ReadToken,
		"API_CONTROL_TOKEN": cfg.ControlToken,
		"API_ADMIN_TOKEN":   cfg.AdminToken,
	}
	seen := make(map[string]string)
	for name, token := range tokens {
		if strings.IndexFunc(token, unicode.IsSpace) >= 0 {
			return fmt.Errorf("%s must not contain whitespace", name)
		}
		if token == "" {
			continue
		}
		if previous, exists := seen[token]; exists {
			return fmt.Errorf("%s and %s must not use the same token", previous, name)
		}
		seen[token] = name
	}
	return nil
}

func validateListenAddr(addr string) error {
	host, port, err := net.SplitHostPort(addr)
	if err != nil || port == "" {
		return fmt.Errorf("LISTEN_ADDR must be a host:port address: %q", addr)
	}
	if _, err := strconv.ParseUint(port, 10, 16); err != nil {
		return fmt.Errorf("LISTEN_ADDR has an invalid port: %q", addr)
	}
	_ = host // SplitHostPort also validates bracketed IPv6 addresses.
	return nil
}

func isLoopbackListenAddr(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func boolEnv(key string) (bool, error) {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return false, nil
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return false, fmt.Errorf("%s must be a boolean: %w", key, err)
	}
	return parsed, nil
}

func parseOrigins(raw string) ([]string, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}

	seen := make(map[string]struct{})
	origins := make([]string, 0)
	for _, part := range strings.Split(raw, ",") {
		origin := strings.TrimSpace(part)
		if origin == "" {
			return nil, fmt.Errorf("CORS_ORIGINS contains an empty origin")
		}
		parsed, err := url.Parse(origin)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
			return nil, fmt.Errorf("CORS_ORIGINS contains invalid origin %q", origin)
		}
		if _, exists := seen[origin]; !exists {
			seen[origin] = struct{}{}
			origins = append(origins, origin)
		}
	}
	return origins, nil
}
