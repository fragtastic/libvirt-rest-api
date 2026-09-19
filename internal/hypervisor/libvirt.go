package hypervisor

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"math"
	"strconv"
	"sync"

	"libvirt.org/go/libvirt"
)

type Libvirt struct {
	connection  *libvirt.Connect
	locksMu     sync.Mutex
	actionLocks map[string]*domainActionLock
}

type domainActionLock struct {
	mutex sync.Mutex
	users int
}

func Connect(uri string) (*Libvirt, error) {
	connection, err := libvirt.NewConnect(uri)
	if err != nil {
		return nil, fmt.Errorf("connect to libvirt: %w", err)
	}
	return &Libvirt{connection: connection}, nil
}

func (l *Libvirt) Close() error {
	_, err := l.connection.Close()
	if err != nil {
		return fmt.Errorf("close libvirt connection: %w", err)
	}
	return nil
}

func (l *Libvirt) Host(context.Context) (Host, error) {
	name, err := l.connection.GetHostname()
	if err != nil {
		return Host{}, fmt.Errorf("get hypervisor hostname: %w", err)
	}
	info, err := l.connection.GetNodeInfo()
	if err != nil {
		return Host{}, fmt.Errorf("get hypervisor information: %w", err)
	}
	return Host{
		Name: name, Model: info.Model, MemoryKiB: info.Memory, CPUs: info.Cpus,
		MHz: info.MHz, NUMANodes: uint(info.Nodes), Sockets: uint(info.Sockets),
		Cores: uint(info.Cores), Threads: uint(info.Threads),
	}, nil
}

func (l *Libvirt) ListDomains(_ context.Context, filter DomainFilter) ([]Domain, error) {
	flags := libvirt.ConnectListAllDomainsFlags(0)
	switch filter {
	case DomainActive:
		flags = libvirt.CONNECT_LIST_DOMAINS_ACTIVE
	case DomainInactive:
		flags = libvirt.CONNECT_LIST_DOMAINS_INACTIVE
	}
	domains, err := l.connection.ListAllDomains(flags)
	if err != nil {
		return nil, fmt.Errorf("list domains: %w", err)
	}
	defer func() {
		for i := range domains {
			_ = domains[i].Free()
		}
	}()
	result := make([]Domain, 0, len(domains))
	for i := range domains {
		domain, itemErr := domainSummary(&domains[i])
		if itemErr != nil {
			return nil, itemErr
		}
		result = append(result, domain)
	}
	return result, nil
}

func (l *Libvirt) Domain(_ context.Context, identifier string) (DomainInfo, error) {
	domain, err := l.lookup(identifier)
	if err != nil {
		return DomainInfo{}, err
	}
	defer domain.Free()

	summary, err := domainSummary(domain)
	if err != nil {
		return DomainInfo{}, err
	}
	info, err := domain.GetInfo()
	if err != nil {
		return DomainInfo{}, fmt.Errorf("get domain %q information: %w", identifier, err)
	}
	return DomainInfo{
		Domain: summary, StateCode: int(info.State), MaxMemoryKiB: info.MaxMem,
		MemoryKiB: info.Memory, VCPUs: info.NrVirtCpu, CPUTimeNS: info.CpuTime,
	}, nil
}

func (l *Libvirt) DomainXML(_ context.Context, identifier string) (string, error) {
	domain, err := l.lookup(identifier)
	if err != nil {
		return "", err
	}
	defer domain.Free()
	description, err := domain.GetXMLDesc(0)
	if err != nil {
		return "", fmt.Errorf("get domain %q XML: %w", identifier, err)
	}
	return description, nil
}

func (l *Libvirt) Viewer(ctx context.Context, identifier string) (Viewer, error) {
	description, err := l.DomainXML(ctx, identifier)
	if err != nil {
		return Viewer{}, err
	}
	viewer, err := viewerFromXML(description)
	if err != nil {
		return Viewer{}, fmt.Errorf("parse domain %q XML: %w", identifier, err)
	}
	return viewer, nil
}

