package hypervisor

import (
	"context"
	"errors"
	"io"
	"time"
)

var (
	ErrNotFound         = errors.New("domain not found")
	ErrSnapshotNotFound = errors.New("snapshot not found")
	ErrConflict         = errors.New("domain state conflict")
	ErrUnsupported      = errors.New("operation unsupported")
)

type DomainFilter uint32

type PowerMode string
type InterfaceAddressSource string

const (
	PowerModeDefault    PowerMode = "default"
	PowerModeACPI       PowerMode = "acpi"
	PowerModeGuestAgent PowerMode = "guest-agent"
)

const (
	InterfaceSourceLease InterfaceAddressSource = "lease"
	InterfaceSourceAgent InterfaceAddressSource = "agent"
	InterfaceSourceARP   InterfaceAddressSource = "arp"
)

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

type CPUStats struct {
	CPUTimeNS    *uint64 `json:"cpu_time_ns,omitempty"`
	UserTimeNS   *uint64 `json:"user_time_ns,omitempty"`
	SystemTimeNS *uint64 `json:"system_time_ns,omitempty"`
}
type HostStats struct {
	SampledAt time.Time         `json:"sampled_at"`
	CPU       CPUStats          `json:"cpu"`
	MemoryKiB map[string]uint64 `json:"memory_kib"`
}
type BlockStats struct {
	Device        string `json:"device"`
	ReadBytes     *int64 `json:"read_bytes,omitempty"`
	ReadRequests  *int64 `json:"read_requests,omitempty"`
	WriteBytes    *int64 `json:"write_bytes,omitempty"`
	WriteRequests *int64 `json:"write_requests,omitempty"`
}
type NetworkStats struct {
	Device    string `json:"device"`
	RXBytes   *int64 `json:"rx_bytes,omitempty"`
	RXPackets *int64 `json:"rx_packets,omitempty"`
	TXBytes   *int64 `json:"tx_bytes,omitempty"`
	TXPackets *int64 `json:"tx_packets,omitempty"`
}
type DomainStats struct {
	SampledAt  time.Time         `json:"sampled_at"`
	Name       string            `json:"name"`
	UUID       string            `json:"uuid"`
	State      string            `json:"state"`
	CPU        CPUStats          `json:"cpu"`
	MemoryKiB  map[string]uint64 `json:"memory_kib"`
	Disks      []BlockStats      `json:"disks"`
	Interfaces []NetworkStats    `json:"interfaces"`
}
type IPAddress struct {
	Family  string `json:"family"`
	Address string `json:"address"`
	Prefix  uint   `json:"prefix"`
}
type DomainInterface struct {
	Name      string      `json:"name"`
	MAC       string      `json:"mac"`
	Addresses []IPAddress `json:"addresses"`
}
type DomainInterfaces struct {
	Name       string                 `json:"name"`
	UUID       string                 `json:"uuid"`
	Source     InterfaceAddressSource `json:"source"`
	Interfaces []DomainInterface      `json:"interfaces"`
}
type Autostart struct {
	Name    string `json:"name"`
	UUID    string `json:"uuid"`
	Enabled bool   `json:"enabled"`
}

type SnapshotKind string

const (
	SnapshotKindSystem SnapshotKind = "system"
	SnapshotKindDisk   SnapshotKind = "disk"
)

type Snapshot struct {
	Name        string     `json:"name"`
	Description string     `json:"description,omitempty"`
	Parent      string     `json:"parent,omitempty"`
	State       string     `json:"state,omitempty"`
	CreatedAt   *time.Time `json:"created_at,omitempty"`
	Current     bool       `json:"current"`
}

type SnapshotCreateRequest struct {
	Name        string
	Description string
	Kind        SnapshotKind
	Quiesce     bool
}

type SnapshotRevertState string

const (
	SnapshotRevertRecorded SnapshotRevertState = "snapshot"
	SnapshotRevertRunning  SnapshotRevertState = "running"
	SnapshotRevertPaused   SnapshotRevertState = "paused"
)

type SnapshotRevertRequest struct {
	State SnapshotRevertState
	Force bool
}

type SnapshotActionResult struct {
	Name     string `json:"name"`
	UUID     string `json:"uuid"`
	Snapshot string `json:"snapshot"`
	Status   string `json:"status"`
}

type Service interface {
	Ready(context.Context) error
	Host(context.Context) (Host, error)
	HostStats(context.Context) (HostStats, error)
	ListDomains(context.Context, DomainFilter) ([]Domain, error)
	Domain(context.Context, string) (DomainInfo, error)
	DomainStats(context.Context, string) (DomainStats, error)
	DomainInterfaces(context.Context, string, InterfaceAddressSource) (DomainInterfaces, error)
	Autostart(context.Context, string) (Autostart, error)
	SetAutostart(context.Context, string, bool) (Autostart, error)
	ListSnapshots(context.Context, string) ([]Snapshot, error)
	CreateSnapshot(context.Context, string, SnapshotCreateRequest) (Snapshot, error)
	RevertSnapshot(context.Context, string, string, SnapshotRevertRequest) (SnapshotActionResult, error)
	DeleteSnapshot(context.Context, string, string, bool) error
	Subscribe(context.Context, string) Subscription
	DomainXML(context.Context, string) (string, error)
	Viewer(context.Context, string) (Viewer, error)
	Screenshot(context.Context, string) (Screenshot, error)
	Start(context.Context, string) (ActionResult, error)
	Shutdown(context.Context, string, PowerMode) (ActionResult, error)
	Reboot(context.Context, string, PowerMode) (ActionResult, error)
	Pause(context.Context, string) (ActionResult, error)
	Resume(context.Context, string) (ActionResult, error)
	Reset(context.Context, string) (ActionResult, error)
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
