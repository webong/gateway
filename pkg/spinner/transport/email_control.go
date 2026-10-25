package transport

import (
	"net/http"
	"strings"

	"github.com/google/uuid"
)

// EmailInternalPlannerRequest reports whether a request belongs to the narrow
// private Planner surface required by Gateway's email transports.
func EmailInternalPlannerRequest(request *http.Request) bool {
	return mailboxCredentialRequest(request) || emailRuntimeRequest(request)
}

func mailboxCredentialRequest(request *http.Request) bool {
	if request.Method != http.MethodGet {
		return false
	}
	const prefix = "/_internal/gateway/mailbox-connections/"
	if !strings.HasPrefix(request.URL.Path, prefix) || !strings.HasSuffix(request.URL.Path, "/smtp") {
		return false
	}
	id := strings.TrimSuffix(strings.TrimPrefix(request.URL.Path, prefix), "/smtp")
	if strings.Contains(id, "/") {
		return false
	}
	_, err := uuid.Parse(id)
	return err == nil
}

func emailRuntimeRequest(request *http.Request) bool {
	const prefix = "/_internal/gateway/email/"
	if !strings.HasPrefix(request.URL.Path, prefix) {
		return false
	}
	remainder := strings.TrimPrefix(request.URL.Path, prefix)

	switch {
	case (request.Method == http.MethodGet || request.Method == http.MethodHead) &&
		strings.HasPrefix(remainder, "dkim/"):
		return singlePathSegment(strings.TrimPrefix(remainder, "dkim/"))
	case request.Method == http.MethodGet && strings.HasPrefix(remainder, "hosted-adapters/"):
		return singlePathSegment(strings.TrimPrefix(remainder, "hosted-adapters/"))
	case request.Method == http.MethodPost && remainder == "outbound-plan":
		return true
	case request.Method == http.MethodPost && strings.HasPrefix(remainder, "outbound-attempts/"):
		attempt := strings.TrimPrefix(remainder, "outbound-attempts/")
		if strings.HasSuffix(attempt, "/reserve") {
			attempt = strings.TrimSuffix(attempt, "/reserve")
		}
		return singlePathSegment(attempt)
	default:
		return false
	}
}

func singlePathSegment(value string) bool {
	return value != "" && !strings.Contains(value, "/")
}