func viewerFromXML(description string) (Viewer, error) {
	var document struct {
		Devices struct {
			Graphics []struct {
				Type     string `xml:"type,attr"`
				Listen   string `xml:"listen,attr"`
				Port     string `xml:"port,attr"`
				Listener struct {
					Address string `xml:"address,attr"`
				} `xml:"listen"`
			} `xml:"graphics"`
		} `xml:"devices"`
	}
	if err := xml.Unmarshal([]byte(description), &document); err != nil {
		return Viewer{}, err
	}
	if len(document.Devices.Graphics) == 0 {
		return Viewer{}, nil
	}
	graphics := document.Devices.Graphics[0]
	viewer := Viewer{Type: graphics.Type}
	if graphics.Listen == "" {
		graphics.Listen = graphics.Listener.Address
	}
	if graphics.Listen != "" {
		viewer.Listen = &graphics.Listen
	}
	if graphics.Port != "" && graphics.Port != "-1" {
		port, err := strconv.Atoi(graphics.Port)
		if err != nil {
			return Viewer{}, fmt.Errorf("parse graphics port: %w", err)
		}
		viewer.Port = &port
	}
	return viewer, nil
}

func (l *Libvirt) Screenshot(ctx context.Context, identifier string) (Screenshot, error) {
	domain, err := l.lookup(identifier)
	if err != nil {
		return Screenshot{}, err
	}
	stream, err := l.connection.NewStream(0)
	if err != nil {
		domain.Free()
		return Screenshot{}, fmt.Errorf("create screenshot stream: %w", err)
	}
	mimeType, err := domain.Screenshot(stream, 0, 0)
	if err != nil {
		stream.Free()
		domain.Free()
		return Screenshot{}, fmt.Errorf("capture domain %q screenshot: %w", identifier, err)
	}
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}

	reader, writer := io.Pipe()
	go func() {
		defer stream.Free()
		defer domain.Free()
		done := make(chan error, 1)
		go func() {
			done <- stream.RecvAll(func(_ *libvirt.Stream, data []byte) (int, error) {
				return writer.Write(data)
			})
		}()
		select {
		case err := <-done:
			if err == nil {
				err = stream.Finish()
			} else {
				_ = stream.Abort()
			}
			_ = writer.CloseWithError(err)
		case <-ctx.Done():
			_ = stream.Abort()
			<-done
			_ = writer.CloseWithError(ctx.Err())
		}
	}()
	return Screenshot{ContentType: mimeType, Data: reader}, nil
}

func (l *Libvirt) Start(_ context.Context, identifier string) (ActionResult, error) {
	domain, err := l.lookup(identifier)
	if err != nil {
		return ActionResult{}, err
	}
	defer domain.Free()
	name, uuid, err := domainIdentity(domain)
	if err != nil {
		return ActionResult{}, err
	}
	unlock := l.lockDomainAction(uuid)
	defer unlock()
	active, err := domain.IsActive()
	if err != nil {
		return ActionResult{}, fmt.Errorf("get domain %q activity: %w", name, err)
	}
	if active {
		return ActionResult{}, fmt.Errorf("%w: domain %q is already running", ErrConflict, name)
	}
	if err := domain.Create(); err != nil {
		if active, stateErr := domain.IsActive(); stateErr == nil && active {
			return ActionResult{}, fmt.Errorf("%w: domain %q is already running", ErrConflict, name)
		}
		return ActionResult{}, fmt.Errorf("start domain %q: %w", name, err)
	}
	return ActionResult{Name: name, UUID: uuid, State: "running"}, nil
}

func (l *Libvirt) Stop(_ context.Context, identifier string) (ActionResult, error) {
	domain, err := l.lookup(identifier)
	if err != nil {
		return ActionResult{}, err
	}
	defer domain.Free()
	name, uuid, err := domainIdentity(domain)
	if err != nil {
		return ActionResult{}, err
	}
	unlock := l.lockDomainAction(uuid)
	defer unlock()
	active, err := domain.IsActive()
	if err != nil {
		return ActionResult{}, fmt.Errorf("get domain %q activity: %w", name, err)
	}
	if !active {
		return ActionResult{}, fmt.Errorf("%w: domain %q is already stopped", ErrConflict, name)
	}
	if err := domain.Destroy(); err != nil {
		if active, stateErr := domain.IsActive(); stateErr == nil && !active {
			return ActionResult{}, fmt.Errorf("%w: domain %q is already stopped", ErrConflict, name)
		}
		return ActionResult{}, fmt.Errorf("stop domain %q: %w", name, err)
	}
	return ActionResult{Name: name, UUID: uuid, State: "shutoff"}, nil
}

