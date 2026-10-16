package forwarding

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/webong/gateway/src/spinner/internal/config"
	"github.com/webong/gateway/src/spinner/internal/logging"
	"golang.org/x/net/http/httpproxy"
)

const maxTransportClients = 128

var ErrResponseTooLarge = errors.New("response body exceeds maximum size")

type ForwardResponse struct {
	StatusCode int
	Header     http.Header
	Body       []byte
}

type Forwarder struct {
	client                *http.Client
	transport             *http.Transport
	clientsMu             sync.Mutex
	clients               map[[sha256.Size]byte]*http.Client
	clientOrder           [][sha256.Size]byte
	logger                *logging.Logger
	successCount          atomic.Uint64
	errorCount            atomic.Uint64
	totalRequests         atomic.Uint64
	maxBodySize           int64
	addressAllowed        func(net.IP) bool
	privateStreamNetworks []netip.Prefix
}

func NewForwarder(settings *config.Config, logger *logging.Logger) *Forwarder {
	forwarder := &Forwarder{
		logger:                logger,
		maxBodySize:           settings.MaxBodySize,
		addressAllowed:        isAllowedTargetAddress,
		privateStreamNetworks: settings.WebstreamPrivateNetworks,
		clients:               make(map[[sha256.Size]byte]*http.Client),
	}

	// Create optimized HTTP client with connection pooling
	transport := &http.Transport{
		MaxIdleConns:        settings.MaxIdleConns,
		MaxIdleConnsPerHost: settings.MaxConnsPerHost,
		MaxConnsPerHost:     settings.MaxConnsPerHost,
		IdleConnTimeout:     settings.IdleConnTimeout,
		DisableCompression:  false,
		DisableKeepAlives:   false,
		DialContext:         forwarder.dialContext(0),
	}

	client := &http.Client{
		Transport: transport,
		Timeout:   settings.RequestTimeout,
	}

	forwarder.client = client
	forwarder.transport = transport
	return forwarder
}

func (f *Forwarder) dialContext(timeout time.Duration) func(context.Context, string, string) (net.Conn, error) {
	return f.dialContextAllowed(timeout, func(ip net.IP) bool { return f.addressAllowed(ip) })
}

func (f *Forwarder) dialContextAllowed(timeout time.Duration, allowed func(net.IP) bool) func(context.Context, string, string) (net.Conn, error) {
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		addresses, err := net.DefaultResolver.LookupIPAddr(ctx, host)
		if err != nil {
			return nil, err
		}
		dialer := &net.Dialer{Timeout: timeout}
		for _, candidate := range addresses {
			if !allowed(candidate.IP) {
				continue
			}
			connection, dialErr := dialer.DialContext(ctx, network, net.JoinHostPort(candidate.IP.String(), port))
			if dialErr == nil {
				return connection, nil
			}
			err = dialErr
		}
		if err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("target %s resolves only to blocked addresses", host)
	}
}

func (f *Forwarder) Forward(req ForwardRequest) {
	f.totalRequests.Add(1)

	resp, err := f.forward(req, false)
	if err != nil {
		f.logger.Error("Error forwarding request to %s: %v", req.TargetURL, err)
		f.errorCount.Add(1)
		return
	}

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		f.successCount.Add(1)
		f.logger.Debug("Request forwarded successfully. Status: %d, Target: %s", resp.StatusCode, req.TargetURL)
	} else {
		f.errorCount.Add(1)
		f.logger.Warn("Request forwarded with error status. Status: %d, Target: %s", resp.StatusCode, req.TargetURL)
	}
}

func (f *Forwarder) ForwardSync(req ForwardRequest) (ForwardResponse, error) {
	f.totalRequests.Add(1)

	response, err := f.forward(req, true)
	if err != nil {
		f.errorCount.Add(1)
		return ForwardResponse{}, err
	}

	if response.StatusCode >= 200 && response.StatusCode < 300 {
		f.successCount.Add(1)
	} else {
		f.errorCount.Add(1)
	}

	return response, nil
}

