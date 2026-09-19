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
	domains          []hypervisor.Domain
	domain           hypervisor.DomainInfo
	err              error
	filter           hypervisor.DomainFilter
	action           string
	actionVM         string
	snapshotRequest  hypervisor.SnapshotCreateRequest
	snapshotName     string
	snapshotChildren bool
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
func (f *fakeHypervisor) Subscribe(_ context.Context, _ string) hypervisor.Subscription {
	channel := make(chan hypervisor.LifecycleEvent, 1)
	channel <- hypervisor.LifecycleEvent{ID: 7, Cursor: "generation:7", Name: "web", Event: "started"}
	close(channel)
	return hypervisor.Subscription{Events: channel, Cancel: func() {}}
}
func (f *fakeHypervisor) Domain(context.Context, string) (hypervisor.DomainInfo, error) {
	return f.domain, f.err
}
func (f *fakeHypervisor) DomainStats(context.Context, string) (hypervisor.DomainStats, error) {
	return hypervisor.DomainStats{Name: "web", MemoryKiB: map[string]uint64{}, Disks: []hypervisor.BlockStats{}, Interfaces: []hypervisor.NetworkStats{}}, f.err
}
func (f *fakeHypervisor) DomainInterfaces(_ context.Context, _ string, source hypervisor.InterfaceAddressSource) (hypervisor.DomainInterfaces, error) {
	return hypervisor.DomainInterfaces{Name: "web", Source: source, Interfaces: []hypervisor.DomainInterface{}}, f.err
}
func (f *fakeHypervisor) Autostart(context.Context, string) (hypervisor.Autostart, error) {
	return hypervisor.Autostart{Name: "web", Enabled: true}, f.err
}
func (f *fakeHypervisor) SetAutostart(_ context.Context, _ string, enabled bool) (hypervisor.Autostart, error) {
	return hypervisor.Autostart{Name: "web", Enabled: enabled}, f.err
}
func (f *fakeHypervisor) ListSnapshots(context.Context, string) ([]hypervisor.Snapshot, error) {
	return []hypervisor.Snapshot{{Name: "clean", Current: true}}, f.err
}
func (f *fakeHypervisor) CreateSnapshot(_ context.Context, _ string, request hypervisor.SnapshotCreateRequest) (hypervisor.Snapshot, error) {
	f.snapshotRequest = request
	return hypervisor.Snapshot{Name: request.Name, Description: request.Description, Current: true}, f.err
}
func (f *fakeHypervisor) RevertSnapshot(_ context.Context, _, snapshot string, _ hypervisor.SnapshotRevertRequest) (hypervisor.SnapshotActionResult, error) {
	f.snapshotName = snapshot
	return hypervisor.SnapshotActionResult{Name: "web", Snapshot: snapshot, Status: "reverted"}, f.err
}
func (f *fakeHypervisor) DeleteSnapshot(_ context.Context, _, snapshot string, children bool) error {
	f.snapshotName, f.snapshotChildren = snapshot, children
	return f.err
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

func TestLifecycleEventStream(t *testing.T) {
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/events", nil)
	request.Header.Set("Last-Event-ID", "6")
	New(&fakeHypervisor{}, Options{}).ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Header().Get("Content-Type") != "text/event-stream" || !strings.Contains(response.Body.String(), "event: vm.lifecycle") || !strings.Contains(response.Body.String(), `"id":7`) {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
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
		{http.MethodGet, "/api/v1/vms/web/interfaces?source=agent", "application/json", `"source":"agent"`},
		{http.MethodGet, "/api/v1/vms/web/autostart", "application/json", `"enabled":true`},
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

func TestRejectsInvalidInterfaceSource(t *testing.T) {
	response := httptest.NewRecorder()
	New(&fakeHypervisor{}, Options{}).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/vms/web/interfaces?source=magic", nil))
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestSetAutostart(t *testing.T) {
	response := httptest.NewRecorder()
	New(&fakeHypervisor{}, Options{}).ServeHTTP(response, httptest.NewRequest(http.MethodPatch, "/api/v1/vms/web/autostart", strings.NewReader(`{"enabled":false}`)))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"enabled":false`) {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestSnapshotRoutes(t *testing.T) {
	fake := &fakeHypervisor{}
	handler := New(fake, Options{})

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/vms/web/snapshots", nil))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"name":"clean"`) {
		t.Fatalf("list status=%d body=%s", response.Code, response.Body.String())
	}

	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/v1/vms/web/snapshots", strings.NewReader(`{"name":"before-upgrade","description":"safe point","kind":"disk","quiesce":true}`)))
	if response.Code != http.StatusCreated || fake.snapshotRequest.Name != "before-upgrade" || fake.snapshotRequest.Kind != hypervisor.SnapshotKindDisk || !fake.snapshotRequest.Quiesce {
		t.Fatalf("create status=%d request=%+v body=%s", response.Code, fake.snapshotRequest, response.Body.String())
	}

	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/v1/vms/web/snapshots/before-upgrade/actions/revert", strings.NewReader(`{"state":"running"}`)))
	if response.Code != http.StatusOK || fake.snapshotName != "before-upgrade" || !strings.Contains(response.Body.String(), `"status":"reverted"`) {
		t.Fatalf("revert status=%d body=%s", response.Code, response.Body.String())
	}

	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodDelete, "/api/v1/vms/web/snapshots/before-upgrade?children=true", nil))
	if response.Code != http.StatusNoContent || fake.snapshotName != "before-upgrade" || !fake.snapshotChildren {
		t.Fatalf("delete status=%d snapshot=%q children=%v", response.Code, fake.snapshotName, fake.snapshotChildren)
	}
}

func TestSnapshotValidation(t *testing.T) {
	handler := New(&fakeHypervisor{}, Options{})
	tests := []struct {
		path string
		body string
		code string
	}{
		{"/api/v1/vms/web/snapshots", `{"name":"../unsafe","kind":"system"}`, "invalid_snapshot_name"},
		{"/api/v1/vms/web/snapshots", `{"name":"safe","kind":"memory"}`, "invalid_snapshot_kind"},
		{"/api/v1/vms/web/snapshots", `{"name":"safe","kind":"system","quiesce":true}`, "invalid_snapshot_options"},
		{"/api/v1/vms/web/snapshots", `{"name":"safe","kind":"system","extra":true}`, "invalid_request"},
	}
	for _, test := range tests {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, test.path, strings.NewReader(test.body)))
		if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), test.code) {
			t.Fatalf("body=%s status=%d response=%s", test.body, response.Code, response.Body.String())
		}
	}
}

