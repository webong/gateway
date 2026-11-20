package provision

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestExecutionSnapshotUsesConfirmedOwnerAndConservativeDeadline(t *testing.T) {
	var started time.Time
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request map[string]any
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		if request["execution_protocol"] != "workload/v1" || request["owner_id"] != "persistent-owner" {
			t.Error("missing execution ownership protocol")
		}
		time.Sleep(30 * time.Millisecond)
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []ServerSpec{validServerSpec()}, "meta": map[string]any{
			"session_id": request["session_id"], "owner_id": request["owner_id"], "execution_protocol": "workload/v1",
			"issued_at_ms": 1000000, "permission_expires_at_ms": 1002000,
		}})
	}))
	defer backend.Close()
	control, err := NewHTTPControlPlane(backend.URL, "token", backend.Client())
	if err != nil {
		t.Fatal(err)
	}
	control.ownerID = "persistent-owner"
	started = time.Now()
	snapshot, _, err := control.executionAssignments(t.Context(), "node-a", []string{"local"}, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.specs) != 1 || snapshot.deadline.After(started.Add(time.Second)) || snapshot.deadline.After(time.Now().Add(900*time.Millisecond)) {
		t.Fatalf("response transit renewed or widened permission: %+v", snapshot)
	}
}

func TestExecutionSnapshotRejectsMissingOwnershipAndExpiredTransit(t *testing.T) {
	for _, mode := range []string{"legacy", "wrong-owner", "expired-transit", "malformed", "null-snapshot", "missing-snapshot"} {
		t.Run(mode, func(t *testing.T) {
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var request map[string]any
				_ = json.NewDecoder(r.Body).Decode(&request)
				meta := map[string]any{"session_id": request["session_id"], "owner_id": "persistent-owner", "execution_protocol": "workload/v1", "issued_at_ms": 1000000, "permission_expires_at_ms": 1002000}
				switch mode {
				case "legacy":
					delete(meta, "execution_protocol")
				case "wrong-owner":
					meta["owner_id"] = "other-owner"
				case "expired-transit":
					meta["permission_expires_at_ms"] = 1000150
					time.Sleep(100 * time.Millisecond)
				case "malformed":
					meta["permission_expires_at_ms"] = "not-a-deadline"
				}
				document := map[string]any{"data": []ServerSpec{}, "meta": meta}
				if mode == "null-snapshot" {
					document["data"] = nil
				}
				if mode == "missing-snapshot" {
					delete(document, "data")
				}
				_ = json.NewEncoder(w).Encode(document)
			}))
			defer backend.Close()
			control, err := NewHTTPControlPlane(backend.URL, "token", backend.Client())
			if err != nil {
				t.Fatal(err)
			}
			control.ownerID = "persistent-owner"
			if _, _, err := control.executionAssignments(t.Context(), "node-a", []string{"local"}, time.Second); err == nil {
				t.Fatal("invalid response became a successful empty snapshot")
			}
		})
	}
}
