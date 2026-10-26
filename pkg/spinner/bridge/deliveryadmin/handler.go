// Package deliveryadmin exposes spool inspection and failed-delivery redrive
// on a separately configured private listener, never on the public edge.
package deliveryadmin

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/webong/gateway/arranger/automation"
	"github.com/webong/gateway/pkg/spinner/bridge/deliveryqueue"
)

type queue interface {
	Status(string) (deliveryqueue.Status, error)
	List(deliveryqueue.State, int) ([]deliveryqueue.Status, error)
	ListEvent(string, int) ([]deliveryqueue.Status, error)
	Retry(string) error
}

type Handler struct {
	queue      queue
	token      string
	runs       runHistoryReader
	automation automation.Executor
	routes     map[string]http.Handler
	fallback   http.Handler
}

type runHistoryReader interface {
	ReadRun(string) ([]byte, error)
}

type HandlerOption func(*Handler) error

// WithAutomationExecutor exposes synchronous, declarative script execution on
// the private admin listener. Scripts return a request plan; they never receive
// the registered credential or a network client.
func WithAutomationExecutor(executor automation.Executor) HandlerOption {
	return func(handler *Handler) error {
		if executor == nil {
			return errors.New("automation executor is required")
		}
		handler.automation = executor
		return nil
	}
}

// WithShellRunHistory exposes redacted shell-run records on the private admin
// listener. The reader enforces run ID and file safety checks.
func WithShellRunHistory(reader runHistoryReader) HandlerOption {
	return func(handler *Handler) error {
		if reader == nil {
			return errors.New("shell run history reader is required")
		}
		handler.runs = reader
		return nil
	}
}

// WithPrivateRoute mounts one authenticated extension-owned operation on the
// private admin listener without teaching the host its protocol semantics.
func WithPrivateRoute(path string, route http.Handler) HandlerOption {
	return func(handler *Handler) error {
		if route == nil || !strings.HasPrefix(path, "/") || path == "/" || strings.ContainsAny(path, "?#") ||
			privatePathUnder(path, "/deliveries") || privatePathUnder(path, "/shell") || privatePathUnder(path, "/automation") {
			return errors.New("private extension route is invalid or reserved")
		}
		if handler.routes == nil {
			handler.routes = make(map[string]http.Handler)
		}
		if _, exists := handler.routes[path]; exists {
			return errors.New("private extension route is already registered")
		}
		handler.routes[path] = route
		return nil
	}
}

// WithPrivateFallback delegates otherwise-unclaimed private paths to a
// dynamic, protocol-neutral Extension registry. Authentication is always
// completed by this handler before delegation.
func WithPrivateFallback(fallback http.Handler) HandlerOption {
	return func(handler *Handler) error {
		if fallback == nil {
			return errors.New("private extension fallback is required")
		}
		handler.fallback = fallback
		return nil
	}
}

func privatePathUnder(path, prefix string) bool {
	return path == prefix || strings.HasPrefix(path, prefix+"/")
}

func NewHandler(queue queue, token string, options ...HandlerOption) (*Handler, error) {
	if strings.TrimSpace(token) == "" {
		return nil, fmt.Errorf("private admin requires an internal token")
	}
	handler := &Handler{queue: queue, token: token}
	for _, option := range options {
		if err := option(handler); err != nil {
			return nil, err
		}
	}
	if queue == nil && handler.runs == nil && handler.automation == nil && len(handler.routes) == 0 && handler.fallback == nil {
		return nil, errors.New("private admin requires a delivery queue, shell run history, automation executor or extension route")
	}
	return handler, nil
}

