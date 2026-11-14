package rpc

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"
)

const maxRequestBytes = 1 << 20

type Error struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type Invoker interface {
	Invoke(context.Context, string, json.RawMessage) (json.RawMessage, *Error)
}

type Handler struct {
	invoker     Invoker
	token       string
	credentials map[string]string
}

func NewHandler(invoker Invoker, token string) (*Handler, error) {
	if invoker == nil || token == "" {
		return nil, errors.New("the RPC handler requires an invoker and bearer token")
	}

	return &Handler{invoker: invoker, token: token}, nil
}

type principalKey struct{}

// Principal is set only by authenticated transport code, never RPC params.
func Principal(ctx context.Context) string {
	value, _ := ctx.Value(principalKey{}).(string)
	return value
}

func NewScopedHandler(invoker Invoker, credentials map[string]string) (*Handler, error) {
	if invoker == nil || len(credentials) == 0 {
		return nil, errors.New("scoped RPC credentials are required")
	}
	copied := make(map[string]string, len(credentials))
	seen := make(map[string]bool, len(credentials))
	for account, token := range credentials {
		if account == "" || len(token) < 32 || seen[token] {
			return nil, errors.New("invalid or ambiguous scoped RPC credentials")
		}
		seen[token] = true
		copied[account] = token
	}
	return &Handler{invoker: invoker, credentials: copied}, nil
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	provided := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	principal := ""
	authenticated := false
	if h.credentials == nil {
		authenticated = subtle.ConstantTimeCompare([]byte(provided), []byte(h.token)) == 1
	} else {
		for account, token := range h.credentials {
			if subtle.ConstantTimeCompare([]byte(provided), []byte(token)) == 1 {
				principal = account
				authenticated = true
			}
		}
	}
	if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") || !authenticated {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	requestContext := context.WithValue(r.Context(), principalKey{}, principal)
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		http.Error(w, "Expected application/json", http.StatusUnsupportedMediaType)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, maxRequestBytes+1))
	if err != nil {
		http.Error(w, "Unable to read request", http.StatusBadRequest)
		return
	}
	if len(body) > maxRequestBytes {
		http.Error(w, "Request too large", http.StatusRequestEntityTooLarge)
		return
	}

	var payload json.RawMessage
	if err := json.Unmarshal(body, &payload); err != nil {
		h.writeJSON(w, response{Version: "2.0", ID: json.RawMessage("null"), Error: &Error{-32700, "Parse error"}})
		return
	}

	if len(payload) > 0 && payload[0] == '[' {
		var batch []json.RawMessage
		if err := json.Unmarshal(payload, &batch); err != nil || len(batch) == 0 {
			h.writeJSON(w, response{Version: "2.0", ID: json.RawMessage("null"), Error: &Error{-32600, "Invalid Request"}})
			return
		}
		results := make([]response, 0, len(batch))
		for _, item := range batch {
			if result := h.process(requestContext, item); result != nil {
				results = append(results, *result)
			}
		}
		if len(results) == 0 {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		h.writeJSON(w, results)
		return
	}

	result := h.process(requestContext, payload)
	if result == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	h.writeJSON(w, result)
}

type response struct {
	Version string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *Error          `json:"error,omitempty"`
}

func (h *Handler) process(ctx context.Context, payload json.RawMessage) *response {
	invalid := &response{Version: "2.0", ID: json.RawMessage("null"), Error: &Error{-32600, "Invalid Request"}}
	var fields map[string]json.RawMessage
	if len(payload) == 0 || payload[0] != '{' || json.Unmarshal(payload, &fields) != nil {
		return invalid
	}

	var version, method string
	if json.Unmarshal(fields["jsonrpc"], &version) != nil || version != "2.0" ||
		json.Unmarshal(fields["method"], &method) != nil || method == "" {
		return invalid
	}

	id, hasID := fields["id"]
	if hasID && !validID(id) {
		return invalid
	}
	if hasID {
		invalid.ID = id
	}

	// State changes require a request ID so their outcome can be acknowledged.
	if !hasID && (method == "flow.start" || method == "flow.complete" || method == "flow.resume" || method == "flow.cancel") {
		return nil
	}

	params := fields["params"]
	if len(params) == 0 {
		params = json.RawMessage("{}")
	}
	if params[0] != '{' {
		if !hasID {
			return nil
		}
		return &response{Version: "2.0", ID: id, Error: &Error{-32602, "Invalid params"}}
	}

	result, callError := h.invoker.Invoke(ctx, method, params)
	if !hasID {
		return nil
	}
	if callError != nil {
		return &response{Version: "2.0", ID: id, Error: callError}
	}
	if len(result) == 0 || !json.Valid(result) {
		return &response{Version: "2.0", ID: id, Error: &Error{-32603, "Internal error"}}
	}
	return &response{Version: "2.0", ID: id, Result: result}
}

func validID(id json.RawMessage) bool {
	if len(id) == 0 {
		return false
	}
	if id[0] == '"' || id[0] == 'n' {
		return true
	}
	if id[0] >= '0' && id[0] <= '9' || id[0] == '-' {
		return true
	}
	return false
}

func (h *Handler) writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(value)
}
