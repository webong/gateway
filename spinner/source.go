package spinner

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"net/netip"
	"net/url"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	protocol "github.com/webong/gateway/arranger/integration"
	"golang.org/x/net/http/httpproxy"
)

// WebstreamSource owns the Web Extension's outbound SSE policy. The generic
// Spinner host only supplies protocol-neutral Planner and delivery services.
type WebstreamSource struct {
	privateNetworkCeilings []netip.Prefix
}

func NewWebstreamSource(privateNetworkCIDRs []string) (*WebstreamSource, error) {
	ceilings := make([]netip.Prefix, 0, len(privateNetworkCIDRs))
	for _, raw := range privateNetworkCIDRs {
		prefix, err := netip.ParsePrefix(strings.TrimSpace(raw))
		if err != nil || prefix != prefix.Masked() || prefix.Addr().Is4In6() {
			return nil, fmt.Errorf("invalid Webstream private-network CIDR %q", raw)
		}
		ceilings = append(ceilings, prefix)
	}
	return &WebstreamSource{privateNetworkCeilings: ceilings}, nil
}

func (source *WebstreamSource) OpenWebstream(ctx context.Context, delivery protocol.GatewayDelivery) (*http.Response, error) {
	method := delivery.Attributes["method"]
	if method == "" {
		method = http.MethodGet
	}
	return source.open(ctx, method, delivery, bytes.NewReader(delivery.Payload), false)
}

func (source *WebstreamSource) OpenWebstreamUpload(ctx context.Context, delivery protocol.GatewayDelivery, body io.ReadCloser) (*http.Response, error) {
	if delivery.Attributes["method"] != http.MethodPost || delivery.Attributes["upload_mode"] != "stream" ||
		delivery.Attributes["retry_idempotent"] == "true" || len(delivery.Payload) > 0 {
		return nil, fmt.Errorf("invalid streaming upload policy")
	}
	return source.open(ctx, http.MethodPost, delivery, body, true)
}

