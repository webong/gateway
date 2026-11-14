// Package runtime supervises Weaver's verified PHP bridge and queue worker.
package runtime

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	plugin "github.com/webong/ext/pkg/plugin-go/contract"
	arranger "github.com/webong/gateway/arranger/integration"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	goruntime "runtime"
	"strconv"
	"sync"
	"syscall"
	"time"
)

var secretReference = regexp.MustCompile(`^[a-z][a-z0-9._-]{0,63}$`)

func New(root string) *Service { return &Service{root: root, stopped: make(chan struct{})} }

// Serve retains cooperative stop responses until the host closes the session.
func (s *Service) Serve(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	defer s.close()
	go func() {
		select {
		case <-s.stopped:
			s.mu.Lock()
			cooperative := s.cooperative
			s.mu.Unlock()
			if !cooperative {
				cancel()
				_ = os.Stdin.Close()
			}
		case <-ctx.Done():
		}
	}()
	return arranger.ServeRuntime(ctx, s)
}

type child struct {
	cmd  *exec.Cmd
	done chan struct{}
}
type Service struct {
	root        string
	ini         string
	iniDir      string
	contextFile string
	mu          sync.Mutex
	children    []*child
	server      *http.Server
	once        sync.Once
	closed      bool
	cooperative bool
	stopped     chan struct{}
}

