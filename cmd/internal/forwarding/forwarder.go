package forwarding

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sync/atomic"

	"github.com/webong/gateway/cmd/internal/config"
	"github.com/webong/gateway/cmd/internal/logging"
)

type ForwardResponse struct {
	StatusCode int
	Header     http.Header
	Body       []byte
}

type Forwarder struct {
	client         *http.Client
	logger         *logging.Logger
	successCount   atomic.Uint64
	errorCount     atomic.Uint64
	totalRequests  atomic.Uint64
	maxBodySize    int64
	addressAllowed func(net.IP) bool
}

func NewForwarder(settings *config.Config, logger *logging.Logger) *Forwarder {
	forwarder := &Forwarder{
		logger:         logger,
		maxBodySize:    settings.MaxBodySize,
		addressAllowed: isAllowedTargetAddress,
	}

	// Create optimized HTTP client with connection pooling
	transport := &http.Transport{
		MaxIdleConns:        settings.MaxIdleConns,
		MaxIdleConnsPerHost: settings.MaxConnsPerHost,
		MaxConnsPerHost:     settings.MaxConnsPerHost,
		IdleConnTimeout:     settings.IdleConnTimeout,
		DisableCompression:  false,
		DisableKeepAlives:   false,
		DialContext:         forwarder.dialContext,
	}

	client := &http.Client{
		Transport: transport,
		Timeout:   settings.RequestTimeout,
	}

	forwarder.client = client
	return forwarder
}

func (f *Forwarder) dialContext(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	addresses, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}
	dialer := &net.Dialer{}
	for _, candidate := range addresses {
		if !f.addressAllowed(candidate.IP) {
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

	resp, err := f.client.Do(httpReq)
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
			return ForwardResponse{}, fmt.Errorf("response body exceeds maximum size of %d bytes", f.maxBodySize)
		}
	}

	return ForwardResponse{
		StatusCode: resp.StatusCode,
		Header:     resp.Header.Clone(),
		Body:       body,
	}, nil
}

func (f *Forwarder) GetStats() (total, success, errors uint64) {
	return f.totalRequests.Load(), f.successCount.Load(), f.errorCount.Load()
}
