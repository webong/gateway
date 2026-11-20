package extensions

import (
	"net/http"
	"time"

	"github.com/webong/ext/pkg/ext-go/engine/host/store"
)

// NewArtifactHTTPClient uses Ext's verified HTTPS redirect policy, with
// Gateway's transfer timeout. Planner credentials use a separate client.
func NewArtifactHTTPClient() *http.Client {
	client := store.NewDownloadClient()
	client.Timeout = 60 * time.Second
	return client
}