func (s *Service) php(args ...string) *exec.Cmd {
	all := append([]string{"--library-path", filepath.Join(s.root, "runtime/lib"), filepath.Join(s.root, "runtime/bin/php"), "-c", s.ini}, args...)
	return exec.Command(filepath.Join(s.root, "runtime/loader"), all...)
}
func (s *Service) launch(cmd *exec.Cmd) (*child, error) {
	cmd.Stderr = os.Stderr
	c := &child{cmd: cmd, done: make(chan struct{})}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, errors.New("runtime is closed")
	}
	started := make(chan error, 1)
	go func() {
		goruntime.LockOSThread()
		defer goruntime.UnlockOSThread()
		configureChild(cmd)
		err := cmd.Start()
		started <- err
		if err != nil {
			return
		}
		_ = cmd.Wait()
		close(c.done)
		s.close()
	}()
	if err := <-started; err != nil {
		return nil, errors.New("bundled runtime child failed to start")
	}
	s.children = append(s.children, c)
	return c, nil
}
func (s *Service) Stop(context.Context) error {
	s.mu.Lock()
	s.cooperative = true
	s.mu.Unlock()
	s.close()
	return nil
}
func (s *Service) close() {
	s.once.Do(func() {
		s.mu.Lock()
		s.closed = true
		if s.iniDir != "" {
			defer os.RemoveAll(s.iniDir)
		}
		if s.stopped != nil {
			defer close(s.stopped)
		}
		server := s.server
		children := append([]*child(nil), s.children...)
		s.mu.Unlock()
		if server != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			if server.Shutdown(ctx) != nil {
				_ = server.Close()
			}
			cancel()
		}
		for _, c := range children {
			_ = c.cmd.Process.Signal(syscall.SIGTERM)
		}
		deadline := time.NewTimer(5 * time.Second)
		defer deadline.Stop()
		for _, c := range children {
			select {
			case <-c.done:
			case <-deadline.C:
				for _, c := range children {
					_ = c.cmd.Process.Kill()
				}
				return
			}
		}
	})
}
func (s *Service) Start(ctx context.Context, request arranger.RuntimeStartRequest, caller plugin.ReverseCaller) (result arranger.RuntimeStartResponse, err error) {
	defer func() {
		if err != nil {
			s.close()
		}
	}()
	var settings struct {
		SchemaVersion           int    `json:"schema_version"`
		DatabaseReference       string `json:"database_ref"`
		AuthenticationReference string `json:"authentication_ref"`
	}
	if json.Unmarshal(request.Configuration, &settings) != nil || settings.SchemaVersion != 1 ||
		!secretReference.MatchString(settings.DatabaseReference) || !secretReference.MatchString(settings.AuthenticationReference) {
		return result, errors.New("invalid Weaver runtime configuration or secret references")
	}
	s.iniDir, err = os.MkdirTemp("", "workflow-runtime-")
	if err != nil {
		return result, errors.New("runtime settings directory unavailable")
	}
	s.ini = filepath.Join(s.iniDir, "php.ini")
	if err = os.WriteFile(s.ini, []byte("extension_dir="+strconv.Quote(filepath.Join(s.root, "runtime/lib"))+"\nlog_errors=On\ndisplay_errors=Off\n"), 0600); err != nil {
		return result, errors.New("runtime settings unavailable")
	}
	// Probe the bundled executable, not PATH or the host's PHP installation.
	probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cmd := s.php("-r", `if (PHP_VERSION_ID < 80300 || PHP_VERSION_ID >= 80400 || !extension_loaded('pcntl') || array_diff(['sqlite','pgsql','mysql'], PDO::getAvailableDrivers())) exit(1); echo 'ready';`)
	cmd.Cancel = nil
	cmd.Env = s.environment()
	cmd.Stderr = io.Discard
	// CommandContext is avoided here only to retain the same trusted argv builder.
	if err := cmd.Start(); err != nil {
		return result, errors.New("bundled PHP 8.3/PDO runtime is unavailable")
	}
	probeDone := make(chan error, 1)
	go func() { probeDone <- cmd.Wait() }()
	select {
	case e := <-probeDone:
		if e != nil {
			return result, errors.New("bundled PHP 8.3/PDO runtime requirements not met")
		}
	case <-probeCtx.Done():
		_ = cmd.Process.Kill()
		<-probeDone
		return result, errors.New("bundled runtime probe timed out")
	}
	host := arranger.NewRuntimeHost(caller, nil)
	database, e := host.Secrets().Resolve(ctx, settings.DatabaseReference)
	if e != nil {
		return result, errors.New("Weaver database secret unavailable")
	}
	authentication, e := host.Secrets().Resolve(ctx, settings.AuthenticationReference)
	if e != nil {
		return result, errors.New("Weaver authentication secret unavailable")
	}
	if !json.Valid(database.Value) || !json.Valid(authentication.Value) {
		return result, errors.New("invalid Weaver scoped secret value")
	}
	contextDocument, e := json.Marshal(struct {
		Configuration  json.RawMessage `json:"configuration"`
		Database       json.RawMessage `json:"database"`
		Authentication json.RawMessage `json:"authentication"`
	}{request.Configuration, database.Value, authentication.Value})
	if e != nil {
		return result, errors.New("invalid Weaver runtime context")
	}
	s.contextFile = filepath.Join(s.iniDir, "context.json")
	if e = os.WriteFile(s.contextFile, contextDocument, 0600); e != nil {
		return result, errors.New("private Weaver runtime context unavailable")
	}
	// Full configuration/storage/authentication validation precedes any worker.
	checkCtx, checkCancel := context.WithTimeout(ctx, 10*time.Second)
	checkArgs := s.php(filepath.Join(s.root, "bin/readiness"))
	check := exec.CommandContext(checkCtx, checkArgs.Path, checkArgs.Args[1:]...)
	check.Env = s.environment()
	goruntime.LockOSThread()
	configureChild(check)
	checkErr := check.Run()
	goruntime.UnlockOSThread()
	checkCancel()
	if checkErr != nil {
		return result, errors.New("Weaver configuration, scoped credentials or operational storage is not ready")
	}
	// Operational schema installation is an explicit operator action, never activation.
	backendListener, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		return result, e
	}
	backend := backendListener.Addr().String()
	_ = backendListener.Close()
	rpc := exec.Command(filepath.Join(s.root, "runtime/loader"), "--library-path", filepath.Join(s.root, "runtime/lib"), filepath.Join(s.root, "runtime/bin/workflowd"))
	rpc.Env = append(s.environment(), "GATEWAY_WEAVER_RPC_LISTEN="+backend)
	rpc.Stdout = os.Stderr
	rpcChild, e := s.launch(rpc)
	if e != nil {
		return result, e
	}
	client := &http.Client{Timeout: time.Second}
	until := time.Now().Add(10 * time.Second)
	for {
		req, _ := http.NewRequestWithContext(ctx, "GET", "http://"+backend+"/healthz", nil)
		response, e := client.Do(req)
		healthy := false
		if e == nil {
			healthy = response.StatusCode == http.StatusNoContent
			_ = response.Body.Close()
		}
		if healthy {
			break
		}
		if time.Now().After(until) {
			return result, errors.New("RPC/PDO readiness timed out")
		}
		select {
		case <-rpcChild.done:
			return result, errors.New("RPC bridge exited")
		case <-ctx.Done():
			return result, ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	// Revalidate capabilities after bootstrap, before the first worker claim.
	freshDatabase, dbErr := host.Secrets().Resolve(ctx, settings.DatabaseReference)
	freshAuthentication, authErr := host.Secrets().Resolve(ctx, settings.AuthenticationReference)
	if dbErr != nil || authErr != nil || freshDatabase.Version != database.Version || freshAuthentication.Version != authentication.Version ||
		!bytes.Equal(freshDatabase.Value, database.Value) || !bytes.Equal(freshAuthentication.Value, authentication.Value) {
		return result, errors.New("Weaver scoped secrets changed during activation")
	}
	worker := s.php(filepath.Join(s.root, "bin/worker"))
	worker.Env = s.environment()
	pipe, e := worker.StdoutPipe()
	if e != nil {
		return result, e
	}
	workerChild, e := s.launch(worker)
	if e != nil {
		return result, e
	}
	ready := make(chan bool, 1)
	go func() {
		scanner := bufio.NewScanner(pipe)
		ready <- scanner.Scan() && scanner.Text() == "READY"
		_, _ = io.Copy(io.Discard, pipe)
	}()
	select {
	case ok := <-ready:
		if !ok {
			return result, errors.New("queue worker bootstrap failed")
		}
	case <-time.After(10 * time.Second):
		return result, errors.New("queue worker readiness timed out")
	case <-ctx.Done():
		return result, ctx.Err()
	}
	listener, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		return result, e
	}
	target, _ := url.Parse("http://" + backend)
	proxy := httputil.NewSingleHostReverseProxy(target)
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/workflow/rpc" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("X-Gateway-Internal") != "" || r.Header.Get("X-Gateway-Target") != "" || r.Header.Get(arranger.PrivateHTTPAuthorizationHeader) != "" {
			http.Error(w, "Invalid private credential handoff", http.StatusBadRequest)
			return
		}
		r.URL.Path = "/rpc"
		proxy.ServeHTTP(w, r)
	})
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		_ = listener.Close()
		return result, errors.New("runtime closed before advertisement")
	}
	s.server = &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 30 * time.Second}
	server := s.server
	s.mu.Unlock()
	select {
	case <-rpcChild.done:
		_ = listener.Close()
		return result, errors.New("RPC bridge exited before readiness")
	case <-workerChild.done:
		_ = listener.Close()
		return result, errors.New("queue worker exited before readiness")
	default:
	}
	go func() {
		if e := server.Serve(listener); e != nil && !errors.Is(e, http.ErrServerClosed) {
			s.close()
		}
	}()
	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-s.stopped:
				return
			case <-ticker.C:
				checkCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				currentDatabase, dbErr := host.Secrets().Resolve(checkCtx, settings.DatabaseReference)
				currentAuthentication, authErr := host.Secrets().Resolve(checkCtx, settings.AuthenticationReference)
				cancel()
				if dbErr != nil || authErr != nil || currentDatabase.Version != database.Version || currentAuthentication.Version != authentication.Version ||
					!bytes.Equal(currentDatabase.Value, database.Value) || !bytes.Equal(currentAuthentication.Value, authentication.Value) {
					s.close()
					return
				}
			}
		}
	}()
	return arranger.RuntimeStartResponse{PrivateHTTP: []arranger.HTTPMount{{Prefix: "/workflow/rpc", Address: "http://" + listener.Addr().String()}}}, nil
}
func (s *Service) environment() []string {
	return []string{
		"PATH=/nonexistent", "HOME=" + s.iniDir, "TMPDIR=/tmp",
		"LD_LIBRARY_PATH=" + filepath.Join(s.root, "runtime/lib"), "PHPRC=" + s.ini,
		"PHP_INI_SCAN_DIR=" + filepath.Join(s.root, "runtime/php.d"),
		"GATEWAY_WEAVER_CONTEXT=" + s.contextFile,
		"GATEWAY_WEAVER_PHP_PUBLIC=" + filepath.Join(s.root, "public"),
	}
}
