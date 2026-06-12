package room

import (
	"testing"
)

func TestRegistrySpawnAndReuse(t *testing.T) {
	eng := newTestEngine(t)
	reg := NewRegistry(eng, &stubStore{})

	first := reg.PIDFor("roomA")
	if first == nil {
		t.Fatal("expected a PID for roomA")
	}

	second := reg.PIDFor("roomA")
	if !first.Equals(second) {
		t.Errorf("same room should reuse the PID: %v vs %v", first, second)
	}

	other := reg.PIDFor("roomB")
	if first.Equals(other) {
		t.Error("different rooms must get different PIDs")
	}
}
