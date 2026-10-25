package spinner

import (
	"bytes"
	"io"
	"net/http"
)

const SubmissionPath = "/email/submissions"

// HandleSubmission keeps Email submission credentials out of Web route
// planning. It forwards only the exact public Email submission route to the
// Planner entrypoint and bounds the JSON/base64 envelope before forwarding.
func HandleSubmission(writer http.ResponseWriter, request *http.Request, planner http.Handler, maxMessageSize int64) bool {
	if request.URL.Path != SubmissionPath {
		return false
	}
	if planner == nil {
		http.Error(writer, "email submission is not configured", http.StatusServiceUnavailable)
		return true
	}
	limit := maxMessageSize*2 + 65536
	if maxMessageSize <= 0 {
		limit = 20*1024*1024 + 65536
	}
	body, err := io.ReadAll(io.LimitReader(request.Body, limit+1))
	if err != nil || int64(len(body)) > limit {
		http.Error(writer, "email submission body is too large or invalid", http.StatusRequestEntityTooLarge)
		return true
	}
	request.Body = io.NopCloser(bytes.NewReader(body))
	planner.ServeHTTP(writer, request)
	return true
}

// SubmissionHandler composes Email's public submission route in front of the
// Web edge without teaching the Web extension about Email paths or limits.
func SubmissionHandler(next, planner http.Handler, maxMessageSize int64) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if HandleSubmission(writer, request, planner, maxMessageSize) {
			return
		}
		if next == nil {
			http.NotFound(writer, request)
			return
		}
		next.ServeHTTP(writer, request)
	})
}
