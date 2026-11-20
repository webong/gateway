package provision

import "testing"

func TestWorkerOwnershipFencesControllerAndPersistsIdentity(t *testing.T) {
	config := Config{NodeID: "node-a", TargetID: "target-a", StateDirectory: t.TempDir()}
	first, err := acquireWorkerOwnership(config)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	if duplicate, err := acquireWorkerOwnership(config); err == nil {
		duplicate.Close()
		t.Fatal("two controllers acquired the same journal")
	}
	id := first.id
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	recovered, err := acquireWorkerOwnership(config)
	if err != nil {
		t.Fatal(err)
	}
	defer recovered.Close()
	if recovered.id != id {
		t.Fatal("restart replaced persistent ownership")
	}
	other := config
	other.StateDirectory = t.TempDir()
	independent, err := acquireWorkerOwnership(other)
	if err != nil {
		t.Fatal(err)
	}
	defer independent.Close()
	if independent.id == id {
		t.Fatal("different journal reused owner identity")
	}
}

func TestWorkerOwnershipRequiresDurableAbsoluteDirectory(t *testing.T) {
	for _, config := range []Config{{NodeID: "node-a"}, {NodeID: "node-a", StateDirectory: "relative"}, {StateDirectory: t.TempDir()}} {
		if owner, err := acquireWorkerOwnership(config); err == nil {
			owner.Close()
			t.Fatal("invalid ownership configuration accepted")
		}
	}
}
