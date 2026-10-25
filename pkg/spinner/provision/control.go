package provision

import (
	"net/http"
	"strings"
)

// InternalPlannerRequest reports whether a request belongs to the private
// provisioning control surface used by managed targets.
func InternalPlannerRequest(request *http.Request) bool {
	const prefix = "/_internal/provisioning"
	return request.URL.Path == prefix || strings.HasPrefix(request.URL.Path, prefix+"/")
}
