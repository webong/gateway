package spinner

import (
	"context"
	"strings"
	"testing"

	protocol "github.com/webong/gateway/arranger/integration"
)

func TestWebstreamPublicDialRefusesSpecialUseAddresses(t *testing.T) {
	dial := dialAllowed(0, protocol.PublicEgressAddress)
	for _, address := range []string{"127.0.0.1", "169.254.169.254", "100.100.100.200", "198.18.0.1", "::ffff:100.100.100.200"} {
		_, err := dial(context.Background(), "tcp", "["+address+"]:80")
		if err == nil || !strings.Contains(err.Error(), "blocked addresses") {
			t.Errorf("expected dial to %s to be refused, got %v", address, err)
		}
	}
}
