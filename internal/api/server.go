package api

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"runtime/debug"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/fragtastic/libvirt-rest-api/internal/hypervisor"
)

const maxRequestBody = 1 << 20

type Options struct {
	Logger         *slog.Logger
	BearerToken    string
	ReadToken      string
	ControlToken   string
	AdminToken     string
	AllowedOrigins []string
}

type scope uint8

const (
	scopePublic scope = iota
	scopeRead
	scopeControl
	scopeAdmin
)

type Server struct {
	hypervisor     hypervisor.Service
	logger         *slog.Logger
	bearerToken    string
	readToken      string
	controlToken   string
	adminToken     string
	allowedOrigins []string
}

func New(service hypervisor.Service, options Options) http.Handler {
	logger := options.Logger
	if logger == nil {
		logger = slog.Default()
	}
	s := &Server{
		hypervisor:     service,
		logger:         logger,
		bearerToken:    options.BearerToken,
		readToken:      options.ReadToken,
		controlToken:   options.ControlToken,
		adminToken:     options.AdminToken,
		allowedOrigins: append([]string(nil), options.AllowedOrigins...),
	}

	mux := http.NewServeMux()
	routes := newRouteRegistry(mux)
	s.handle(routes, http.MethodGet, "/healthz", scopePublic, s.health)
	s.handle(routes, http.MethodGet, "/readyz", scopePublic, s.ready)
	s.handle(routes, http.MethodGet, "/api/v1/host", scopeRead, s.host)
	s.handle(routes, http.MethodGet, "/api/v1/host/stats", scopeRead, s.hostStats)
	s.handle(routes, http.MethodGet, "/api/v1/vms", scopeRead, s.listVMs)
	s.handle(routes, http.MethodGet, "/api/v1/events", scopeRead, s.events)
	s.handle(routes, http.MethodGet, "/api/v1/vms/{identifier}", scopeRead, s.vm)
	s.handle(routes, http.MethodGet, "/api/v1/vms/{identifier}/stats", scopeRead, s.vmStats)
	s.handle(routes, http.MethodGet, "/api/v1/vms/{identifier}/interfaces", scopeRead, s.vmInterfaces)
	s.handle(routes, http.MethodGet, "/api/v1/vms/{identifier}/autostart", scopeRead, s.vmAutostart)
	s.handle(routes, http.MethodPatch, "/api/v1/vms/{identifier}/autostart", scopeAdmin, s.setVMAutostart)
	s.handle(routes, http.MethodGet, "/api/v1/vms/{identifier}/snapshots", scopeRead, s.listVMSnapshots)
	s.handle(routes, http.MethodPost, "/api/v1/vms/{identifier}/snapshots", scopeAdmin, s.createVMSnapshot)
	s.handle(routes, http.MethodPost, "/api/v1/vms/{identifier}/snapshots/{snapshot}/actions/revert", scopeAdmin, s.revertVMSnapshot)
	s.handle(routes, http.MethodDelete, "/api/v1/vms/{identifier}/snapshots/{snapshot}", scopeAdmin, s.deleteVMSnapshot)
	s.handle(routes, http.MethodGet, "/api/v1/vms/{identifier}/xml", scopeRead, s.vmXML)
	s.handle(routes, http.MethodGet, "/api/v1/vms/{identifier}/viewer", scopeRead, s.vmViewer)
	s.handle(routes, http.MethodGet, "/api/v1/vms/{identifier}/screenshot", scopeRead, s.vmScreenshot)
	s.handle(routes, http.MethodPost, "/api/v1/vms/{identifier}/actions/start", scopeControl, s.vmStart)
	s.handle(routes, http.MethodPost, "/api/v1/vms/{identifier}/actions/shutdown", scopeControl, s.vmShutdown)
	s.handle(routes, http.MethodPost, "/api/v1/vms/{identifier}/actions/reboot", scopeControl, s.vmReboot)
	s.handle(routes, http.MethodPost, "/api/v1/vms/{identifier}/actions/pause", scopeControl, s.vmPause)
	s.handle(routes, http.MethodPost, "/api/v1/vms/{identifier}/actions/resume", scopeControl, s.vmResume)
	s.handle(routes, http.MethodPost, "/api/v1/vms/{identifier}/actions/reset", scopeAdmin, s.vmReset)
	s.handle(routes, http.MethodPost, "/api/v1/vms/{identifier}/actions/stop", scopeAdmin, s.vmStop)
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		writeError(w, http.StatusNotFound, "not_found", "endpoint not found")
	})

	return s.recoverPanic(s.accessLog(s.securityHeaders(s.cors(mux))))
}

