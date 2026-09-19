package api

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/fragtastic/libvirt-rest-api/internal/hypervisor"
)

type fakeHypervisor struct {
	domains  []hypervisor.Domain
	domain   hypervisor.DomainInfo
	err      error
	filter   hypervisor.DomainFilter
	action   string
	actionVM string
}

func (f *fakeHypervisor) Ready(context.Context) error { return f.err }

func (f *fakeHypervisor) Host(context.Context) (hypervisor.Host, error) {
	return hypervisor.Host{Name: "test-host", CPUs: 8}, f.err
}
func (f *fakeHypervisor) HostStats(context.Context) (hypervisor.HostStats, error) {
	return hypervisor.HostStats{MemoryKiB: map[string]uint64{"total": 1024}}, f.err
}
func (f *fakeHypervisor) ListDomains(_ context.Context, filter hypervisor.DomainFilter) ([]hypervisor.Domain, error) {
	f.filter = filter
	return f.domains, f.err
}
func (f *fakeHypervisor) Domain(context.Context, string) (hypervisor.DomainInfo, error) {
	return f.domain, f.err
}
func (f *fakeHypervisor) DomainStats(context.Context, string) (hypervisor.DomainStats, error) {
	return hypervisor.DomainStats{Name: "web", MemoryKiB: map[string]uint64{}, Disks: []hypervisor.BlockStats{}, Interfaces: []hypervisor.NetworkStats{}}, f.err
}
func (f *fakeHypervisor) DomainXML(context.Context, string) (string, error) {
	return "<domain><name>test</name></domain>", f.err
}
func (f *fakeHypervisor) Viewer(context.Context, string) (hypervisor.Viewer, error) {
	listen, port := "127.0.0.1", 5900
	return hypervisor.Viewer{Type: "spice", Listen: &listen, Port: &port}, f.err
}
func (f *fakeHypervisor) Screenshot(context.Context, string) (hypervisor.Screenshot, error) {
	return hypervisor.Screenshot{ContentType: "image/png", Data: io.NopCloser(strings.NewReader("png"))}, f.err
}
func (f *fakeHypervisor) Start(_ context.Context, name string) (hypervisor.ActionResult, error) {
	f.action, f.actionVM = "start", name
	return hypervisor.ActionResult{Name: "database", UUID: "52d7a2fe-1942-4a89-93d9-9a57d8f67b6d", State: "running"}, f.err
}
func (f *fakeHypervisor) Shutdown(_ context.Context, name string, _ hypervisor.PowerMode) (hypervisor.ActionResult, error) {
	f.action, f.actionVM = "shutdown", name
	return hypervisor.ActionResult{Name: "database", UUID: "52d7a2fe-1942-4a89-93d9-9a57d8f67b6d", State: "running", Status: "shutdown-requested"}, f.err
}
func (f *fakeHypervisor) Reboot(_ context.Context, name string, _ hypervisor.PowerMode) (hypervisor.ActionResult, error) {
	f.action, f.actionVM = "reboot", name
	return hypervisor.ActionResult{Name: "database", UUID: "52d7a2fe-1942-4a89-93d9-9a57d8f67b6d", State: "running", Status: "reboot-requested"}, f.err
}
func (f *fakeHypervisor) Pause(_ context.Context, name string) (hypervisor.ActionResult, error) {
	f.action = "pause"
	return hypervisor.ActionResult{Name: name, State: "paused"}, f.err
}
func (f *fakeHypervisor) Resume(_ context.Context, name string) (hypervisor.ActionResult, error) {
	f.action = "resume"
	return hypervisor.ActionResult{Name: name, State: "running"}, f.err
}
func (f *fakeHypervisor) Reset(_ context.Context, name string) (hypervisor.ActionResult, error) {
	f.action = "reset"
	return hypervisor.ActionResult{Name: name, State: "running"}, f.err
}
func (f *fakeHypervisor) Stop(_ context.Context, name string) (hypervisor.ActionResult, error) {
	f.action, f.actionVM = "stop", name
	return hypervisor.ActionResult{Name: "database", UUID: "52d7a2fe-1942-4a89-93d9-9a57d8f67b6d", State: "shutoff"}, f.err
}
func (*fakeHypervisor) Close() error { return nil }

