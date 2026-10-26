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
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/webong/gateway/pkg/spinner/internal/config"
	"github.com/webong/gateway/pkg/spinner/internal/logging"
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
	client                  *http.Client
	transport               *http.Transport
	clientsMu               sync.Mutex
	clients                 map[[sha256.Size]byte]*http.Client
	clientOrder             [][sha256.Size]byte
	logger                  *logging.Logger
	successCount            atomic.Uint64
	errorCount              atomic.Uint64
	totalRequests           atomic.Uint64
	maxBodySize             int64
	addressAllowed          func(net.IP) bool
	privateCallbackNetworks []netip.Prefix
}

func NewForwarder(settings *config.Config, logger *logging.Logger) *Forwarder {
	forwarder := &Forwarder{
		logger:                  logger,
		maxBodySize:             settings.MaxBodySize,
		addressAllowed:          isAllowedTargetAddress,
		privateCallbackNetworks: settings.HTTPPrivateNetworks,
		clients:                 make(map[[sha256.Size]byte]*http.Client),
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

	var client *http.Client
	var privateTransport *http.Transport
	if req.Transport.PrivateCallbackCIDRs != "" {
		client, privateTransport, err = f.privateCallbackClient(req.Transport)
		if privateTransport != nil {
			defer privateTransport.CloseIdleConnections()
		}
	} else {
		client, err = f.clientFor(req.Transport)
	}
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

func (f *Forwarder) privateCallbackClient(settings HTTPTransport) (*http.Client, *http.Transport, error) {
	if settings.HTTPProxy != "" || settings.HTTPSProxy != "" || len(settings.NoProxy) != 0 {
		return nil, nil, fmt.Errorf("private callback cannot use an HTTP proxy")
	}
	var ranges []netip.Prefix
	for _, item := range strings.Split(settings.PrivateCallbackCIDRs, ",") {
		prefix, err := netip.ParsePrefix(strings.TrimSpace(item))
		if err != nil || prefix != prefix.Masked() || prefix.Addr().Is4In6() {
			return nil, nil, fmt.Errorf("invalid Planner private callback CIDR")
		}
		withinCeiling := false
		for _, ceiling := range f.privateCallbackNetworks {
			if prefix.Bits() >= ceiling.Bits() && ceiling.Contains(prefix.Addr()) {
				withinCeiling = true
				break
			}
		}
		if !withinCeiling {
			return nil, nil, fmt.Errorf("Planner private callback CIDR is outside deployment ceiling")
		}
		ranges = append(ranges, prefix)
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
	timeout := f.client.Timeout
	if settings.RequestTimeout > 0 {
		timeout = settings.RequestTimeout
	}
	client := &http.Client{Transport: transport, Timeout: timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return client, transport, nil
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
