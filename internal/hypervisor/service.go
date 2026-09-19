package hypervisor

import (
	"context"
	"errors"
	"io"
)

var (
	ErrNotFound    = errors.New("domain not found")
	ErrConflict    = errors.New("domain state conflict")
	ErrUnsupported = errors.New("operation unsupported")
)

type DomainFilter uint32

const (
	DomainAll DomainFilter = iota
	DomainActive
	DomainInactive
)

type Host struct {
	Name      string `json:"name"`
	Model     string `json:"model"`
	MemoryKiB uint64 `json:"memory_kib"`
	CPUs      uint   `json:"cpus"`
	MHz       uint   `json:"mhz"`
	NUMANodes uint   `json:"numa_nodes"`
	Sockets   uint   `json:"sockets"`
	Cores     uint   `json:"cores"`
	Threads   uint   `json:"threads"`
}

type Domain struct {
	Name   string `json:"name"`
	UUID   string `json:"uuid"`
	ID     int    `json:"id"`
	State  string `json:"state"`
	Active bool   `json:"active"`
}

type DomainInfo struct {
	Domain
	StateCode    int    `json:"state_code"`
	MaxMemoryKiB uint64 `json:"max_memory_kib"`
	MemoryKiB    uint64 `json:"memory_kib"`
	VCPUs        uint   `json:"vcpus"`
	CPUTimeNS    uint64 `json:"cpu_time_ns"`
}

type Viewer struct {
	Type   string  `json:"type,omitempty"`
	Listen *string `json:"listen"`
	Port   *int    `json:"port"`
}

type ActionResult struct {
	Name   string `json:"name"`
	UUID   string `json:"uuid"`
	State  string `json:"state"`
	Status string `json:"status,omitempty"`
}

type Screenshot struct {
	ContentType string
	Data        io.ReadCloser
}

type Service interface {
	Host(context.Context) (Host, error)
	ListDomains(context.Context, DomainFilter) ([]Domain, error)
	Domain(context.Context, string) (DomainInfo, error)
	DomainXML(context.Context, string) (string, error)
	Viewer(context.Context, string) (Viewer, error)
	Screenshot(context.Context, string) (Screenshot, error)
	Start(context.Context, string) (ActionResult, error)
	Shutdown(context.Context, string) (ActionResult, error)
	Stop(context.Context, string) (ActionResult, error)
	Close() error
}

func StateName(code int) string {
	names := [...]string{
		"no-state", "running", "blocked", "paused", "shutdown",
		"shutoff", "crashed", "suspended",
	}
	if code < 0 || code >= len(names) {
		return "unknown"
	}
	return names[code]
}