func TestListVMs(t *testing.T) {
	fake := &fakeHypervisor{domains: []hypervisor.Domain{{Name: "web", UUID: "52d7a2fe-1942-4a89-93d9-9a57d8f67b6d", ID: 2, State: "running", Active: true}}}
	request := httptest.NewRequest(http.MethodGet, "/api/v1/vms?state=active", nil)
	response := httptest.NewRecorder()
	New(fake, Options{}).ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if fake.filter != hypervisor.DomainActive || !strings.Contains(response.Body.String(), `"uuid":"52d7a2fe-1942-4a89-93d9-9a57d8f67b6d"`) {
		t.Fatalf("filter = %d, body = %s", fake.filter, response.Body.String())
	}
}

func TestReadiness(t *testing.T) {
	fake := &fakeHypervisor{}
	handler := New(fake, Options{BearerToken: "secret"})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"status":"ready"`) {
		t.Fatalf("ready status = %d, body = %s", response.Code, response.Body.String())
	}
	fake.err = errors.New("disconnected")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if response.Code != http.StatusServiceUnavailable || strings.Contains(response.Body.String(), "disconnected") {
		t.Fatalf("failed status = %d, body = %s", response.Code, response.Body.String())
	}
}

func TestListVMsRejectsUnknownFilter(t *testing.T) {
	response := httptest.NewRecorder()
	New(&fakeHypervisor{}, Options{}).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/vms?state=paused", nil))
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "invalid_state") {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
}

func TestVMRoutes(t *testing.T) {
	fake := &fakeHypervisor{domain: hypervisor.DomainInfo{Domain: hypervisor.Domain{Name: "web", UUID: "52d7a2fe-1942-4a89-93d9-9a57d8f67b6d"}, VCPUs: 2}}
	tests := []struct {
		method      string
		path        string
		contentType string
		body        string
	}{
		{http.MethodGet, "/api/v1/host", "application/json", `"name":"test-host"`},
		{http.MethodGet, "/api/v1/vms/web", "application/json", `"uuid":"52d7a2fe-1942-4a89-93d9-9a57d8f67b6d"`},
		{http.MethodGet, "/api/v1/host/stats", "application/json", `"total":1024`},
		{http.MethodGet, "/api/v1/vms/web/stats", "application/json", `"name":"web"`},
		{http.MethodGet, "/api/v1/vms/web/xml", "application/xml", "<domain>"},
		{http.MethodGet, "/api/v1/vms/web/viewer", "application/json", `"port":5900`},
		{http.MethodGet, "/api/v1/vms/web/screenshot", "image/png", "png"},
	}
	for _, test := range tests {
		t.Run(test.path, func(t *testing.T) {
			response := httptest.NewRecorder()
			New(fake, Options{}).ServeHTTP(response, httptest.NewRequest(test.method, test.path, nil))
			if response.Code != http.StatusOK || !strings.HasPrefix(response.Header().Get("Content-Type"), test.contentType) || !strings.Contains(response.Body.String(), test.body) {
				t.Fatalf("status = %d, type = %q, body = %s", response.Code, response.Header().Get("Content-Type"), response.Body.String())
			}
		})
	}
}

func TestActionsAndErrors(t *testing.T) {
	fake := &fakeHypervisor{}
	handler := New(fake, Options{})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/v1/vms/database/actions/start", nil))
	if response.Code != http.StatusOK || fake.action != "start" || fake.actionVM != "database" {
		t.Fatalf("status = %d, action = %s %s", response.Code, fake.action, fake.actionVM)
	}
	uuid := "52d7a2fe-1942-4a89-93d9-9a57d8f67b6d"
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/v1/vms/"+uuid+"/actions/stop", nil))
	if response.Code != http.StatusOK || fake.action != "stop" || fake.actionVM != uuid || !strings.Contains(response.Body.String(), `"name":"database"`) || !strings.Contains(response.Body.String(), `"uuid":"`+uuid+`"`) {
		t.Fatalf("UUID action: status = %d, action = %s %s, body = %s", response.Code, fake.action, fake.actionVM, response.Body.String())
	}
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/v1/vms/"+uuid+"/actions/shutdown", nil))
	if response.Code != http.StatusAccepted || fake.action != "shutdown" || fake.actionVM != uuid || !strings.Contains(response.Body.String(), `"state":"running"`) || !strings.Contains(response.Body.String(), `"status":"shutdown-requested"`) {
		t.Fatalf("graceful shutdown: status = %d, action = %s %s, body = %s", response.Code, fake.action, fake.actionVM, response.Body.String())
	}
	response = httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/vms/"+uuid+"/actions/reboot", strings.NewReader(`{"mode":"acpi"}`))
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusAccepted || fake.action != "reboot" || !strings.Contains(response.Body.String(), `"status":"reboot-requested"`) {
		t.Fatalf("reboot: status = %d, body = %s", response.Code, response.Body.String())
	}

	fake.err = hypervisor.ErrNotFound
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/vms/missing", nil))
	if response.Code != http.StatusNotFound || !strings.Contains(response.Body.String(), "vm_not_found") {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}

	fake.err = errors.New("connection lost")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/host", nil))
	if response.Code != http.StatusServiceUnavailable || strings.Contains(response.Body.String(), "connection lost") {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
}

func TestUnsupportedGracefulShutdown(t *testing.T) {
	fake := &fakeHypervisor{err: hypervisor.ErrUnsupported}
	response := httptest.NewRecorder()
	New(fake, Options{}).ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/v1/vms/database/actions/shutdown", nil))
	if response.Code != http.StatusUnprocessableEntity || !strings.Contains(response.Body.String(), "operation_unsupported") {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
}

func TestRejectsInvalidPowerMode(t *testing.T) {
	response := httptest.NewRecorder()
	New(&fakeHypervisor{}, Options{}).ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/v1/vms/web/actions/shutdown", strings.NewReader(`{"mode":"magic"}`)))
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "invalid_mode") {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
}

func TestPauseResumeAndResetRoutes(t *testing.T) {
	fake := &fakeHypervisor{}
	handler := New(fake, Options{})
	for _, test := range []struct{ path, action, state string }{
		{"pause", "pause", "paused"}, {"resume", "resume", "running"}, {"reset", "reset", "running"},
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/v1/vms/web/actions/"+test.path, nil))
		if response.Code != http.StatusOK || fake.action != test.action || !strings.Contains(response.Body.String(), `"state":"`+test.state+`"`) {
			t.Fatalf("%s: status=%d body=%s", test.path, response.Code, response.Body.String())
		}
	}
}

func TestAuthentication(t *testing.T) {
	handler := New(&fakeHypervisor{}, Options{BearerToken: "secret"})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/host", nil))
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status without token = %d", response.Code)
	}

	request := httptest.NewRequest(http.MethodGet, "/api/v1/host", nil)
	request.Header.Set("Authorization", "Bearer secret")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status with token = %d, body = %s", response.Code, response.Body.String())
	}

	request = httptest.NewRequest(http.MethodGet, "/api/v1/host", nil)
	request.Header.Set("Authorization", "bearer secret")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status with lowercase scheme = %d, body = %s", response.Code, response.Body.String())
	}

	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("health status = %d", response.Code)
	}
}

func TestAuthorizationScopes(t *testing.T) {
	handler := New(&fakeHypervisor{}, Options{ReadToken: "read", ControlToken: "control", AdminToken: "admin"})
	tests := []struct {
		name   string
		token  string
		method string
		path   string
		status int
	}{
		{"read can read", "read", http.MethodGet, "/api/v1/host", http.StatusOK},
		{"read cannot control", "read", http.MethodPost, "/api/v1/vms/web/actions/start", http.StatusForbidden},
		{"control can read", "control", http.MethodGet, "/api/v1/host", http.StatusOK},
		{"control can start", "control", http.MethodPost, "/api/v1/vms/web/actions/start", http.StatusOK},
		{"control cannot force stop", "control", http.MethodPost, "/api/v1/vms/web/actions/stop", http.StatusForbidden},
		{"admin can force stop", "admin", http.MethodPost, "/api/v1/vms/web/actions/stop", http.StatusOK},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(test.method, test.path, nil)
			request.Header.Set("Authorization", "Bearer "+test.token)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != test.status {
				t.Fatalf("status = %d, want %d, body = %s", response.Code, test.status, response.Body.String())
			}
		})
	}
}

func TestRoutingErrorsUseJSON(t *testing.T) {
	handler := New(&fakeHypervisor{}, Options{})
	tests := []struct {
		method string
		path   string
		status int
		code   string
		allow  string
	}{
		{http.MethodGet, "/not-an-endpoint", http.StatusNotFound, "not_found", ""},
		{http.MethodDelete, "/api/v1/host", http.StatusMethodNotAllowed, "method_not_allowed", "GET, HEAD"},
		{http.MethodGet, "/api/v1/vms/web/actions/start", http.StatusMethodNotAllowed, "method_not_allowed", "POST"},
	}
	for _, test := range tests {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(test.method, test.path, nil))
		if response.Code != test.status || !strings.HasPrefix(response.Header().Get("Content-Type"), "application/json") || !strings.Contains(response.Body.String(), test.code) || response.Header().Get("Allow") != test.allow {
			t.Fatalf("%s %s: status = %d, allow = %q, body = %s", test.method, test.path, response.Code, response.Header().Get("Allow"), response.Body.String())
		}
	}
}

func TestRouteRegistrySupportsMultipleMethods(t *testing.T) {
	mux := http.NewServeMux()
	routes := newRouteRegistry(mux)
	routes.handle(http.MethodGet, "/resource", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	routes.handle(http.MethodPatch, "/resource", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })

	for method, want := range map[string]int{http.MethodGet: http.StatusOK, http.MethodPatch: http.StatusNoContent} {
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, httptest.NewRequest(method, "/resource", nil))
		if response.Code != want {
			t.Fatalf("%s status = %d, want %d", method, response.Code, want)
		}
	}
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, httptest.NewRequest(http.MethodDelete, "/resource", nil))
	if response.Code != http.StatusMethodNotAllowed || response.Header().Get("Allow") != "GET, HEAD, PATCH" {
		t.Fatalf("DELETE status = %d, Allow = %q", response.Code, response.Header().Get("Allow"))
	}
}

func TestCORSUsesExactAllowlist(t *testing.T) {
	handler := New(&fakeHypervisor{}, Options{AllowedOrigins: []string{"https://console.example"}})
	request := httptest.NewRequest(http.MethodGet, "/api/v1/host", nil)
	request.Header.Set("Origin", "https://evil.example")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("disallowed origin status = %d", response.Code)
	}

	request = httptest.NewRequest(http.MethodGet, "/api/v1/host", nil)
	request.Header.Set("Origin", "https://console.example")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Header().Get("Access-Control-Allow-Origin") != "https://console.example" {
		t.Fatalf("status = %d, allow origin = %q", response.Code, response.Header().Get("Access-Control-Allow-Origin"))
	}
}

func TestCORSRejectsBrowserOriginWhenDisabled(t *testing.T) {
	fake := &fakeHypervisor{}
	handler := New(fake, Options{})
	request := httptest.NewRequest(http.MethodPost, "/api/v1/vms/database/actions/start", nil)
	request.Header.Set("Origin", "https://attacker.example")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden || fake.action != "" {
		t.Fatalf("status = %d, action = %q", response.Code, fake.action)
	}
}
