package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
	"strings"

	plugin "github.com/webong/ext/pkg/plugin-go/contract"
	"github.com/webong/gateway/arranger/integration"
)

// HandleHostOperation serves Shell's package-owned private run-history mount.
// Spinner authenticates the private listener and routes only advertised mounts.
func HandleHostOperation(_ context.Context, request plugin.Request) (json.RawMessage, error) {
	if request.Contract != integration.RuntimeContract || request.Operation != integration.OperationStart {
		return nil, plugin.ErrUnsupported
	}
	var start integration.RuntimeStartRequest
	if err := plugin.Decode(request.Payload, &start); err != nil {
		return nil, plugin.ErrInvalid
	}
	engine, err := NewFromEnvironment()
	if err != nil {
		return nil, err
	}
	response := integration.RuntimeStartResponse{}
	if engine.RunHistoryConfigured() {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return nil, err
		}
		server := &http.Server{Handler: runHistoryHandler(engine)}
		go func() { _ = server.Serve(listener) }()
		response.PrivateHTTP = []integration.HTTPMount{{
			Prefix:  "/shell/runs",
			Address: "http://" + listener.Addr().String(),
		}}
	}
	return json.Marshal(response)
}

func runHistoryHandler(engine *Engine) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		const prefix = "/shell/runs/"
		if request.Method != http.MethodGet {
			writer.Header().Set("Allow", http.MethodGet)
			http.Error(writer, "Method not allowed.", http.StatusMethodNotAllowed)
			return
		}
		if !strings.HasPrefix(request.URL.Path, prefix) {
			http.NotFound(writer, request)
			return
		}
		id := strings.TrimPrefix(request.URL.Path, prefix)
		if !runRecordFilename(id + ".json") {
			http.NotFound(writer, request)
			return
		}
		record, err := engine.ReadRun(id)
		if errors.Is(err, os.ErrNotExist) {
			http.NotFound(writer, request)
			return
		}
		if err != nil {
			http.Error(writer, "Extension run history is unavailable.", http.StatusInternalServerError)
			return
		}
		writer.Header().Set("Cache-Control", "no-store")
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write(record)
	})
}