func (handler *Handler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	writer.Header().Set("Cache-Control", "no-store")
	provided := strings.TrimPrefix(request.Header.Get("Authorization"), "Bearer ")
	if provided == "" || subtle.ConstantTimeCompare([]byte(provided), []byte(handler.token)) != 1 {
		writeError(writer, http.StatusUnauthorized, "Unauthenticated.")
		return
	}
	path := request.URL.Path
	if route := handler.routes[path]; route != nil {
		route.ServeHTTP(writer, request)
		return
	}
	if path == "/automation/credential-hook" {
		handler.executeCredentialHook(writer, request)
		return
	}
	if strings.HasPrefix(path, "/shell/runs/") {
		if handler.runs == nil {
			http.NotFound(writer, request)
			return
		}
		if request.Method != http.MethodGet {
			methodNotAllowed(writer, http.MethodGet)
			return
		}
		record, err := handler.runs.ReadRun(strings.TrimPrefix(path, "/shell/runs/"))
		if errors.Is(err, os.ErrNotExist) {
			http.NotFound(writer, request)
			return
		}
		if err != nil {
			writeError(writer, http.StatusInternalServerError, "Shell run history is unavailable.")
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write(record)
		return
	}
	if handler.fallback != nil && !privatePathUnder(path, "/deliveries") &&
		!privatePathUnder(path, "/shell") && !privatePathUnder(path, "/automation") {
		handler.fallback.ServeHTTP(writer, request)
		return
	}
	if handler.queue == nil {
		http.NotFound(writer, request)
		return
	}
	if path == "/deliveries" {
		if request.Method != http.MethodGet {
			methodNotAllowed(writer, http.MethodGet)
			return
		}
		state := deliveryqueue.State(request.URL.Query().Get("state"))
		eventID := request.URL.Query().Get("event_id")
		if _, present := request.URL.Query()["event_id"]; present {
			if !validEventID(eventID) || request.URL.Query().Has("state") {
				writeError(writer, http.StatusBadRequest, "Invalid delivery event filter.")
				return
			}
		} else if state == "" {
			state = deliveryqueue.StateFailed
		}
		limit := 50
		if raw := request.URL.Query().Get("limit"); raw != "" {
			parsed, err := strconv.Atoi(raw)
			if err != nil || parsed < 1 || parsed > 100 {
				writeError(writer, http.StatusBadRequest, "Limit must be between 1 and 100.")
				return
			}
			limit = parsed
		}
		var statuses []deliveryqueue.Status
		var err error
		if eventID != "" {
			statuses, err = handler.queue.ListEvent(eventID, limit)
		} else {
			statuses, err = handler.queue.List(state, limit)
		}
		if errors.Is(err, deliveryqueue.ErrInvalidState) {
			writeError(writer, http.StatusBadRequest, "Unsupported delivery state.")
			return
		}
		if err != nil {
			writeError(writer, http.StatusInternalServerError, "Delivery status is unavailable.")
			return
		}
		writeJSON(writer, http.StatusOK, map[string]any{"data": statuses})
		return
	}
	if !strings.HasPrefix(path, "/deliveries/") {
		http.NotFound(writer, request)
		return
	}
	rest := strings.TrimPrefix(path, "/deliveries/")
	if strings.HasSuffix(rest, "/retry") {
		id := strings.TrimSuffix(rest, "/retry")
		if request.Method != http.MethodPost {
			methodNotAllowed(writer, http.MethodPost)
			return
		}
		if request.ContentLength > 0 {
			writeError(writer, http.StatusBadRequest, "Retry does not accept a request body.")
			return
		}
		if err := handler.queue.Retry(id); err != nil {
			writeQueueError(writer, err)
			return
		}
		status, err := handler.queue.Status(id)
		if err != nil {
			writeError(writer, http.StatusInternalServerError, "Delivery status is unavailable.")
			return
		}
		writeJSON(writer, http.StatusAccepted, status)
		return
	}
	if request.Method != http.MethodGet {
		methodNotAllowed(writer, http.MethodGet)
		return
	}
	status, err := handler.queue.Status(rest)
	if err != nil {
		writeQueueError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, status)
}

func (handler *Handler) executeCredentialHook(writer http.ResponseWriter, request *http.Request) {
	if handler.automation == nil {
		http.NotFound(writer, request)
		return
	}
	if request.Method != http.MethodPost {
		methodNotAllowed(writer, http.MethodPost)
		return
	}
	if request.Header.Get("Content-Type") != "application/json" {
		writeError(writer, http.StatusUnsupportedMediaType, "JSON content type is required.")
		return
	}
	var input struct {
		Language string         `json:"language"`
		Source   string         `json:"source"`
		Event    map[string]any `json:"event"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(writer, request.Body, 131072))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil || decoder.Decode(new(any)) != io.EOF ||
		len(input.Language) > 64 || len(input.Source) > 65536 || input.Event == nil {
		writeError(writer, http.StatusBadRequest, "Invalid credential hook.")
		return
	}
	outcome, err := handler.automation.Execute(request.Context(), automation.Script{
		Language: automation.Language(input.Language), Source: input.Source,
	}, automation.Input{Event: input.Event})
	if err != nil || len(outcome.Deliveries) != 1 || len(outcome.Schedules) != 0 || outcome.Response != nil {
		writeError(writer, http.StatusUnprocessableEntity, "Credential hook did not return one request.")
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"delivery": outcome.Deliveries[0]})
}

func validEventID(value string) bool {
	if len(value) < 1 || len(value) > 255 {
		return false
	}
	for _, char := range value {
		if char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' || strings.ContainsRune("._:-", char) {
			continue
		}
		return false
	}
	return true
}

func writeQueueError(writer http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, deliveryqueue.ErrNotFound):
		writeError(writer, http.StatusNotFound, "Delivery not found.")
	case errors.Is(err, deliveryqueue.ErrAlreadyPending):
		writeError(writer, http.StatusConflict, "Delivery is already pending.")
	default:
		writeError(writer, http.StatusInternalServerError, "Delivery operation failed.")
	}
}

func methodNotAllowed(writer http.ResponseWriter, allowed string) {
	writer.Header().Set("Allow", allowed)
	writeError(writer, http.StatusMethodNotAllowed, "Method not allowed.")
}

func writeError(writer http.ResponseWriter, status int, message string) {
	writeJSON(writer, status, map[string]string{"message": message})
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}