func (l *Libvirt) lockDomainAction(name string) func() {
	l.locksMu.Lock()
	if l.actionLocks == nil {
		l.actionLocks = make(map[string]*domainActionLock)
	}
	lock := l.actionLocks[name]
	if lock == nil {
		lock = &domainActionLock{}
		l.actionLocks[name] = lock
	}
	lock.users++
	l.locksMu.Unlock()

	lock.mutex.Lock()
	return func() {
		lock.mutex.Unlock()
		l.locksMu.Lock()
		lock.users--
		if lock.users == 0 {
			delete(l.actionLocks, name)
		}
		l.locksMu.Unlock()
	}
}

func (l *Libvirt) lookup(identifier string) (*libvirt.Domain, error) {
	return lookupDomain(identifier, l.connection.LookupDomainByName, l.connection.LookupDomainByUUIDString)
}

type domainLookup func(string) (*libvirt.Domain, error)

func lookupDomain(identifier string, byName, byUUID domainLookup) (*libvirt.Domain, error) {
	domain, err := byName(identifier)
	if err == nil {
		return domain, nil
	}
	if !errors.Is(err, libvirt.ERR_NO_DOMAIN) {
		return nil, fmt.Errorf("look up domain name %q: %w", identifier, err)
	}
	if isCanonicalUUID(identifier) {
		domain, err = byUUID(identifier)
		if err == nil {
			return domain, nil
		}
		if !errors.Is(err, libvirt.ERR_NO_DOMAIN) {
			return nil, fmt.Errorf("look up domain UUID %q: %w", identifier, err)
		}
	}
	if errors.Is(err, libvirt.ERR_NO_DOMAIN) {
		return nil, fmt.Errorf("%w: %q", ErrNotFound, identifier)
	}
	return nil, fmt.Errorf("look up domain %q: %w", identifier, err)
}

func isCanonicalUUID(value string) bool {
	if len(value) != 36 {
		return false
	}
	for index, char := range value {
		switch index {
		case 8, 13, 18, 23:
			if char != '-' {
				return false
			}
		default:
			if !((char >= '0' && char <= '9') || (char >= 'a' && char <= 'f') || (char >= 'A' && char <= 'F')) {
				return false
			}
		}
	}
	return true
}

func domainIdentity(domain *libvirt.Domain) (string, string, error) {
	name, err := domain.GetName()
	if err != nil {
		return "", "", fmt.Errorf("get domain name: %w", err)
	}
	uuid, err := domain.GetUUIDString()
	if err != nil {
		return "", "", fmt.Errorf("get domain %q UUID: %w", name, err)
	}
	return name, uuid, nil
}

func domainSummary(domain *libvirt.Domain) (Domain, error) {
	name, uuid, err := domainIdentity(domain)
	if err != nil {
		return Domain{}, err
	}
	state, _, err := domain.GetState()
	if err != nil {
		return Domain{}, fmt.Errorf("get domain %q state: %w", name, err)
	}
	active, err := domain.IsActive()
	if err != nil {
		return Domain{}, fmt.Errorf("get domain %q activity: %w", name, err)
	}
	numericID := -1
	if active {
		id, err := domain.GetID()
		if err != nil {
			return Domain{}, fmt.Errorf("get domain %q ID: %w", name, err)
		}
		numericID = normalizeDomainID(id)
	}
	return Domain{Name: name, UUID: uuid, ID: numericID, State: StateName(int(state)), Active: active}, nil
}

func normalizeDomainID(id uint) int {
	if id == math.MaxUint32 {
		return -1
	}
	return int(id)
}
