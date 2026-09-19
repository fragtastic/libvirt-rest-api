package hypervisor

import (
	"context"
	"encoding/xml"
	"errors"
	"testing"
	"time"

	"libvirt.org/go/libvirt"
)

func TestSnapshotCreateXMLDoesNotIncludeUserControlledStructure(t *testing.T) {
	definition, err := xml.Marshal(snapshotCreateXML{Name: `safe-name`, Description: `<disk name="vda"/>`})
	if err != nil {
		t.Fatal(err)
	}
	want := `<domainsnapshot><name>safe-name</name><description>&lt;disk name=&#34;vda&#34;/&gt;</description></domainsnapshot>`
	if string(definition) != want {
		t.Fatalf("definition = %s, want %s", definition, want)
	}
}

func TestCanceledSnapshotActionStopsWhileWaitingForDomainLock(t *testing.T) {
	service := &Libvirt{}
	release := service.lockDomainAction("uuid")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	result := make(chan error, 1)
	go func() {
		unlock, err := service.lockSnapshotAction(ctx, "uuid")
		if unlock != nil {
			unlock()
		}
		result <- err
	}()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("error = %v, want context cancellation", err)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("canceled action remained blocked on the domain lock")
	}
	release()
}

func TestSnapshotOperationErrorClassification(t *testing.T) {
	if err := snapshotLookupError("vm", "missing", libvirt.ERR_NO_DOMAIN_SNAPSHOT); !errors.Is(err, ErrSnapshotNotFound) {
		t.Fatalf("lookup error = %v", err)
	}
	if err := snapshotOperationError("create", "vm", "snap", libvirt.ERR_OPERATION_INVALID); !errors.Is(err, ErrConflict) {
		t.Fatalf("operation error = %v", err)
	}
}

func TestSnapshotFlagSelection(t *testing.T) {
	create := snapshotCreateFlags(SnapshotCreateRequest{Kind: SnapshotKindDisk, Quiesce: true})
	wantCreate := libvirt.DOMAIN_SNAPSHOT_CREATE_ATOMIC | libvirt.DOMAIN_SNAPSHOT_CREATE_DISK_ONLY | libvirt.DOMAIN_SNAPSHOT_CREATE_QUIESCE
	if create != wantCreate {
		t.Fatalf("create flags = %d, want %d", create, wantCreate)
	}
	if flags := snapshotCreateFlags(SnapshotCreateRequest{Kind: SnapshotKindSystem}); flags != libvirt.DOMAIN_SNAPSHOT_CREATE_ATOMIC {
		t.Fatalf("system create flags = %d", flags)
	}

	revert := snapshotRevertFlags(SnapshotRevertRequest{State: SnapshotRevertPaused, Force: true})
	wantRevert := libvirt.DOMAIN_SNAPSHOT_REVERT_PAUSED | libvirt.DOMAIN_SNAPSHOT_REVERT_FORCE
	if revert != wantRevert {
		t.Fatalf("revert flags = %d, want %d", revert, wantRevert)
	}
	if flags := snapshotRevertFlags(SnapshotRevertRequest{State: SnapshotRevertRecorded}); flags != 0 {
		t.Fatalf("recorded-state revert flags = %d", flags)
	}

	if flags := snapshotDeleteFlags(true); flags != libvirt.DOMAIN_SNAPSHOT_DELETE_CHILDREN {
		t.Fatalf("recursive delete flags = %d", flags)
	}
	if flags := snapshotDeleteFlags(false); flags != 0 {
		t.Fatalf("single delete flags = %d", flags)
	}
}

func TestParseSnapshotDefinition(t *testing.T) {
	snapshot, err := parseSnapshotDefinition(`<domainsnapshot><name>clean</name><description>before update</description><state>shutoff</state><creationTime>1700000000</creationTime><parent><name>base</name></parent></domainsnapshot>`, "clean")
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Name != "clean" || snapshot.Parent != "base" || !snapshot.Current || snapshot.CreatedAt == nil || snapshot.CreatedAt.Unix() != 1_700_000_000 {
		t.Fatalf("snapshot = %+v", snapshot)
	}
}
