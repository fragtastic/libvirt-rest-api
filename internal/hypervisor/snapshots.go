package hypervisor

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"slices"
	"time"

	"libvirt.org/go/libvirt"
)

type snapshotXML struct {
	XMLName      xml.Name          `xml:"domainsnapshot"`
	Name         string            `xml:"name"`
	Description  string            `xml:"description,omitempty"`
	State        string            `xml:"state,omitempty"`
	CreationTime int64             `xml:"creationTime,omitempty"`
	Parent       snapshotParentXML `xml:"parent"`
}

type snapshotCreateXML struct {
	XMLName     xml.Name `xml:"domainsnapshot"`
	Name        string   `xml:"name"`
	Description string   `xml:"description,omitempty"`
}

type snapshotParentXML struct {
	Name string `xml:"name,omitempty"`
}

func (l *Libvirt) ListSnapshots(_ context.Context, identifier string) ([]Snapshot, error) {
	domain, err := l.lookup(identifier)
	if err != nil {
		return nil, err
	}
	defer domain.Free()

	currentName := ""
	current, err := domain.SnapshotCurrent(0)
	if err == nil {
		currentName, err = current.GetName()
		_ = current.Free()
		if err != nil {
			return nil, fmt.Errorf("get current snapshot name for domain %q: %w", identifier, err)
		}
	} else if !errors.Is(err, libvirt.ERR_NO_DOMAIN_SNAPSHOT) {
		return nil, fmt.Errorf("get current snapshot for domain %q: %w", identifier, err)
	}

	items, err := domain.ListAllSnapshots(0)
	if err != nil {
		return nil, fmt.Errorf("list snapshots for domain %q: %w", identifier, err)
	}
	defer func() {
		for index := range items {
			_ = items[index].Free()
		}
	}()

	result := make([]Snapshot, 0, len(items))
	for index := range items {
		snapshot, err := snapshotMetadata(&items[index], currentName)
		if err != nil {
			return nil, err
		}
		result = append(result, snapshot)
	}
	slices.SortFunc(result, func(left, right Snapshot) int {
		if left.CreatedAt != nil && right.CreatedAt != nil && !left.CreatedAt.Equal(*right.CreatedAt) {
			if left.CreatedAt.Before(*right.CreatedAt) {
				return -1
			}
			return 1
		}
		if left.Name < right.Name {
			return -1
		}
		if left.Name > right.Name {
			return 1
		}
		return 0
	})
	return result, nil
}

func (l *Libvirt) CreateSnapshot(ctx context.Context, identifier string, request SnapshotCreateRequest) (Snapshot, error) {
	domain, err := l.lookup(identifier)
	if err != nil {
		return Snapshot{}, err
	}
	defer domain.Free()
	name, uuid, err := domainIdentity(domain)
	if err != nil {
		return Snapshot{}, err
	}
	unlock, err := l.lockSnapshotAction(ctx, uuid)
	if err != nil {
		return Snapshot{}, fmt.Errorf("create snapshot for domain %q: %w", name, err)
	}
	defer unlock()

	description, err := xml.Marshal(snapshotCreateXML{Name: request.Name, Description: request.Description})
	if err != nil {
		return Snapshot{}, fmt.Errorf("encode snapshot definition: %w", err)
	}
	flags := snapshotCreateFlags(request)
	if err := ctx.Err(); err != nil {
		return Snapshot{}, fmt.Errorf("create snapshot for domain %q: %w", name, err)
	}
	created, err := domain.CreateSnapshotXML(string(description), flags)
	if err != nil {
		return Snapshot{}, snapshotOperationError("create", name, request.Name, err)
	}
	defer created.Free()
	return snapshotMetadata(created, request.Name)
}

func (l *Libvirt) RevertSnapshot(ctx context.Context, identifier, snapshotName string, request SnapshotRevertRequest) (SnapshotActionResult, error) {
	domain, err := l.lookup(identifier)
	if err != nil {
		return SnapshotActionResult{}, err
	}
	defer domain.Free()
	name, uuid, err := domainIdentity(domain)
	if err != nil {
		return SnapshotActionResult{}, err
	}
	unlock, err := l.lockSnapshotAction(ctx, uuid)
	if err != nil {
		return SnapshotActionResult{}, fmt.Errorf("revert snapshot for domain %q: %w", name, err)
	}
	defer unlock()

	snapshot, err := domain.SnapshotLookupByName(snapshotName, 0)
	if err != nil {
		return SnapshotActionResult{}, snapshotLookupError(name, snapshotName, err)
	}
	defer snapshot.Free()
	flags := snapshotRevertFlags(request)
	if err := ctx.Err(); err != nil {
		return SnapshotActionResult{}, fmt.Errorf("revert snapshot for domain %q: %w", name, err)
	}
	if err := snapshot.RevertToSnapshot(flags); err != nil {
		return SnapshotActionResult{}, snapshotOperationError("revert to", name, snapshotName, err)
	}
	return SnapshotActionResult{Name: name, UUID: uuid, Snapshot: snapshotName, Status: "reverted"}, nil
}

