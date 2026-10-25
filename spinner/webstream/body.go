package webstream

import (
	"compress/gzip"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"
)

var errEncoding = errors.New("unsupported content encoding")

// Both wire bytes and decoded bytes are independently bounded.
func requestBody(w http.ResponseWriter, r *http.Request, limit int64) (io.ReadCloser, error) {
	encoding := strings.ToLower(strings.TrimSpace(strings.Join(r.Header.Values("Content-Encoding"), ",")))
	if encoding != "" && encoding != "identity" && encoding != "gzip" {
		return nil, errEncoding
	}
	wire := http.MaxBytesReader(w, r.Body, limit)
	if encoding != "gzip" {
		return wire, nil
	}
	decoded, err := gzip.NewReader(wire)
	if err != nil {
		wire.Close()
		return nil, err
	}
	return &decodedBody{Reader: http.MaxBytesReader(w, decoded, limit), wire: wire}, nil
}

type decodedBody struct {
	io.Reader
	wire io.Closer
}

func (b *decodedBody) Close() error { return b.wire.Close() }

func bodyStatus(err error) int {
	var large *http.MaxBytesError
	if errors.As(err, &large) {
		return http.StatusRequestEntityTooLarge
	}
	if errors.Is(err, errEncoding) {
		return http.StatusUnsupportedMediaType
	}
	if timeout, ok := err.(interface{ Timeout() bool }); ok && timeout.Timeout() {
		return http.StatusRequestTimeout
	}
	return http.StatusBadRequest
}

func normalizedBodyHeaders(r *http.Request) http.Header {
	headers := r.Header.Clone()
	if strings.EqualFold(r.Header.Get("Content-Encoding"), "gzip") {
		for _, name := range []string{"Content-MD5", "Digest", "Content-Digest", "Repr-Digest"} {
			headers.Del(name)
		}
	}
	for _, name := range []string{"Content-Encoding", "Content-Length", "Transfer-Encoding", "Trailer", "X-Gateway-Upload-Mode"} {
		headers.Del(name)
	}
	return headers
}

// Upload reads are serialized by net/http. A stalled or invalid upload cancels
// the source, including when response streaming has already begun.
type uploadBody struct {
	io.ReadCloser
	controller *http.ResponseController
	idle       time.Duration
	deadline   time.Time
	cancel     func()
	completed  func()
}

func (b *uploadBody) Read(p []byte) (int, error) {
	deadline := time.Now().Add(b.idle)
	if b.deadline.Before(deadline) {
		deadline = b.deadline
	}
	_ = b.controller.SetReadDeadline(deadline)
	n, err := b.ReadCloser.Read(p)
	if err != nil && err != io.EOF {
		b.cancel()
	}
	if err == io.EOF {
		b.completed()
		_ = b.controller.SetReadDeadline(time.Time{})
	}
	return n, err
}
