package hypervisor

import (
	"errors"
	"testing"
	"time"

	"libvirt.org/go/libvirt"
)

func TestStateName(t *testing.T) {
	tests := map[int]string{-1: "unknown", 0: "no-state", 1: "running", 5: "shutoff", 7: "suspended", 8: "unknown"}
	for state, want := range tests {
		if got := StateName(state); got != want {
			t.Errorf("StateName(%d) = %q, want %q", state, got, want)
		}
	}
}

func TestViewerFromXML(t *testing.T) {
	tests := []struct {
		name       string
		xml        string
		wantListen string
		wantPort   int
	}{
		{"graphics attributes", `<domain><devices><graphics type="spice" listen="0.0.0.0" port="5901"/></devices></domain>`, "0.0.0.0", 5901},
		{"nested listener", `<domain><devices><graphics type="vnc" port="5902"><listen type="address" address="127.0.0.1"/></graphics></devices></domain>`, "127.0.0.1", 5902},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			viewer, err := viewerFromXML(test.xml)
			if err != nil {
				t.Fatalf("viewerFromXML() error = %v", err)
			}
			if viewer.Listen == nil || *viewer.Listen != test.wantListen || viewer.Port == nil || *viewer.Port != test.wantPort {
				t.Fatalf("viewerFromXML() = %#v", viewer)
			}
		})
	}
}

func TestNormalizeDomainID(t *testing.T) {
	if got := normalizeDomainID(42); got != 42 {
		t.Fatalf("normalizeDomainID(42) = %d", got)
	}
	if got := normalizeDomainID(^uint(0) & 0xffffffff); got != -1 {
		t.Fatalf("normalizeDomainID(UINT_MAX) = %d", got)
	}
}

func TestIsCanonicalUUID(t *testing.T) {
	tests := map[string]bool{
		"52d7a2fe-1942-4a89-93d9-9a57d8f67b6d": true,
		"52D7A2FE-1942-4A89-93D9-9A57D8F67B6D": true,
		"win11":                                false,
		"52d7a2fe19424a8993d99a57d8f67b6d":     false,
		"52d7a2fe-1942-4a89-93d9-9a57d8f67b6x": false,
	}
	for value, want := range tests {
		if got := isCanonicalUUID(value); got != want {
			t.Errorf("isCanonicalUUID(%q) = %t, want %t", value, got, want)
		}
	}
}

func TestLookupDomainPrefersNameThenFallsBackToUUID(t *testing.T) {
	uuid := "52d7a2fe-1942-4a89-93d9-9a57d8f67b6d"
	noDomain := libvirt.Error{Code: libvirt.ERR_NO_DOMAIN}

	t.Run("UUID-shaped name wins", func(t *testing.T) {
		uuidCalled := false
		_, err := lookupDomain(uuid,
			func(string) (*libvirt.Domain, error) { return nil, nil },
			func(string) (*libvirt.Domain, error) { uuidCalled = true; return nil, nil },
		)
		if err != nil || uuidCalled {
			t.Fatalf("error = %v, UUID lookup called = %t", err, uuidCalled)
		}
	})

	t.Run("name miss falls back to UUID", func(t *testing.T) {
		calls := make([]string, 0, 2)
		_, err := lookupDomain(uuid,
			func(string) (*libvirt.Domain, error) { calls = append(calls, "name"); return nil, noDomain },
			func(string) (*libvirt.Domain, error) { calls = append(calls, "uuid"); return nil, nil },
		)
		if err != nil || len(calls) != 2 || calls[0] != "name" || calls[1] != "uuid" {
			t.Fatalf("error = %v, calls = %v", err, calls)
		}
	})

	t.Run("both missing maps to not found", func(t *testing.T) {
		_, err := lookupDomain(uuid,
			func(string) (*libvirt.Domain, error) { return nil, noDomain },
			func(string) (*libvirt.Domain, error) { return nil, noDomain },
		)
		if !errors.Is(err, ErrNotFound) {
			t.Fatalf("error = %v, want ErrNotFound", err)
		}
	})

	t.Run("ordinary name never tries UUID", func(t *testing.T) {
		_, err := lookupDomain("win11",
			func(string) (*libvirt.Domain, error) { return nil, noDomain },
			func(string) (*libvirt.Domain, error) { t.Fatal("unexpected UUID lookup"); return nil, nil },
		)
		if !errors.Is(err, ErrNotFound) {
			t.Fatalf("error = %v, want ErrNotFound", err)
		}
	})
}

func TestDomainActionLocksAreScopedAndReleased(t *testing.T) {
	service := &Libvirt{}
	unlockFirst := service.lockDomainAction("first")
	acquiredSecond := make(chan func(), 1)
	go func() {
		acquiredSecond <- service.lockDomainAction("second")
	}()
	select {
	case unlockSecond := <-acquiredSecond:
		unlockSecond()
	case <-time.After(time.Second):
		t.Fatal("an unrelated domain action was blocked")
	}
	unlockFirst()

	service.locksMu.Lock()
	remaining := len(service.actionLocks)
	service.locksMu.Unlock()
	if remaining != 0 {
		t.Fatalf("action lock count = %d, want 0", remaining)
	}
}

func TestDomainActionLockSerializesSameUUID(t *testing.T) {
	service := &Libvirt{}
	unlockFirst := service.lockDomainAction("52d7a2fe-1942-4a89-93d9-9a57d8f67b6d")
	acquired := make(chan func(), 1)
	go func() {
		acquired <- service.lockDomainAction("52d7a2fe-1942-4a89-93d9-9a57d8f67b6d")
	}()
	select {
	case unlock := <-acquired:
		unlock()
		t.Fatal("same-domain action lock was acquired concurrently")
	case <-time.After(25 * time.Millisecond):
	}
	unlockFirst()
	select {
	case unlock := <-acquired:
		unlock()
	case <-time.After(time.Second):
		t.Fatal("same-domain action lock was not released")
	}
}
