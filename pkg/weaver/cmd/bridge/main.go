package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/dunglas/frankenphp"
	"github.com/webong/gateway/pkg/weaver/internal/rpc"
)

type phpInvoker struct {
	root string
}

func (p phpInvoker) Invoke(ctx context.Context, method string, params json.RawMessage) (json.RawMessage, *rpc.Error) {
	body, err := json.Marshal(struct {
		Principal string          `json:"principal"`
		Method    string          `json:"method"`
		Params    json.RawMessage `json:"params"`
	}{Principal: rpc.Principal(ctx), Method: method, Params: params})
	if err != nil {
		return nil, &rpc.Error{Code: -32603, Message: "Internal error"}
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://localhost/index.php", bytes.NewReader(body))
	if err != nil {
		return nil, &rpc.Error{Code: -32603, Message: "Internal error"}
	}
	request.Header.Set("Content-Type", "application/json")
	request, err = frankenphp.NewRequestWithContext(request, frankenphp.WithRequestDocumentRoot(p.root, false))
	if err != nil {
		return nil, &rpc.Error{Code: -32603, Message: "Internal error"}
	}
	response := httptest.NewRecorder()
	if err := frankenphp.ServeHTTP(response, request); err != nil || response.Code != http.StatusOK {
		slog.Error("PHP RPC invocation failed", "status", response.Code)
		return nil, &rpc.Error{Code: -32603, Message: "Internal error"}
	}

	var result struct {
		Result json.RawMessage `json:"result"`
		Error  *rpc.Error      `json:"error"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil || result.Error == nil && len(result.Result) == 0 {
		slog.Error("PHP RPC returned an invalid response")
		return nil, &rpc.Error{Code: -32603, Message: "Internal error"}
	}
	return result.Result, result.Error
}

func main() {
	if err := run(); err != nil {
		slog.Error("workflowd stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	contextPath := os.Getenv("GATEWAY_WEAVER_CONTEXT")
	raw, err := os.ReadFile(contextPath)
	if err != nil {
		return errors.New("private WorkFlow context is unavailable")
	}
	var config struct {
		Authentication map[string]string `json:"authentication"`
		Configuration  struct {
			Accounts []struct {
				ID string `json:"id"`
			} `json:"accounts"`
		} `json:"configuration"`
	}
	if json.Unmarshal(raw, &config) != nil {
		return errors.New("invalid private WorkFlow context")
	}
	if len(config.Authentication) != len(config.Configuration.Accounts) {
		return errors.New("Weaver accounts and credentials do not match")
	}
	for _, account := range config.Configuration.Accounts {
		if _, ok := config.Authentication[account.ID]; !ok {
			return errors.New("Weaver account credential unavailable")
		}
	}
	root := os.Getenv("GATEWAY_WEAVER_PHP_PUBLIC")
	if root == "" {
		root = "public"
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(root, "index.php")); err != nil {
		return fmt.Errorf("workflow PHP bridge: %w", err)
	}

	if err := frankenphp.Init(); err != nil {
		return err
	}
	defer frankenphp.Shutdown()

	invoker := phpInvoker{root: root}
	if _, readinessError := invoker.Invoke(context.Background(), "workflow.ready", json.RawMessage("{}")); readinessError != nil {
		return errors.New("WorkFlow RPC/storage readiness failed")
	}
	handler, err := rpc.NewScopedHandler(invoker, config.Authentication)
	if err != nil {
		return err
	}
	mux := http.NewServeMux()
	mux.Handle("/rpc", handler)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})

	listen := os.Getenv("GATEWAY_WEAVER_RPC_LISTEN")
	if listen == "" {
		listen = "127.0.0.1:8080"
	}
	server := &http.Server{
		Addr:              listen,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	listener, err := net.Listen("tcp", listen)
	if err != nil {
		return err
	}
	slog.Info("Weaver JSON-RPC listening", "address", listen)
	return rpc.Serve(ctx, server, listener, 3*time.Second)
}