func (l *Libvirt) DeleteSnapshot(ctx context.Context, identifier, snapshotName string, children bool) error {
	domain, err := l.lookup(identifier)
	if err != nil {
		return err
	}
	defer domain.Free()
	name, uuid, err := domainIdentity(domain)
	if err != nil {
		return err
	}
	unlock, err := l.lockSnapshotAction(ctx, uuid)
	if err != nil {
		return fmt.Errorf("delete snapshot for domain %q: %w", name, err)
	}
	defer unlock()

	snapshot, err := domain.SnapshotLookupByName(snapshotName, 0)
	if err != nil {
		return snapshotLookupError(name, snapshotName, err)
	}
	defer snapshot.Free()
	flags := snapshotDeleteFlags(children)
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("delete snapshot for domain %q: %w", name, err)
	}
	if err := snapshot.Delete(flags); err != nil {
		return snapshotOperationError("delete", name, snapshotName, err)
	}
	return nil
}

func (l *Libvirt) lockSnapshotAction(ctx context.Context, uuid string) (func(), error) {
	return l.lockDomainActionContext(ctx, uuid)
}

func snapshotCreateFlags(request SnapshotCreateRequest) libvirt.DomainSnapshotCreateFlags {
	flags := libvirt.DOMAIN_SNAPSHOT_CREATE_ATOMIC
	if request.Kind == SnapshotKindDisk {
		flags |= libvirt.DOMAIN_SNAPSHOT_CREATE_DISK_ONLY
	}
	if request.Quiesce {
		flags |= libvirt.DOMAIN_SNAPSHOT_CREATE_QUIESCE
	}
	return flags
}

func snapshotRevertFlags(request SnapshotRevertRequest) libvirt.DomainSnapshotRevertFlags {
	flags := libvirt.DomainSnapshotRevertFlags(0)
	switch request.State {
	case SnapshotRevertRunning:
		flags |= libvirt.DOMAIN_SNAPSHOT_REVERT_RUNNING
	case SnapshotRevertPaused:
		flags |= libvirt.DOMAIN_SNAPSHOT_REVERT_PAUSED
	}
	if request.Force {
		flags |= libvirt.DOMAIN_SNAPSHOT_REVERT_FORCE
	}
	return flags
}

func snapshotDeleteFlags(children bool) libvirt.DomainSnapshotDeleteFlags {
	if children {
		return libvirt.DOMAIN_SNAPSHOT_DELETE_CHILDREN
	}
	return 0
}

func snapshotMetadata(snapshot *libvirt.DomainSnapshot, currentName string) (Snapshot, error) {
	definition, err := snapshot.GetXMLDesc(0)
	if err != nil {
		return Snapshot{}, fmt.Errorf("get snapshot XML: %w", err)
	}
	return parseSnapshotDefinition(definition, currentName)
}

func parseSnapshotDefinition(definition, currentName string) (Snapshot, error) {
	var parsed snapshotXML
	if err := xml.Unmarshal([]byte(definition), &parsed); err != nil {
		return Snapshot{}, fmt.Errorf("decode snapshot XML: %w", err)
	}
	result := Snapshot{Name: parsed.Name, Description: parsed.Description, Parent: parsed.Parent.Name, State: parsed.State, Current: parsed.Name == currentName}
	if parsed.CreationTime > 0 {
		createdAt := time.Unix(parsed.CreationTime, 0).UTC()
		result.CreatedAt = &createdAt
	}
	return result, nil
}

func snapshotLookupError(domainName, snapshotName string, err error) error {
	if errors.Is(err, libvirt.ERR_NO_DOMAIN_SNAPSHOT) {
		return fmt.Errorf("%w: snapshot %q for domain %q", ErrSnapshotNotFound, snapshotName, domainName)
	}
	return fmt.Errorf("look up snapshot %q for domain %q: %w", snapshotName, domainName, err)
}

func snapshotOperationError(operation, domainName, snapshotName string, err error) error {
	switch {
	case errors.Is(err, libvirt.ERR_OPERATION_INVALID):
		return fmt.Errorf("%w: cannot %s snapshot %q for domain %q", ErrConflict, operation, snapshotName, domainName)
	case errors.Is(err, libvirt.ERR_NO_SUPPORT), errors.Is(err, libvirt.ERR_OPERATION_UNSUPPORTED), errors.Is(err, libvirt.ERR_CONFIG_UNSUPPORTED):
		return fmt.Errorf("%w: cannot %s snapshot %q for domain %q", ErrUnsupported, operation, snapshotName, domainName)
	default:
		return fmt.Errorf("%s snapshot %q for domain %q: %w", operation, snapshotName, domainName, err)
	}
}