func (s *Server) handle(routes *routeRegistry, method, path string, required scope, handler http.HandlerFunc) {
	routes.handle(method, path, s.requireScope(required, handler))
}

type routeRegistry struct {
	mux    *http.ServeMux
	routes map[string]map[string]http.HandlerFunc
}

func newRouteRegistry(mux *http.ServeMux) *routeRegistry {
	return &routeRegistry{mux: mux, routes: make(map[string]map[string]http.HandlerFunc)}
}

func (r *routeRegistry) handle(method, path string, handler http.HandlerFunc) {
	methods, exists := r.routes[path]
	if !exists {
		methods = make(map[string]http.HandlerFunc)
		r.routes[path] = methods
		r.mux.HandleFunc(path, func(w http.ResponseWriter, request *http.Request) {
			requestMethod := request.Method
			if requestMethod == http.MethodHead {
				requestMethod = http.MethodGet
			}
			if matched, ok := methods[requestMethod]; ok {
				matched(w, request)
				return
			}
			allowed := make([]string, 0, len(methods)+1)
			for allowedMethod := range methods {
				allowed = append(allowed, allowedMethod)
				if allowedMethod == http.MethodGet {
					allowed = append(allowed, http.MethodHead)
				}
			}
			slices.Sort(allowed)
			w.Header().Set("Allow", strings.Join(allowed, ", "))
			writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		})
	}
	if _, duplicate := methods[method]; duplicate {
		panic("duplicate route: " + method + " " + path)
	}
	methods[method] = handler
}

func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) ready(w http.ResponseWriter, r *http.Request) {
	if err := s.hypervisor.Ready(r.Context()); err != nil {
		s.logger.Warn("readiness check failed", "error", err)
		writeError(w, http.StatusServiceUnavailable, "hypervisor_unavailable", "hypervisor is not ready")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

func (s *Server) host(w http.ResponseWriter, r *http.Request) {
	host, err := s.hypervisor.Host(r.Context())
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, host)
}

func (s *Server) hostStats(w http.ResponseWriter, r *http.Request) {
	stats, err := s.hypervisor.HostStats(r.Context())
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, stats)
}

func (s *Server) listVMs(w http.ResponseWriter, r *http.Request) {
	filter, ok := parseDomainFilter(r.URL.Query().Get("state"))
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid_state", "state must be all, active, or inactive")
		return
	}
	domains, err := s.hypervisor.ListDomains(r.Context(), filter)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	if domains == nil {
		domains = []hypervisor.Domain{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"vms": domains})
}

func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming_unsupported", "streaming is unavailable")
		return
	}
	cursor := r.Header.Get("Last-Event-ID")
	_ = http.NewResponseController(w).SetWriteDeadline(time.Time{})
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, ": connected\n\n")
	flusher.Flush()
	subscription := s.hypervisor.Subscribe(r.Context(), cursor)
	defer subscription.Cancel()
	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case event, open := <-subscription.Events:
			if !open {
				return
			}
			if event.StreamReset {
				data, err := json.Marshal(map[string]any{"id": event.ID, "occurred_at": event.OccurredAt, "reason": "replay_unavailable"})
				if err != nil {
					return
				}
				if _, err := fmt.Fprintf(w, "id: %s\nevent: stream.reset\ndata: %s\n\n", event.Cursor, data); err != nil {
					return
				}
				flusher.Flush()
				continue
			}
			data, err := json.Marshal(event)
			if err != nil {
				return
			}
			if _, err := fmt.Fprintf(w, "id: %s\nevent: vm.lifecycle\ndata: %s\n\n", event.Cursor, data); err != nil {
				return
			}
			flusher.Flush()
		case <-heartbeat.C:
			if _, err := io.WriteString(w, ": heartbeat\n\n"); err != nil {
				return
			}
			flusher.Flush()
		case <-r.Context().Done():
			return
		}
	}
}

func (s *Server) vm(w http.ResponseWriter, r *http.Request) {
	vm, err := s.hypervisor.Domain(r.Context(), r.PathValue("identifier"))
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, vm)
}

