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

// Config is the complete process configuration. An empty APIToken means that
// authentication middleware should not require a bearer token.
type Config struct {
	LibvirtURI  string
	ListenAddr  string
	APIToken    string
	CORSOrigins []string
	Timeouts    HTTPTimeouts
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
		LibvirtURI: libvirtURI,
		ListenAddr: valueOrDefault("LISTEN_ADDR", defaultListenAddr),
		APIToken:   token,
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
	if err := validateToken(cfg.APIToken); err != nil {
		return Config{}, err
	}
	if err := validateListenAddr(cfg.ListenAddr); err != nil {
		return Config{}, err
	}
	allowed, err := boolEnv("ALLOW_INSECURE_LISTEN")
	if err != nil {
		return Config{}, err
	}
	if !isLoopbackListenAddr(cfg.ListenAddr) && cfg.APIToken == "" {
		if !allowed {
			return Config{}, fmt.Errorf("LISTEN_ADDR %q is not loopback; set API_BEARER_TOKEN or explicitly set ALLOW_INSECURE_LISTEN=true", cfg.ListenAddr)
		}
	}
	return cfg, nil
}

func valueOrDefault(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func validateToken(token string) error {
	if strings.IndexFunc(token, unicode.IsSpace) >= 0 {
		return fmt.Errorf("API_BEARER_TOKEN must not contain whitespace")
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
	if strings.EqualFold(host, "localhost") {
		return true
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