// ForwardStatus observes a delivery response without buffering its body. An
// asynchronous callback succeeds or fails by status, not by response size.
func (f *Forwarder) ForwardStatus(req ForwardRequest) (int, error) {
	f.totalRequests.Add(1)
	response, err := f.forward(req, false)
	if err != nil {
		f.errorCount.Add(1)
		return 0, err
	}
	if response.StatusCode >= 200 && response.StatusCode < 300 {
		f.successCount.Add(1)
	} else {
		f.errorCount.Add(1)
	}
	return response.StatusCode, nil
}

// OpenStream keeps the response body open. Its caller's context owns the
// stream lifetime, including cancellation when the downstream client leaves.
func (f *Forwarder) OpenStream(ctx context.Context, target string, headers http.Header, settings HTTPTransport) (*http.Response, error) {
	return f.OpenStreamRequest(ctx, http.MethodGet, target, headers, nil, settings)
}

// OpenStreamRequest sends a single bounded request, never a replayable upload.
func (f *Forwarder) OpenStreamRequest(ctx context.Context, method, target string, headers http.Header, payload []byte, settings HTTPTransport) (*http.Response, error) {
	return f.OpenStreamRequestPolicy(ctx, method, target, headers, payload, settings, false)
}

func (f *Forwarder) OpenStreamUpload(ctx context.Context, target string, headers http.Header, body io.ReadCloser, settings HTTPTransport) (*http.Response, error) {
	return f.openStream(ctx, http.MethodPost, target, headers, body, 0, settings, false, nil, true)
}

func (f *Forwarder) OpenStreamRequestPolicy(ctx context.Context, method, target string, headers http.Header, payload []byte, settings HTTPTransport, retry bool) (*http.Response, error) {
	if method != http.MethodGet && method != http.MethodPost {
		return nil, fmt.Errorf("unsupported Webstream method %q", method)
	}
	var body io.Reader
	if method == http.MethodPost {
		// Hide GetBody, including for empty POSTs: net/http must not replay it.
		body = io.NopCloser(bytes.NewReader(payload))
	}
	if retry && (method != http.MethodPost || len(headers.Values("Idempotency-Key")) != 1 || strings.TrimSpace(headers.Get("Idempotency-Key")) == "") {
		return nil, fmt.Errorf("retry requires an idempotent POST with one key")
	}
	return f.openStream(ctx, method, target, headers, body, int64(len(payload)), settings, retry, payload, false)
}