func (s *Server) vmStats(w http.ResponseWriter, r *http.Request) {
	stats, err := s.hypervisor.DomainStats(r.Context(), r.PathValue("identifier"))
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, stats)
}

func (s *Server) vmInterfaces(w http.ResponseWriter, r *http.Request) {
	source := hypervisor.InterfaceAddressSource(strings.ToLower(r.URL.Query().Get("source")))
	if source == "" {
		source = hypervisor.InterfaceSourceLease
	}
	if source != hypervisor.InterfaceSourceLease && source != hypervisor.InterfaceSourceAgent && source != hypervisor.InterfaceSourceARP {
		writeError(w, http.StatusBadRequest, "invalid_source", "source must be lease, agent, or arp")
		return
	}
	interfaces, err := s.hypervisor.DomainInterfaces(r.Context(), r.PathValue("identifier"), source)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, interfaces)
}

func (s *Server) vmAutostart(w http.ResponseWriter, r *http.Request) {
	value, err := s.hypervisor.Autostart(r.Context(), r.PathValue("identifier"))
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, value)
}

func (s *Server) setVMAutostart(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Enabled *bool `json:"enabled"`
	}
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil || request.Enabled == nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "request body must contain boolean enabled")
		return
	}
	value, err := s.hypervisor.SetAutostart(r.Context(), r.PathValue("identifier"), *request.Enabled)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, value)
}

func (s *Server) listVMSnapshots(w http.ResponseWriter, r *http.Request) {
	snapshots, err := s.hypervisor.ListSnapshots(r.Context(), r.PathValue("identifier"))
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	if snapshots == nil {
		snapshots = []hypervisor.Snapshot{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"snapshots": snapshots})
}

func (s *Server) createVMSnapshot(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Name        string                  `json:"name"`
		Description string                  `json:"description"`
		Kind        hypervisor.SnapshotKind `json:"kind"`
		Quiesce     bool                    `json:"quiesce"`
	}
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "request body must be one JSON object")
		return
	}
	if !validSnapshotName(request.Name) {
		writeError(w, http.StatusBadRequest, "invalid_snapshot_name", "name must be 1-128 ASCII letters, numbers, dots, underscores, or hyphens and must start with a letter or number")
		return
	}
	if len(request.Description) > 1024 {
		writeError(w, http.StatusBadRequest, "invalid_description", "description must not exceed 1024 bytes")
		return
	}
	if request.Kind != hypervisor.SnapshotKindSystem && request.Kind != hypervisor.SnapshotKindDisk {
		writeError(w, http.StatusBadRequest, "invalid_snapshot_kind", "kind must be system or disk")
		return
	}
	if request.Quiesce && request.Kind != hypervisor.SnapshotKindDisk {
		writeError(w, http.StatusBadRequest, "invalid_snapshot_options", "quiesce is only valid for disk snapshots")
		return
	}
	_ = http.NewResponseController(w).SetWriteDeadline(time.Time{})
	snapshot, err := s.hypervisor.CreateSnapshot(r.Context(), r.PathValue("identifier"), hypervisor.SnapshotCreateRequest{
		Name: request.Name, Description: request.Description, Kind: request.Kind, Quiesce: request.Quiesce,
	})
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, snapshot)
}

func (s *Server) revertVMSnapshot(w http.ResponseWriter, r *http.Request) {
	snapshotName := r.PathValue("snapshot")
	if !validSnapshotLookupName(snapshotName) {
		writeError(w, http.StatusBadRequest, "invalid_snapshot_name", "invalid snapshot name")
		return
	}
	request := struct {
		State hypervisor.SnapshotRevertState `json:"state"`
		Force bool                           `json:"force"`
	}{State: hypervisor.SnapshotRevertRecorded}
	if r.ContentLength != 0 {
		if err := decodeJSON(r, &request); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request", "request body must be one JSON object")
			return
		}
	}
	if request.State == "" {
		request.State = hypervisor.SnapshotRevertRecorded
	}
	if request.State != hypervisor.SnapshotRevertRecorded && request.State != hypervisor.SnapshotRevertRunning && request.State != hypervisor.SnapshotRevertPaused {
		writeError(w, http.StatusBadRequest, "invalid_revert_state", "state must be snapshot, running, or paused")
		return
	}
	_ = http.NewResponseController(w).SetWriteDeadline(time.Time{})
	result, err := s.hypervisor.RevertSnapshot(r.Context(), r.PathValue("identifier"), snapshotName, hypervisor.SnapshotRevertRequest{State: request.State, Force: request.Force})
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) deleteVMSnapshot(w http.ResponseWriter, r *http.Request) {
	snapshotName := r.PathValue("snapshot")
	if !validSnapshotLookupName(snapshotName) {
		writeError(w, http.StatusBadRequest, "invalid_snapshot_name", "invalid snapshot name")
		return
	}
	children := false
	if value := r.URL.Query().Get("children"); value != "" {
		parsed, err := strconv.ParseBool(value)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_children", "children must be true or false")
			return
		}
		children = parsed
	}
	_ = http.NewResponseController(w).SetWriteDeadline(time.Time{})
	if err := s.hypervisor.DeleteSnapshot(r.Context(), r.PathValue("identifier"), snapshotName, children); err != nil {
		s.writeServiceError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) vmXML(w http.ResponseWriter, r *http.Request) {
	xml, err := s.hypervisor.DomainXML(r.Context(), r.PathValue("identifier"))
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(xml))
}

