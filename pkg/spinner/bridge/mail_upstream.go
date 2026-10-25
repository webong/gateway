package gateway

import (
	"context"
	"crypto/x509"
	"net"
	"time"

	"github.com/webong/gateway/pkg/email/spinner/mailnet"
)

// MailTLSDialer retains Spinner's public compatibility surface while the
// implementation lives with the Email extension.
type MailTLSDialer = mailnet.MailTLSDialer

func ValidMailUpstream(address, serverName, networkScope string) bool {
	return mailnet.ValidMailUpstream(address, serverName, networkScope)
}

func DialMailUpstream(ctx context.Context, address, networkScope string, timeout time.Duration) (net.Conn, error) {
	return mailnet.DialMailUpstream(ctx, address, networkScope, timeout)
}

func DialTLSMailUpstream(ctx context.Context, address, serverName, networkScope string,
	timeout time.Duration, roots *x509.CertPool) (net.Conn, error) {
	return mailnet.DialTLSMailUpstream(ctx, address, serverName, networkScope, timeout, roots)
}