func TestExistingSnapshotNamesAreAddressable(t *testing.T) {
	fake := &fakeHypervisor{}
	handler := New(fake, Options{})
	for _, test := range []struct {
		path string
		want string
	}{
		{"manual%20snapshot", "manual snapshot"},
		{"更新前", "更新前"},
		{"name%2Btag", "name+tag"},
	} {
		response := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, "/api/v1/vms/web/snapshots/"+test.path+"/actions/revert", nil)
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusOK || fake.snapshotName != test.want {
			t.Fatalf("path=%q status=%d snapshot=%q body=%s", test.path, response.Code, fake.snapshotName, response.Body.String())
		}
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
	if challenge := response.Header().Get("WWW-Authenticate"); challenge != `Bearer realm="libvirt-rest-api"` {
		t.Fatalf("WWW-Authenticate without token = %q", challenge)
	}

	request := httptest.NewRequest(http.MethodGet, "/api/v1/host", nil)
	request.Header.Set("Authorization", "Bearer invalid")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status with invalid token = %d", response.Code)
	}
	if challenge := response.Header().Get("WWW-Authenticate"); challenge != `Bearer realm="libvirt-rest-api"` {
		t.Fatalf("WWW-Authenticate with invalid token = %q", challenge)
	}

	request = httptest.NewRequest(http.MethodGet, "/api/v1/host", nil)
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
		{"control cannot create snapshot", "control", http.MethodPost, "/api/v1/vms/web/snapshots", http.StatusForbidden},
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
