package mailpoll

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
)

func TestExtensionControlPlaneUsesIdentityScopedPrivatePlannerRoute(t *testing.T) {
	var path string
	control, err := NewExtensionControlPlane(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		path = request.URL.Path
		if request.Header.Get("X-Gateway-Internal") != "" {
			t.Error("Extension received or supplied the host internal credential")
		}
		_ = json.NewEncoder(writer).Encode(Claim{RouteID: "poll-1", Address: "support@example.test", LeaseToken: "lease"})
	}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := control.Claim(context.Background(), "poll-1", ClaimPoll); err != nil {
		t.Fatal(err)
	}
	if path != "/_internal/gateway/extensions/webong/email/mailbox-polls/poll-1/claim" {
		t.Fatalf("private Planner path = %q", path)
	}
	if err := control.Baseline(context.Background(), "poll-1", "lease", 13, 0); err != nil {
		t.Fatal(err)
	}
	if path != "/_internal/gateway/extensions/webong/email/mailbox-polls/poll-1/baseline" {
		t.Fatalf("private Planner baseline path = %q", path)
	}
}
