package runtime

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	plugin "github.com/webong/ext/pkg/plugin-go/contract"
	arranger "github.com/webong/gateway/arranger/integration"
)

type httpDestination struct {
	URL               string              `json:"url"`
	EffectScope       string              `json:"effect_scope"`
	Method            string              `json:"method,omitempty"`
	Headers           map[string][]string `json:"headers,omitempty"`
	Idempotent        bool                `json:"idempotent"`
	IdempotencyHeader string              `json:"idempotency_header,omitempty"`
	TimeoutSeconds    int                 `json:"timeout_seconds,omitempty"`
}

var headerName = regexp.MustCompile(`^[A-Za-z0-9-]+$`)
var effectKey = regexp.MustCompile(`^[a-f0-9]{64}$`)

func parseHTTPDestination(raw json.RawMessage) (httpDestination, error) {
	var value httpDestination
	invalid := errors.New("invalid scoped HTTP destination")
	if plugin.Decode(raw, &value) != nil {
		return value, invalid
	}
	if len(value.EffectScope) < 1 || len(value.EffectScope) > 191 || strings.ContainsAny(value.EffectScope, "\x00\r\n") {
		return value, invalid
	}
	target, err := url.Parse(value.URL)
	if err != nil || (target.Scheme != "https" && target.Scheme != "http") || target.Host == "" || target.User != nil || target.Fragment != "" {
		return value, invalid
	}
	if value.Method == "" {
		value.Method = http.MethodPost
	}
	if value.Method != "POST" && value.Method != "PUT" && value.Method != "PATCH" {
		return value, invalid
	}
	if value.TimeoutSeconds == 0 {
		value.TimeoutSeconds = 10
	}
	if value.TimeoutSeconds < 1 || value.TimeoutSeconds > 20 {
		return value, invalid
	}
	if value.IdempotencyHeader == "" {
		value.IdempotencyHeader = "Idempotency-Key"
		if !value.Idempotent {
			value.IdempotencyHeader = "X-Weaver-Effect-Key"
		}
	}
	if !value.Idempotent && (strings.EqualFold(value.IdempotencyHeader, "Idempotency-Key") || strings.EqualFold(value.IdempotencyHeader, "X-Idempotency-Key")) {
		return value, invalid
	}
	if !headerName.MatchString(value.IdempotencyHeader) {
		return value, invalid
	}
	switch strings.ToLower(value.IdempotencyHeader) {
	case "authorization", "cookie", "host", "content-type", "content-length", "connection", "transfer-encoding":
		return value, invalid
	}
	headers := make(map[string][]string, len(value.Headers)+2)
	for name, entries := range value.Headers {
		if !headerName.MatchString(name) || len(entries) > 16 || strings.EqualFold(name, value.IdempotencyHeader) {
			return value, invalid
		}
		switch strings.ToLower(name) {
		case "idempotency-key", "x-idempotency-key", "host", "content-length", "connection", "transfer-encoding":
			return value, invalid
		}
		for _, entry := range entries {
			if strings.ContainsAny(entry, "\r\n") {
				return value, invalid
			}
		}
		headers[http.CanonicalHeaderKey(name)] = append([]string(nil), entries...)
	}
	value.Headers = headers
	value.IdempotencyHeader = http.CanonicalHeaderKey(value.IdempotencyHeader)
	return value, nil
}

func (d httpDestination) fingerprint() string {
	sum := sha256.Sum256([]byte(d.Method + "\x00" + d.URL + "\x00" + d.EffectScope + "\x00" + strings.ToLower(d.IdempotencyHeader)))
	return hex.EncodeToString(sum[:])
}

type effectResult struct {
	Status    int  `json:"status"`
	Retryable bool `json:"retryable"`
	Uncertain bool `json:"uncertain"`
}

func (s *Service) effectHandler(caller plugin.ReverseCaller) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		s.mu.Lock()
		token, closed := s.controlToken, s.closed
		s.mu.Unlock()
		provided := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if token == "" || !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") || subtle.ConstantTimeCompare([]byte(token), []byte(provided)) != 1 {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if closed {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		var request struct {
			Destination       string          `json:"destination"`
			DestinationSHA256 string          `json:"destination_sha256"`
			IdempotencyKey    string          `json:"idempotency_key"`
			Payload           json.RawMessage `json:"payload"`
		}
		raw, err := io.ReadAll(io.LimitReader(r.Body, 65537))
		if err != nil || len(raw) > 65536 || plugin.Decode(raw, &request) != nil || !effectKey.MatchString(request.IdempotencyKey) || !json.Valid(request.Payload) {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		s.mu.Lock()
		destination, ok := s.destinations[request.Destination]
		s.mu.Unlock()
		if !ok || request.DestinationSHA256 != destination.fingerprint() {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		headers := http.Header(destination.Headers).Clone()
		headers.Set("Content-Type", "application/json")
		headers.Set(destination.IdempotencyHeader, request.IdempotencyKey)
		ctx, cancel := context.WithTimeout(r.Context(), time.Duration(destination.TimeoutSeconds)*time.Second)
		defer cancel()
		delivery := arranger.Delivery{Protocol: arranger.ProtocolHTTP, URL: destination.URL, Method: destination.Method, Headers: headers, Body: append([]byte(nil), request.Payload...), Transport: &arranger.HTTPTransport{ConnectTimeoutSeconds: float64(destination.TimeoutSeconds), RequestTimeoutSeconds: float64(destination.TimeoutSeconds)}}
		payload, _ := json.Marshal(delivery)
		result, callErr := caller.Call(ctx, arranger.DeliveryContract, arranger.OperationDeliverPinnedHTTP, payload)
		var response arranger.DeliveryResponse
		if callErr == nil {
			callErr = plugin.Decode(result, &response)
		}
		outcome := effectResult{Status: response.Response.StatusCode, Uncertain: callErr != nil}
		outcome.Retryable = destination.Idempotent && (outcome.Uncertain || outcome.Status >= 500 || outcome.Status == 408 || outcome.Status == 429)
		// Never return provider bodies, headers or reverse errors into workflow state.
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(outcome)
	})
}
