// Runtime feasibility prototype only; extraction is gated by issue #18.
package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	plugin "github.com/webong/ext/pkg/plugin-go/contract"
	"github.com/webong/ext/pkg/plugin-go/contract/host"
	"github.com/webong/ext/pkg/plugin-go/contract/host/store"
	packagekit "github.com/webong/ext/pkg/plugin-go/contract/package"
	arranger "github.com/webong/gateway/arranger/integration"
)

type child struct {
	cmd  *exec.Cmd
	done chan struct{}
}
type service struct {
	root        string
	ini         string
	iniDir      string
	mu          sync.Mutex
	children    []*child
	server      *http.Server
	once        sync.Once
	closed      bool
	cooperative bool
	stopped     chan struct{}
}

func (s *service) php(args ...string) *exec.Cmd {
	all := append([]string{"--library-path", filepath.Join(s.root, "runtime/lib"), filepath.Join(s.root, "runtime/bin/php"), "-c", s.ini}, args...)
	return exec.Command(filepath.Join(s.root, "runtime/loader"), all...)
}
func (s *service) launch(cmd *exec.Cmd) (*child, error) {
	cmd.Stderr = os.Stderr
	c := &child{cmd: cmd, done: make(chan struct{})}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, errors.New("runtime is closed")
	}
	started := make(chan error, 1)
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
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
func (s *service) Stop(context.Context) error {
	s.mu.Lock()
	s.cooperative = true
	s.mu.Unlock()
	s.close()
	return nil
}
func (s *service) close() {
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
func (s *service) Start(ctx context.Context, request arranger.RuntimeStartRequest, caller plugin.ReverseCaller) (result arranger.RuntimeStartResponse, err error) {
	defer func() {
		if err != nil {
			s.close()
		}
	}()
	if len(request.Configuration) > 0 && string(request.Configuration) != "{}" {
		return result, errors.New("prototype accepts no operator code or runtime paths")
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
	cmd.Env = s.environment("")
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
	secret, e := arranger.NewRuntimeHost(caller, nil).Secrets().Resolve(ctx, "workflow-runtime")
	if e != nil {
		return result, errors.New("workflow runtime secret unavailable")
	}
	var credentials struct {
		Token string `json:"rpcToken"`
	}
	if json.Unmarshal(secret.Value, &credentials) != nil || len(credentials.Token) < 16 {
		return result, errors.New("workflow runtime secret invalid")
	}
	// State and bootstrap are fixed trusted feasibility fixtures, never request-selected.
	if err = os.MkdirAll("/state", 0700); err != nil {
		return result, errors.New("prototype state directory unavailable")
	}
	installArgs := s.php(filepath.Join(s.root, "gate/install.php"))
	install := exec.CommandContext(ctx, installArgs.Path, installArgs.Args[1:]...)
	install.Env = s.environment(credentials.Token)
	if out, e := install.CombinedOutput(); e != nil {
		_ = out
		return result, errors.New("prototype operational store initialization failed")
	}
	backendListener, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		return result, e
	}
	backend := backendListener.Addr().String()
	_ = backendListener.Close()
	rpc := exec.Command(filepath.Join(s.root, "runtime/loader"), "--library-path", filepath.Join(s.root, "runtime/lib"), filepath.Join(s.root, "runtime/bin/workflowd"))
	rpc.Env = append(s.environment(credentials.Token), "WORKFLOW_RPC_LISTEN="+backend)
	rpc.Stdout = os.Stderr
	rpcChild, e := s.launch(rpc)
	if e != nil {
		return result, e
	}
	worker := s.php(filepath.Join(s.root, "gate/worker.php"))
	worker.Env = s.environment(credentials.Token)
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
	client := &http.Client{Timeout: time.Second}
	until := time.Now().Add(10 * time.Second)
	for {
		req, _ := http.NewRequestWithContext(ctx, "POST", "http://"+backend+"/rpc", bytes.NewBufferString(`{"jsonrpc":"2.0","id":1,"method":"flow.get","params":{"flow_key":"gate_order","run_id":"readiness","subject":{"type":"gate","id":"ready"}}}`))
		req.Header.Set("Authorization", "Bearer "+credentials.Token)
		req.Header.Set("Content-Type", "application/json")
		response, e := client.Do(req)
		healthy := false
		if e == nil {
			var data map[string]json.RawMessage
			healthy = response.StatusCode == 200 && json.NewDecoder(response.Body).Decode(&data) == nil && data["error"] == nil
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
		case <-workerChild.done:
			return result, errors.New("queue worker exited")
		case <-ctx.Done():
			return result, ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
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
				current, e := arranger.NewRuntimeHost(caller, nil).Secrets().Resolve(checkCtx, "workflow-runtime")
				cancel()
				if e != nil || current.Version != secret.Version || !bytes.Equal(current.Value, secret.Value) {
					s.close()
					return
				}
			}
		}
	}()
	return arranger.RuntimeStartResponse{PrivateHTTP: []arranger.HTTPMount{{Prefix: "/workflow/rpc", Address: "http://" + listener.Addr().String()}}}, nil
}
func (s *service) environment(token string) []string {
	return []string{
		"PATH=/nonexistent", "HOME=/state", "TMPDIR=/tmp", "LD_LIBRARY_PATH=" + filepath.Join(s.root, "runtime/lib"), "PHPRC=" + s.ini, "PHP_INI_SCAN_DIR=" + filepath.Join(s.root, "runtime/php.d"),
		"WORKFLOW_RPC_TOKEN=" + token, "WORKFLOW_BOOTSTRAP=" + filepath.Join(s.root, "app/app/bootstrap.example.php"), "WORKFLOW_RUNTIME_BOOTSTRAP=" + filepath.Join(s.root, "gate/runtime.php"), "WORKFLOW_PHP_PUBLIC=" + filepath.Join(s.root, "app/app/public"),
	}
}

