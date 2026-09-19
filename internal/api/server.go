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
	"strings"
	"time"

	"github.com/fragtastic/libvirt-rest-api/internal/hypervisor"
)

const maxRequestBody = 1 << 20

type Options struct {
	Logger         *slog.Logger
	BearerToken    string
	AllowedOrigins []string
}

type Server struct {
	hypervisor     hypervisor.Service
	logger         *slog.Logger
	bearerToken    string
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
		allowedOrigins: append([]string(nil), options.AllowedOrigins...),
	}

	mux := http.NewServeMux()
	routes := newRouteRegistry(mux)
	routes.handle(http.MethodGet, "/healthz", s.health)
	routes.handle(http.MethodGet, "/api/v1/host", s.host)
	routes.handle(http.MethodGet, "/api/v1/vms", s.listVMs)
	routes.handle(http.MethodGet, "/api/v1/vms/{identifier}", s.vm)
	routes.handle(http.MethodGet, "/api/v1/vms/{identifier}/xml", s.vmXML)
	routes.handle(http.MethodGet, "/api/v1/vms/{identifier}/viewer", s.vmViewer)
	routes.handle(http.MethodGet, "/api/v1/vms/{identifier}/screenshot", s.vmScreenshot)
	routes.handle(http.MethodPost, "/api/v1/vms/{identifier}/actions/start", s.vmStart)
	routes.handle(http.MethodPost, "/api/v1/vms/{identifier}/actions/stop", s.vmStop)
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		writeError(w, http.StatusNotFound, "not_found", "endpoint not found")
	})

	return s.recoverPanic(s.accessLog(s.securityHeaders(s.cors(s.authenticate(mux)))))
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

func (s *Server) host(w http.ResponseWriter, r *http.Request) {
	host, err := s.hypervisor.Host(r.Context())
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, host)
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

func (s *Server) vm(w http.ResponseWriter, r *http.Request) {
	vm, err := s.hypervisor.Domain(r.Context(), r.PathValue("identifier"))
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, vm)
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
	s.runAction(w, r, s.hypervisor.Start)
}

func (s *Server) vmStop(w http.ResponseWriter, r *http.Request) {
	s.runAction(w, r, s.hypervisor.Stop)
}

func (s *Server) runAction(w http.ResponseWriter, r *http.Request, action func(context.Context, string) (hypervisor.ActionResult, error)) {
	identifier := r.PathValue("identifier")
	result, err := action(r.Context(), identifier)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
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

func (s *Server) writeServiceError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, hypervisor.ErrNotFound):
		writeError(w, http.StatusNotFound, "vm_not_found", "virtual machine not found")
	case errors.Is(err, hypervisor.ErrConflict):
		writeError(w, http.StatusConflict, "state_conflict", err.Error())
	default:
		s.logger.Error("hypervisor request failed", "error", err)
		writeError(w, http.StatusServiceUnavailable, "hypervisor_unavailable", "hypervisor request failed")
	}
}

func (s *Server) authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" || s.bearerToken == "" {
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
		if subtle.ConstantTimeCompare([]byte(provided), []byte(s.bearerToken)) != 1 {
			w.Header().Set("WWW-Authenticate", `Bearer realm="virt-rest-api"`)
			writeError(w, http.StatusUnauthorized, "unauthorized", "a valid bearer token is required")
			return
		}
		next.ServeHTTP(w, r)
	})
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
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
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