func (f *Forwarder) openStream(ctx context.Context, method, target string, headers http.Header, body io.Reader, length int64, settings HTTPTransport, retry bool, payload []byte, upload bool) (*http.Response, error) {
	ctx, cancel := context.WithCancel(ctx)
	// Bound DNS, dialing, TLS and response headers, but not the response body.
	timeout := settings.RequestTimeout
	if timeout <= 0 {
		timeout = f.client.Timeout
	}
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	if upload {
		timeout = 5 * time.Minute
	}
	timer := time.AfterFunc(timeout, cancel)
	defer timer.Stop()
	request, err := http.NewRequestWithContext(ctx, method, target, body)
	if err != nil {
		cancel()
		return nil, err
	}
	request.Header = headers.Clone()
	if request.Header == nil {
		request.Header = make(http.Header)
	}
	for _, name := range request.Header.Values("Connection") {
		for _, token := range strings.Split(name, ",") {
			request.Header.Del(strings.TrimSpace(token))
		}
	}
	for _, name := range []string{"Host", "Content-Length", "Connection", "Transfer-Encoding", "Upgrade", "Proxy-Authorization", "Proxy-Connection", "Keep-Alive", "Trailer", "Te", "Expect"} {
		request.Header.Del(name)
	}
	request.Header.Set("Accept", "text/event-stream")
	if method == http.MethodPost && length > 0 {
		request.ContentLength = length
	}
	if retry && request.Header.Get("Idempotency-Key") != headers.Get("Idempotency-Key") {
		cancel()
		return nil, fmt.Errorf("idempotency key removed by connection headers")
	}
	client, err := f.clientFor(settings)
	if err != nil {
		cancel()
		return nil, err
	}
	var ranges []netip.Prefix
	if settings.PrivateSourceCIDRs != "" {
		for _, item := range strings.Split(settings.PrivateSourceCIDRs, ",") {
			prefix, parseErr := netip.ParsePrefix(item)
			if parseErr != nil || prefix != prefix.Masked() || prefix.Addr().Is4In6() {
				cancel()
				return nil, fmt.Errorf("invalid Planner Webstream private CIDR")
			}
			withinCeiling := false
			for _, ceiling := range f.privateStreamNetworks {
				if prefix.Bits() >= ceiling.Bits() && ceiling.Contains(prefix.Addr()) {
					withinCeiling = true
					break
				}
			}
			if !withinCeiling {
				cancel()
				return nil, fmt.Errorf("Planner Webstream private CIDR is outside deployment ceiling")
			}
			ranges = append(ranges, prefix)
		}
	}
	if len(ranges) > 0 {
		// An operator grant applies only to this exact SSE origin. Bypass any
		// profile proxy and pin the DNS result at dial time to granted ranges.
		if settings.HTTPProxy != "" || settings.HTTPSProxy != "" {
			cancel()
			return nil, fmt.Errorf("private Webstream source cannot use an HTTP proxy")
		}
		transport := f.transport.Clone()
		transport.Proxy = nil
		transport.DialContext = f.dialContextAllowed(settings.ConnectTimeout, func(ip net.IP) bool {
			address, ok := netip.AddrFromSlice(ip)
			if !ok {
				return false
			}
			address = address.Unmap()
			for _, prefix := range ranges {
				if prefix.Contains(address) {
					return true
				}
			}
			return false
		})
		defer transport.CloseIdleConnections()
		privateClient := *client
		privateClient.Transport = transport
		client = &privateClient
	}
	streamClient := *client
	streamClient.Timeout = 0
	// A selected source must not redirect its credentials to another URL.
	streamClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	var received atomic.Bool
	request = request.WithContext(httptrace.WithClientTrace(request.Context(), &httptrace.ClientTrace{GotFirstResponseByte: func() { received.Store(true) }}))
	response, err := streamClient.Do(request)
	if retry && err != nil && !received.Load() && ctx.Err() == nil && transientStreamError(err) {
		// Exactly one explicit retry; GetBody remains nil on both attempts.
		retryRequest := request.Clone(request.Context())
		retryRequest.Body = io.NopCloser(bytes.NewReader(payload))
		select {
		case <-ctx.Done():
			cancel()
			return nil, ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
		response, err = streamClient.Do(retryRequest)
	}
	if err != nil {
		cancel()
		return nil, err
	}
	if !timer.Stop() {
		response.Body.Close()
		cancel()
		return nil, context.DeadlineExceeded
	}
	response.Body = &streamBody{ReadCloser: response.Body, cancel: cancel}
	return response, nil
}

func transientStreamError(err error) bool {
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.EPIPE) || errors.Is(err, syscall.ECONNREFUSED) {
		return true
	}
	var network net.Error
	return errors.As(err, &network) && network.Timeout()
}

type streamBody struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (b *streamBody) Close() error { b.cancel(); return b.ReadCloser.Close() }