func (s *Server) vmViewer(w http.ResponseWriter, r *http.Request) {
	viewer, err := s.hypervisor.Viewer(r.Context(), r.PathValue("identifier"))
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, viewer)
}

func (s *Server) vmScreenshot(w http.ResponseWriter, r *http.Request) {
	screenshot, err := s.hypervisor.Screenshot(r.Context(), r.PathValue("identifier"))
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	defer screenshot.Data.Close()
	w.Header().Set("Content-Type", screenshot.ContentType)
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	if _, err := io.Copy(w, screenshot.Data); err != nil {
		s.logger.Warn("stream screenshot", "error", err)
	}
}

func (s *Server) vmStart(w http.ResponseWriter, r *http.Request) {
	s.runAction(w, r, http.StatusOK, s.hypervisor.Start)
}

func (s *Server) vmShutdown(w http.ResponseWriter, r *http.Request) {
	mode, ok := decodePowerMode(w, r)
	if !ok {
		return
	}
	result, err := s.hypervisor.Shutdown(r.Context(), r.PathValue("identifier"), mode)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, result)
}

func (s *Server) vmReboot(w http.ResponseWriter, r *http.Request) {
	mode, ok := decodePowerMode(w, r)
	if !ok {
		return
	}
	result, err := s.hypervisor.Reboot(r.Context(), r.PathValue("identifier"), mode)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, result)
}

func (s *Server) vmStop(w http.ResponseWriter, r *http.Request) {
	s.runAction(w, r, http.StatusOK, s.hypervisor.Stop)
}

func (s *Server) vmPause(w http.ResponseWriter, r *http.Request) {
	s.runAction(w, r, http.StatusOK, s.hypervisor.Pause)
}
func (s *Server) vmResume(w http.ResponseWriter, r *http.Request) {
	s.runAction(w, r, http.StatusOK, s.hypervisor.Resume)
}
func (s *Server) vmReset(w http.ResponseWriter, r *http.Request) {
	s.runAction(w, r, http.StatusOK, s.hypervisor.Reset)
}

func decodePowerMode(w http.ResponseWriter, r *http.Request) (hypervisor.PowerMode, bool) {
	if r.ContentLength == 0 {
		return hypervisor.PowerModeDefault, true
	}
	var request struct {
		Mode hypervisor.PowerMode `json:"mode"`
	}
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "request body must be a JSON object containing mode")
		return "", false
	}
	if request.Mode == "" {
		request.Mode = hypervisor.PowerModeDefault
	}
	switch request.Mode {
	case hypervisor.PowerModeDefault, hypervisor.PowerModeACPI, hypervisor.PowerModeGuestAgent:
		return request.Mode, true
	default:
		writeError(w, http.StatusBadRequest, "invalid_mode", "mode must be default, acpi, or guest-agent")
		return "", false
	}
}

func (s *Server) runAction(w http.ResponseWriter, r *http.Request, status int, action func(context.Context, string) (hypervisor.ActionResult, error)) {
	identifier := r.PathValue("identifier")
	result, err := action(r.Context(), identifier)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	writeJSON(w, status, result)
}

func parseDomainFilter(value string) (hypervisor.DomainFilter, bool) {
	switch strings.ToLower(value) {
	case "", "all":
		return hypervisor.DomainAll, true
	case "active":
		return hypervisor.DomainActive, true
	case "inactive":
		return hypervisor.DomainInactive, true
	default:
		return 0, false
	}
}

