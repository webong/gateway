package spinner

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	protocol "github.com/webong/gateway/arranger/protocol"
)

// PublicWebstreamSource opens a Planner-authorized SSE source while enforcing
// the extension's deployment safety ceiling. Private sources remain closed
// until a future runtime contract supplies an explicit private-network grant.
type PublicWebstreamSource struct{}

func (PublicWebstreamSource) OpenWebstream(ctx context.Context, delivery protocol.GatewayDelivery) (*http.Response, error) {
	method := delivery.Attributes["method"]
	if method == "" {
		method = http.MethodGet
	}
	return openPublicWebstream(ctx, method, delivery, bytes.NewReader(delivery.Payload))
}

func (PublicWebstreamSource) OpenWebstreamUpload(ctx context.Context, delivery protocol.GatewayDelivery, body io.ReadCloser) (*http.Response, error) {
	if delivery.Attributes["method"] != http.MethodPost || delivery.Attributes["upload_mode"] != "stream" {
		return nil, fmt.Errorf("invalid streaming upload policy")
	}
	return openPublicWebstream(ctx, http.MethodPost, delivery, body)
}

func openPublicWebstream(ctx context.Context, method string, delivery protocol.GatewayDelivery, body io.Reader) (*http.Response, error) {
	if method != http.MethodGet && method != http.MethodPost {
		return nil, fmt.Errorf("unsupported Webstream method %q", method)
	}
	if strings.TrimSpace(delivery.Attributes["private_source_cidrs"]) != "" {
		return nil, fmt.Errorf("private Webstream sources are not enabled for this runtime")
	}
	target, err := url.Parse(delivery.Target)
	if err != nil || target.User != nil || target.Fragment != "" || target.Host == "" || (target.Scheme != "http" && target.Scheme != "https") {
		return nil, fmt.Errorf("invalid Webstream source")
	}
	if query := delivery.Attributes["raw_query"]; method == http.MethodPost && query != "" {
		if target.RawQuery != "" {
			target.RawQuery += "&"
		}
		target.RawQuery += query
	}
	request, err := http.NewRequestWithContext(ctx, method, target.String(), body)
	if err != nil {
		return nil, err
	}
	request.Header = cloneSourceHeaders(delivery)
	request.Header.Set("Accept", "text/event-stream")
	transport := &http.Transport{
		Proxy:                 nil,
		DialContext:           publicDialer,
		ForceAttemptHTTP2:     true,
		ResponseHeaderTimeout: requestTimeout(delivery),
		IdleConnTimeout:       90 * time.Second,
	}
	client := &http.Client{
		Transport:     transport,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	response, err := client.Do(request)
	if err != nil {
		transport.CloseIdleConnections()
		return nil, err
	}
	response.Body = &closingBody{ReadCloser: response.Body, close: transport.CloseIdleConnections}
	return response, nil
}

func publicDialer(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	addresses, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}
	var last error
	for _, candidate := range addresses {
		ip := candidate.IP
		if !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsUnspecified() {
			continue
		}
		connection, dialErr := (&net.Dialer{}).DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
		if dialErr == nil {
			return connection, nil
		}
		last = dialErr
	}
	if last != nil {
		return nil, last
	}
	return nil, fmt.Errorf("Webstream source resolves only to blocked addresses")
}

func requestTimeout(delivery protocol.GatewayDelivery) time.Duration {
	if delivery.Transport != nil && delivery.Transport.RequestTimeoutSeconds > 0 {
		return time.Duration(delivery.Transport.RequestTimeoutSeconds * float64(time.Second))
	}
	return 30 * time.Second
}

func cloneSourceHeaders(delivery protocol.GatewayDelivery) http.Header {
	headers := make(http.Header)
	for name, values := range delivery.Headers {
		headers[http.CanonicalHeaderKey(name)] = append([]string(nil), values...)
	}
	if delivery.Transport != nil {
		for name, values := range delivery.Transport.StreamHeaders {
			headers[http.CanonicalHeaderKey(name)] = append([]string(nil), values...)
		}
	}
	for _, name := range []string{"Host", "Content-Length", "Connection", "Transfer-Encoding", "Upgrade", "Proxy-Authorization", "Proxy-Connection", "Keep-Alive", "Trailer", "Te", "Expect"} {
		headers.Del(name)
	}
	return headers
}

type closingBody struct {
	io.ReadCloser
	close func()
}

func (b *closingBody) Close() error {
	err := b.ReadCloser.Close()
	b.close()
	return err
}
