// Package runtime embeds the bundled PHP Planner with FrankenPHP's Go library.
// It does not own the Spinner listener or depend on its standalone command.
package runtime

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/dunglas/frankenphp"
)

// Runtime owns one process-wide FrankenPHP lifecycle and serves the Planner
// front controller as an in-process HTTP handler.
type Runtime struct {
	publicDir string
}

func Start(appDir string) (*Runtime, error) {
	publicDir, err := filepath.Abs(filepath.Join(appDir, "public"))
	if err != nil {
		return nil, fmt.Errorf("resolve Planner public directory: %w", err)
	}
	index := filepath.Join(publicDir, "index.php")
	if info, err := os.Stat(index); err != nil || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("Planner front controller is missing: %s", index)
	}
	if err := frankenphp.Init(); err != nil {
		return nil, fmt.Errorf("initialize embedded FrankenPHP: %w", err)
	}
	return &Runtime{publicDir: publicDir}, nil
}

func (r *Runtime) ServeHTTP(w http.ResponseWriter, request *http.Request) {
	// CGI maps hyphens and underscores to the same name. Discard ambiguous
	// client headers before they enter the PHP request environment.
	request = request.Clone(request.Context())
	for name := range request.Header {
		if strings.ContainsRune(name, '_') {
			delete(request.Header, name)
		}
	}
	requestURI := request.URL.RequestURI()
	request.URL.Path = "/index.php"
	request.URL.RawPath = ""

	prepared, err := frankenphp.NewRequestWithContext(request,
		frankenphp.WithRequestDocumentRoot(r.publicDir, false),
		frankenphp.WithRequestEnv(map[string]string{"REQUEST_URI": requestURI}),
	)
	if err != nil {
		http.Error(w, "Planner request preparation failed", http.StatusInternalServerError)
		return
	}
	if err := frankenphp.ServeHTTP(w, prepared); err != nil {
		http.Error(w, "Planner execution failed", http.StatusBadGateway)
	}
}

func (r *Runtime) Close() {
	frankenphp.Shutdown()
}

var _ http.Handler = (*Runtime)(nil)