func decodeJSON(r *http.Request, destination any) error {
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("request body must contain one JSON value")
	}
	return nil
}

func validSnapshotName(name string) bool {
	if len(name) == 0 || len(name) > 128 || !isASCIILetterOrNumber(name[0]) {
		return false
	}
	for index := 1; index < len(name); index++ {
		character := name[index]
		if !isASCIILetterOrNumber(character) && character != '.' && character != '_' && character != '-' {
			return false
		}
	}
	return true
}

func validSnapshotLookupName(name string) bool {
	return len(name) > 0 && len(name) <= 1024 && utf8.ValidString(name) && !strings.ContainsRune(name, '\x00')
}

func isASCIILetterOrNumber(character byte) bool {
	return character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character >= '0' && character <= '9'
}

func (s *Server) writeServiceError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, hypervisor.ErrNotFound):
		writeError(w, http.StatusNotFound, "vm_not_found", "virtual machine not found")
	case errors.Is(err, hypervisor.ErrSnapshotNotFound):
		writeError(w, http.StatusNotFound, "snapshot_not_found", "snapshot not found")
	case errors.Is(err, hypervisor.ErrConflict):
		writeError(w, http.StatusConflict, "state_conflict", err.Error())
	case errors.Is(err, hypervisor.ErrUnsupported):
		writeError(w, http.StatusUnprocessableEntity, "operation_unsupported", err.Error())
	default:
		s.logger.Error("hypervisor request failed", "error", err)
		writeError(w, http.StatusServiceUnavailable, "hypervisor_unavailable", "hypervisor request failed")
	}
}

func (s *Server) requireScope(required scope, next http.Handler) http.HandlerFunc {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if required == scopePublic || !s.hasCredentials() {
			next.ServeHTTP(w, r)
			return
		}
		authorization := r.Header.Get("Authorization")
		scheme, provided, found := strings.Cut(authorization, " ")
		if !found || !strings.EqualFold(scheme, "Bearer") || provided == "" {
			w.Header().Set("WWW-Authenticate", `Bearer realm="virt-rest-api"`)
			writeError(w, http.StatusUnauthorized, "unauthorized", "a valid bearer token is required")
			return
		}
		providedScope, valid := s.scopeForToken(provided)
		if !valid {
			w.Header().Set("WWW-Authenticate", `Bearer realm="virt-rest-api"`)
			writeError(w, http.StatusUnauthorized, "unauthorized", "a valid bearer token is required")
			return
		}
		if providedScope < required {
			writeError(w, http.StatusForbidden, "insufficient_scope", "the bearer token does not grant the required scope")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) hasCredentials() bool {
	return s.bearerToken != "" || s.readToken != "" || s.controlToken != "" || s.adminToken != ""
}

func (s *Server) scopeForToken(token string) (scope, bool) {
	candidates := []struct {
		token string
		scope scope
	}{
		{s.readToken, scopeRead},
		{s.controlToken, scopeControl},
		{s.bearerToken, scopeAdmin},
		{s.adminToken, scopeAdmin},
	}
	for _, candidate := range candidates {
		if candidate.token != "" && subtle.ConstantTimeCompare([]byte(token), []byte(candidate.token)) == 1 {
			return candidate.scope, true
		}
	}
	return scopePublic, false
}

func (s *Server) cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin == "" {
			next.ServeHTTP(w, r)
			return
		}
		if !slices.Contains(s.allowedOrigins, origin) {
			writeError(w, http.StatusForbidden, "origin_forbidden", "origin is not allowed")
			return
		}
		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Set("Vary", "Origin")
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, Last-Event-ID")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PATCH, DELETE, OPTIONS")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		r.Body = http.MaxBytesReader(w, r.Body, maxRequestBody)
		next.ServeHTTP(w, r)
	})
}

func (s *Server) accessLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		next.ServeHTTP(w, r)
		s.logger.Info("http request", "method", r.Method, "path", r.URL.Path, "duration", time.Since(started))
	})
}

func (s *Server) recoverPanic(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if recovered := recover(); recovered != nil {
				s.logger.Error("panic serving request", "panic", recovered, "stack", string(debug.Stack()))
				writeError(w, http.StatusInternalServerError, "internal_error", "internal server error")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		panic(fmt.Errorf("encode response: %w", err))
	}
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": message}})
}