func (f *Forwarder) forward(req ForwardRequest, captureBody bool) (ForwardResponse, error) {
	targetURL, err := url.Parse(req.TargetURL)
	if err != nil {
		return ForwardResponse{}, err
	}
	if req.RawQuery != "" {
		if targetURL.RawQuery != "" {
			targetURL.RawQuery += "&"
		}
		targetURL.RawQuery += req.RawQuery
	}
	forwardURL := targetURL.String()

	f.logger.Debug("Forwarding to: %s", forwardURL)
	requestContext := req.Context
	if requestContext == nil {
		requestContext = context.Background()
	}
	httpReq, err := http.NewRequestWithContext(requestContext, req.Method, forwardURL, bytes.NewReader(req.RequestBody))
	if err != nil {
		return ForwardResponse{}, err
	}

	for key, values := range req.Headers {
		for _, value := range values {
			httpReq.Header.Add(key, value)
		}
	}

	client, err := f.clientFor(req.Transport)
	if err != nil {
		return ForwardResponse{}, err
	}
	resp, err := client.Do(httpReq)
	if err != nil {
		return ForwardResponse{}, err
	}
	defer resp.Body.Close()

	var body []byte
	if captureBody {
		body, err = io.ReadAll(io.LimitReader(resp.Body, f.maxBodySize+1))
		if err != nil {
			return ForwardResponse{}, err
		}
		if int64(len(body)) > f.maxBodySize {
			return ForwardResponse{}, fmt.Errorf("%w of %d bytes", ErrResponseTooLarge, f.maxBodySize)
		}
	}

	return ForwardResponse{
		StatusCode: resp.StatusCode,
		Header:     resp.Header.Clone(),
		Body:       body,
	}, nil
}

func (f *Forwarder) clientFor(settings HTTPTransport) (*http.Client, error) {
	if settings.HTTPProxy == "" && settings.HTTPSProxy == "" && len(settings.NoProxy) == 0 &&
		settings.ConnectTimeout == 0 && settings.RequestTimeout == 0 {
		return f.client, nil
	}

	key := transportKey(settings)
	f.clientsMu.Lock()
	defer f.clientsMu.Unlock()
	if client := f.clients[key]; client != nil {
		return client, nil
	}

	transport := f.transport.Clone()
	transport.DialContext = f.dialContext(settings.ConnectTimeout)
	if settings.HTTPProxy != "" || settings.HTTPSProxy != "" || len(settings.NoProxy) > 0 {
		proxyConfig := &httpproxy.Config{
			HTTPProxy:  settings.HTTPProxy,
			HTTPSProxy: settings.HTTPSProxy,
			NoProxy:    strings.Join(settings.NoProxy, ","),
		}
		proxyFunc := proxyConfig.ProxyFunc()
		transport.Proxy = func(request *http.Request) (*url.URL, error) {
			return proxyFunc(request.URL)
		}
	}

	timeout := f.client.Timeout
	if settings.RequestTimeout > 0 {
		timeout = settings.RequestTimeout
	}
	client := &http.Client{Transport: transport, Timeout: timeout}
	if len(f.clients) >= maxTransportClients {
		oldest := f.clientOrder[0]
		f.clientOrder = f.clientOrder[1:]
		if evicted := f.clients[oldest]; evicted != nil {
			evicted.CloseIdleConnections()
		}
		delete(f.clients, oldest)
	}
	f.clients[key] = client
	f.clientOrder = append(f.clientOrder, key)
	return client, nil
}

func transportKey(settings HTTPTransport) [sha256.Size]byte {
	parts := []string{
		settings.HTTPProxy,
		settings.HTTPSProxy,
		strings.Join(settings.NoProxy, "\x00"),
		strconv.FormatInt(int64(settings.ConnectTimeout), 10),
		strconv.FormatInt(int64(settings.RequestTimeout), 10),
	}
	return sha256.Sum256([]byte(strings.Join(parts, "\x01")))
}

func (f *Forwarder) GetStats() (total, success, errors uint64) {
	return f.totalRequests.Load(), f.successCount.Load(), f.errorCount.Load()
}

// CloseIdleConnections releases pooled outbound connections when the host
// runtime unloads the transport plane.
func (f *Forwarder) CloseIdleConnections() {
	if f == nil || f.client == nil || f.client.Transport == nil {
		return
	}
	if closer, ok := f.client.Transport.(interface{ CloseIdleConnections() }); ok {
		closer.CloseIdleConnections()
	}
	f.clientsMu.Lock()
	defer f.clientsMu.Unlock()
	for _, client := range f.clients {
		client.CloseIdleConnections()
	}
}
