package config

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

func clearEnvironment(t *testing.T) {
	t.Helper()
	for _, key := range []string{"LIBVIRT_URI", "QEMU_URI", "LISTEN_ADDR", "API_BEARER_TOKEN", "API_READ_TOKEN", "API_CONTROL_TOKEN", "API_ADMIN_TOKEN", "CORS_ORIGINS", "ALLOW_INSECURE_LISTEN", "ALLOW_INSECURE_LOOPBACK_LISTEN"} {
		t.Setenv(key, "")
	}
}

func TestLoadDefaults(t *testing.T) {
	clearEnvironment(t)
	t.Setenv("API_BEARER_TOKEN", "test-token")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.LibvirtURI != "qemu:///system" || cfg.ListenAddr != "127.0.0.1:8080" {
		t.Fatalf("Load() endpoint config = %#v", cfg)
	}
	if cfg.APIToken != "test-token" || cfg.CORSOrigins != nil {
		t.Fatalf("Load() security defaults = %#v", cfg)
	}
	wantTimeouts := HTTPTimeouts{5 * time.Second, 15 * time.Second, 30 * time.Second, 60 * time.Second}
	if cfg.Timeouts != wantTimeouts {
		t.Fatalf("Load() timeouts = %#v, want %#v", cfg.Timeouts, wantTimeouts)
	}
}

func TestLoadConfigured(t *testing.T) {
	clearEnvironment(t)
	t.Setenv("LIBVIRT_URI", "qemu+ssh://virt.example/system")
	t.Setenv("QEMU_URI", "ignored://legacy")
	t.Setenv("LISTEN_ADDR", "0.0.0.0:8443")
	t.Setenv("API_BEARER_TOKEN", "secret-token")
	t.Setenv("CORS_ORIGINS", "https://app.example, http://localhost:3000, https://app.example")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.LibvirtURI != "qemu+ssh://virt.example/system" || cfg.ListenAddr != "0.0.0.0:8443" || cfg.APIToken != "secret-token" {
		t.Fatalf("Load() config = %#v", cfg)
	}
	wantOrigins := []string{"https://app.example", "http://localhost:3000"}
	if !reflect.DeepEqual(cfg.CORSOrigins, wantOrigins) {
		t.Fatalf("Load() CORS origins = %#v, want %#v", cfg.CORSOrigins, wantOrigins)
	}
}

func TestLoadUsesLegacyQEMUURI(t *testing.T) {
	clearEnvironment(t)
	t.Setenv("QEMU_URI", "qemu+tcp://hypervisor/system")
	t.Setenv("API_BEARER_TOKEN", "test-token")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.LibvirtURI != "qemu+tcp://hypervisor/system" {
		t.Fatalf("Load() LibvirtURI = %q", cfg.LibvirtURI)
	}
}

func TestLoadRejectsUnsafeOrInvalidConfiguration(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
		want string
	}{
		{"loopback without token", nil, "API_BEARER_TOKEN is required"},
		{"public without token", map[string]string{"LISTEN_ADDR": "0.0.0.0:8080"}, "required for non-loopback"},
		{"loopback override on public", map[string]string{"LISTEN_ADDR": "0.0.0.0:8080", "ALLOW_INSECURE_LOOPBACK_LISTEN": "true"}, "does not permit external"},
		{"public opt-in invalid", map[string]string{"LISTEN_ADDR": "0.0.0.0:8080", "ALLOW_INSECURE_LISTEN": "sometimes"}, "must be a boolean"},
		{"loopback opt-in invalid", map[string]string{"ALLOW_INSECURE_LOOPBACK_LISTEN": "sometimes"}, "must be a boolean"},
		{"malformed listen address", map[string]string{"LISTEN_ADDR": "127.0.0.1"}, "host:port"},
		{"bad port", map[string]string{"LISTEN_ADDR": "127.0.0.1:70000"}, "invalid port"},
		{"token whitespace", map[string]string{"API_BEARER_TOKEN": "two words"}, "must not contain whitespace"},
		{"scoped token whitespace", map[string]string{"API_READ_TOKEN": "two words"}, "API_READ_TOKEN must not contain whitespace"},
		{"duplicate tokens", map[string]string{"API_READ_TOKEN": "same", "API_ADMIN_TOKEN": "same"}, "must not use the same token"},
		{"token surrounding whitespace", map[string]string{"API_BEARER_TOKEN": " token"}, "must not contain whitespace"},
		{"invalid opt-in on loopback", map[string]string{"ALLOW_INSECURE_LISTEN": "sometimes"}, "must be a boolean"},
		{"empty cors item", map[string]string{"CORS_ORIGINS": "https://one.example,,https://two.example"}, "empty origin"},
		{"cors path", map[string]string{"CORS_ORIGINS": "https://one.example/path"}, "invalid origin"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			clearEnvironment(t)
			for key, value := range test.env {
				t.Setenv(key, value)
			}
			_, err := Load()
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Load() error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestLoadAllowsExplicitInsecureListen(t *testing.T) {
	for _, address := range []string{"127.0.0.1:8080", "[::]:8080"} {
		t.Run(address, func(t *testing.T) {
			clearEnvironment(t)
			t.Setenv("LISTEN_ADDR", address)
			t.Setenv("ALLOW_INSECURE_LISTEN", "true")

			if _, err := Load(); err != nil {
				t.Fatalf("Load() error = %v", err)
			}
		})
	}
}

func TestLoadAllowsExplicitInsecureLoopbackListen(t *testing.T) {
	for _, address := range []string{"127.0.0.1:8080", "[::1]:8080"} {
		t.Run(address, func(t *testing.T) {
			clearEnvironment(t)
			t.Setenv("LISTEN_ADDR", address)
			t.Setenv("ALLOW_INSECURE_LOOPBACK_LISTEN", "true")

			if _, err := Load(); err != nil {
				t.Fatalf("Load() error = %v", err)
			}
		})
	}
}

func TestLoadRejectsHostnameForInsecureLoopbackOverride(t *testing.T) {
	clearEnvironment(t)
	t.Setenv("LISTEN_ADDR", "localhost:8080")
	t.Setenv("ALLOW_INSECURE_LOOPBACK_LISTEN", "true")

	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "does not permit external") {
		t.Fatalf("Load() error = %v, want hostname rejection", err)
	}
}