func (source *WebstreamSource) open(ctx context.Context, method string, delivery protocol.GatewayDelivery, body io.Reader, upload bool) (*http.Response, error) {
	if method != http.MethodGet && method != http.MethodPost {
		return nil, fmt.Errorf("unsupported Webstream method %q", method)
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

	headers := cloneSourceHeaders(delivery)
	headers.Del("Content-Encoding")
	retry := delivery.Attributes["retry_idempotent"] == "true"
	if retry && (method != http.MethodPost || strings.TrimSpace(headers.Get("Idempotency-Key")) == "") {
		return nil, fmt.Errorf("retry requires an idempotent POST with one key")
	}
	if retry && headers.Get("Idempotency-Key") != http.Header(delivery.Headers).Get("Idempotency-Key") {
		return nil, fmt.Errorf("source profile cannot override replay key")
	}

	privateRanges, err := source.privateRanges(delivery.Attributes["private_source_cidrs"])
	if err != nil {
		return nil, err
	}
	transport, openingTimeout, err := source.transport(delivery.Transport, privateRanges)
	if err != nil {
		return nil, err
	}
	if upload {
		openingTimeout = 5 * time.Minute
	}

	requestContext, cancel := context.WithCancel(ctx)
	timer := time.AfterFunc(openingTimeout, cancel)
	request, err := http.NewRequestWithContext(requestContext, method, target.String(), body)
	if err != nil {
		timer.Stop()
		cancel()
		transport.CloseIdleConnections()
		return nil, err
	}
	request.Header = headers
	if method == http.MethodPost && !upload {
		request.ContentLength = int64(len(delivery.Payload))
		// Prevent net/http from manufacturing GetBody and replaying POSTs.
		request.GetBody = nil
	}
	client := &http.Client{
		Transport:     transport,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	var received atomic.Bool
	request = request.WithContext(httptrace.WithClientTrace(request.Context(), &httptrace.ClientTrace{
		GotFirstResponseByte: func() { received.Store(true) },
	}))
	response, err := client.Do(request)
	if retry && err != nil && !received.Load() && requestContext.Err() == nil && transientStreamError(err) {
		retryRequest := request.Clone(request.Context())
		retryRequest.Body = io.NopCloser(bytes.NewReader(delivery.Payload))
		retryRequest.GetBody = nil
		select {
		case <-requestContext.Done():
			timer.Stop()
			cancel()
			transport.CloseIdleConnections()
			return nil, requestContext.Err()
		case <-time.After(100 * time.Millisecond):
		}
		response, err = client.Do(retryRequest)
	}
	if err != nil {
		timer.Stop()
		cancel()
		transport.CloseIdleConnections()
		return nil, err
	}
	if !timer.Stop() {
		response.Body.Close()
		cancel()
		transport.CloseIdleConnections()
		return nil, context.DeadlineExceeded
	}
	response.Body = &closingBody{ReadCloser: response.Body, close: func() {
		cancel()
		transport.CloseIdleConnections()
	}}
	return response, nil
}

func (source *WebstreamSource) privateRanges(raw string) ([]netip.Prefix, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	ranges := make([]netip.Prefix, 0)
	for _, item := range strings.Split(raw, ",") {
		prefix, err := netip.ParsePrefix(strings.TrimSpace(item))
		if err != nil || prefix != prefix.Masked() || prefix.Addr().Is4In6() {
			return nil, fmt.Errorf("invalid Planner Webstream private CIDR")
		}
		allowed := false
		for _, ceiling := range source.privateNetworkCeilings {
			if prefix.Bits() >= ceiling.Bits() && ceiling.Contains(prefix.Addr()) {
				allowed = true
				break
			}
		}
		if !allowed {
			return nil, fmt.Errorf("Planner Webstream private CIDR is outside Extension configuration")
		}
		ranges = append(ranges, prefix)
	}
	return ranges, nil
}

func (source *WebstreamSource) transport(settings *protocol.HTTPTransport, privateRanges []netip.Prefix) (*http.Transport, time.Duration, error) {
	connectTimeout := time.Duration(0)
	openingTimeout := 30 * time.Second
	transport := &http.Transport{ForceAttemptHTTP2: true, IdleConnTimeout: 90 * time.Second}
	if settings != nil {
		connectTimeout = durationSeconds(settings.ConnectTimeoutSeconds)
		if timeout := durationSeconds(settings.RequestTimeoutSeconds); timeout > 0 {
			openingTimeout = timeout
		}
	}
	allowed := publicAddress
	if len(privateRanges) > 0 {
		if settings != nil && settings.Proxy != nil && (settings.Proxy.HTTP != "" || settings.Proxy.HTTPS != "" || len(settings.Proxy.NoProxy) > 0) {
			return nil, 0, fmt.Errorf("private Webstream source cannot use an HTTP proxy")
		}
		allowed = func(address netip.Addr) bool {
			for _, prefix := range privateRanges {
				if prefix.Contains(address) {
					return true
				}
			}
			return false
		}
	} else if settings != nil && settings.Proxy != nil {
		proxyConfig := &httpproxy.Config{
			HTTPProxy: settings.Proxy.HTTP, HTTPSProxy: settings.Proxy.HTTPS,
			NoProxy: strings.Join(settings.Proxy.NoProxy, ","),
		}
		proxyFunc := proxyConfig.ProxyFunc()
		transport.Proxy = func(request *http.Request) (*url.URL, error) { return proxyFunc(request.URL) }
	}
	transport.DialContext = dialAllowed(connectTimeout, allowed)
	return transport, openingTimeout, nil
}

func dialAllowed(timeout time.Duration, allowed func(netip.Addr) bool) func(context.Context, string, string) (net.Conn, error) {
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		addresses, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
		if err != nil {
			return nil, err
		}
		dialer := &net.Dialer{Timeout: timeout}
		for _, candidate := range addresses {
			candidate = candidate.Unmap()
			if !allowed(candidate) {
				continue
			}
			connection, dialErr := dialer.DialContext(ctx, network, net.JoinHostPort(candidate.String(), port))
			if dialErr == nil {
				return connection, nil
			}
			err = dialErr
		}
		if err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("Webstream source resolves only to blocked addresses")
	}
}

func publicAddress(address netip.Addr) bool {
	return address.IsGlobalUnicast() && !address.IsPrivate() && !address.IsLoopback() &&
		!address.IsLinkLocalUnicast() && !address.IsUnspecified()
}

func durationSeconds(value float64) time.Duration {
	if value <= 0 {
		return 0
	}
	return time.Duration(value * float64(time.Second))
}

func transientStreamError(err error) bool {
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, syscall.ECONNRESET) ||
		errors.Is(err, syscall.EPIPE) || errors.Is(err, syscall.ECONNREFUSED) {
		return true
	}
	var network net.Error
	return errors.As(err, &network) && network.Timeout()
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
	for _, value := range headers.Values("Connection") {
		for _, token := range strings.Split(value, ",") {
			headers.Del(strings.TrimSpace(token))
		}
	}
	for _, name := range []string{"Host", "Content-Length", "Connection", "Transfer-Encoding", "Upgrade", "Proxy-Authorization", "Proxy-Connection", "Keep-Alive", "Trailer", "Te", "Expect"} {
		headers.Del(name)
	}
	headers.Set("Accept", "text/event-stream")
	return headers
}

type closingBody struct {
	io.ReadCloser
	close func()
}

func (body *closingBody) Close() error {
	err := body.ReadCloser.Close()
	body.close()
	return err
}