type fixtureSecrets struct{}

func (fixtureSecrets) Call(_ context.Context, ref plugin.ContractRef, op string, payload json.RawMessage) (json.RawMessage, error) {
	if ref != arranger.SecretContract || op != arranger.OperationResolveSecret {
		return nil, plugin.ErrUnsupported
	}
	var req arranger.SecretRequest
	if json.Unmarshal(payload, &req) != nil || req.Reference != "workflow-runtime" {
		return nil, plugin.ErrInvalid
	}
	return json.Marshal(arranger.SecretValue{Reference: req.Reference, Version: 1, Value: json.RawMessage(`{"rpcToken":"disposable-weaver-runtime-gate-token"}`)})
}
func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	root, err := os.Getwd()
	if err != nil {
		panic(err)
	}
	raw, e := os.ReadFile(filepath.Join(root, "plugin.json"))
	if e != nil {
		fmt.Fprintln(os.Stderr, "verified runtime manifest unavailable")
		os.Exit(1)
	}
	manifest, e := packagekit.Decode(raw)
	if e != nil || manifest.VerifyArtifacts(root) != nil {
		fmt.Fprintln(os.Stderr, "runtime artifact verification failed")
		os.Exit(1)
	}
	if len(os.Args) == 2 && os.Args[1] == "--kill-runtime-guest" {
		entries, _ := os.ReadDir("/proc")
		killed := 0
		for _, entry := range entries {
			pid, e := strconv.Atoi(entry.Name())
			if e != nil || pid == os.Getpid() {
				continue
			}
			raw, e := os.ReadFile(filepath.Join("/proc", entry.Name(), "cmdline"))
			if e != nil {
				continue
			}
			args := strings.Split(strings.TrimRight(string(raw), "\x00"), "\x00")
			if len(args) == 1 && args[0] == filepath.Join(root, "bin/spinner") {
				process, _ := os.FindProcess(pid)
				if process.Kill() == nil {
					killed++
				}
			}
		}
		if killed != 1 {
			fmt.Fprintln(os.Stderr, "expected exactly one runtime guest")
			os.Exit(1)
		}
		fmt.Println("Terminated the disposable runtime guest for parent-death acceptance.")
		return
	}
	if len(os.Args) == 2 && (os.Args[1] == "--session-probe" || os.Args[1] == "--session-no-secret") {
		deny := os.Args[1] == "--session-no-secret"
		proc, e := host.Open(ctx, store.Installed{Manifest: manifest, Directory: root}, host.Options{Timeout: 30 * time.Second, Reverse: &plugin.ReverseOptions{
			Authorize: func(_ context.Context, r plugin.Request) error {
				if deny || r.Plugin.ID != manifest.Descriptor.Identity.ID || r.Contract != arranger.SecretContract || r.Operation != arranger.OperationResolveSecret {
					return plugin.ErrDenied
				}
				return nil
			},
			Handle: func(ctx context.Context, r plugin.Request) (json.RawMessage, error) {
				return (fixtureSecrets{}).Call(ctx, r.Contract, r.Operation, r.Payload)
			},
		}})
		if e != nil {
			fmt.Fprintln(os.Stderr, "verified Ext session could not open")
			os.Exit(1)
		}
		var result arranger.RuntimeStartResponse
		e = proc.Call(ctx, plugin.Method(arranger.RuntimeContract, arranger.OperationStart), arranger.RuntimeStartRequest{}, &result)
		if e != nil {
			_ = proc.Close()
			fmt.Fprintln(os.Stderr, "verified runtime activation failed")
			os.Exit(1)
		}
		_ = json.NewEncoder(os.Stdout).Encode(result)
		<-ctx.Done()
		_ = proc.Close()
		return
	}
	s := &service{root: root, stopped: make(chan struct{})}
	defer s.close()
	if len(os.Args) == 2 && os.Args[1] == "--probe" {
		result, e := s.Start(ctx, arranger.RuntimeStartRequest{}, fixtureSecrets{})
		if e != nil {
			fmt.Fprintln(os.Stderr, e)
			os.Exit(1)
		}
		_ = json.NewEncoder(os.Stdout).Encode(result)
		select {
		case <-ctx.Done():
			return
		case <-s.stopped:
			fmt.Fprintln(os.Stderr, "runtime child exited")
			os.Exit(1)
		}
	}
	go func() {
		select {
		case <-s.stopped:
			s.mu.Lock()
			cooperative := s.cooperative
			s.mu.Unlock()
			if cooperative {
				return
			}
			stop()
			_ = os.Stdin.Close()
		case <-ctx.Done():
		}
	}()
	if err := arranger.ServeRuntime(ctx, s); err != nil {
		s.close()
		fmt.Fprintln(os.Stderr, "workflow runtime protocol stopped")
		os.Exit(1)
	}
}
