package provision

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/webong/gateway/driver/integration"
)

const integrationCallTimeout = 15 * time.Second

// IntegrationPaths is an explicit installation list. No directory is scanned or
// executable selected by a server record received from Planner.
func IntegrationPaths() ([]string, error) {
	raw := strings.TrimSpace(os.Getenv("GATEWAY_INTEGRATION_PATHS"))
	if raw == "" {
		return nil, nil
	}
	paths := filepath.SplitList(raw)
	for _, path := range paths {
		if !filepath.IsAbs(path) {
			return nil, fmt.Errorf("GATEWAY_INTEGRATION_PATHS requires absolute executable paths")
		}
	}
	return paths, nil
}

// IntegrationSet owns the long-lived integration processes. Close it after the
// reconciler has stopped so no workload call uses a closed process.
type IntegrationSet struct{ integrations []*IntegrationWorkload }

func LoadIntegrations(paths []string) (*IntegrationSet, []Workload, error) {
	set := &IntegrationSet{}
	workloads := make([]Workload, 0, len(paths))
	types := make(map[string]bool)
	for _, path := range paths {
		candidate, err := OpenIntegration(path)
		if err == nil && types[candidate.Type()] {
			candidate.Close()
			err = fmt.Errorf("integration %q is configured twice", candidate.Type())
		}
		if err != nil {
			set.Close()
			return nil, nil, err
		}
		types[candidate.Type()] = true
		set.integrations = append(set.integrations, candidate)
		workloads = append(workloads, candidate)
	}
	return set, workloads, nil
}

func (s *IntegrationSet) Close() {
	if s == nil {
		return
	}
	for _, integration := range s.integrations {
		integration.Close()
	}
}

type IntegrationWorkload struct {
	mu       sync.Mutex
	cmd      *exec.Cmd
	stdin    io.WriteCloser
	scanner  *bufio.Scanner
	done     chan struct{}
	waitErr  error
	typeName string
	drivers  map[string]bool
	nextID   uint64
}

func OpenIntegration(path string) (*IntegrationWorkload, error) {
	if !filepath.IsAbs(path) {
		return nil, fmt.Errorf("integration path must be absolute: %q", path)
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("stat integration: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 {
		return nil, fmt.Errorf("integration %q must be an executable file", path)
	}
	cmd := exec.Command(path)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, err
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		return nil, fmt.Errorf("start integration %q: %w", path, err)
	}
	loaded := &IntegrationWorkload{cmd: cmd, stdin: stdin, scanner: bufio.NewScanner(stdout), done: make(chan struct{}), drivers: make(map[string]bool)}
	loaded.scanner.Buffer(make([]byte, 64*1024), integration.MaxMessageBytes)
	go func() { loaded.waitErr = cmd.Wait(); close(loaded.done) }()
	ctx, cancel := context.WithTimeout(context.Background(), integrationCallTimeout)
	defer cancel()
	response, err := loaded.call(ctx, integration.Request{Method: "describe"})
	if err != nil {
		loaded.Close()
		return nil, fmt.Errorf("describe integration %q: %w", path, err)
	}
	if strings.TrimSpace(response.Type) == "" || len(response.Drivers) == 0 {
		loaded.Close()
		return nil, fmt.Errorf("integration %q did not advertise a type and driver", path)
	}
	for _, name := range response.Drivers {
		if name != DriverLocal && name != DriverDocker && name != DriverKubernetes {
			loaded.Close()
			return nil, fmt.Errorf("integration %q advertised unsupported driver %q", path, name)
		}
		loaded.drivers[name] = true
	}
	loaded.typeName = response.Type
	return loaded, nil
}

func (p *IntegrationWorkload) Type() string                { return p.typeName }
func (p *IntegrationWorkload) Supports(driver string) bool { return p.drivers[driver] }

func (p *IntegrationWorkload) Validate(spec ServerSpec) error {
	ctx, cancel := context.WithTimeout(context.Background(), integrationCallTimeout)
	defer cancel()
	_, err := p.call(ctx, integration.Request{Method: "validate", Spec: &spec})
	return err
}

func (p *IntegrationWorkload) Launch(spec ServerSpec, runtime, host string, port int) (LaunchSpec, error) {
	ctx, cancel := context.WithTimeout(context.Background(), integrationCallTimeout)
	defer cancel()
	response, err := p.call(ctx, integration.Request{Method: "launch", Spec: &spec, Runtime: runtime, Host: host, Port: port})
	if err != nil {
		return LaunchSpec{}, err
	}
	if response.Launch == nil {
		return LaunchSpec{}, fmt.Errorf("integration returned no launch specification")
	}
	return *response.Launch, nil
}

func (p *IntegrationWorkload) call(ctx context.Context, request integration.Request) (integration.Response, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	select {
	case <-p.done:
		return integration.Response{}, fmt.Errorf("integration exited: %v", p.waitErr)
	default:
	}
	p.nextID++
	request.ID = p.nextID
	request.Version = integration.Version
	encoded, err := integration.Encode(request)
	if err != nil {
		return integration.Response{}, err
	}
	type result struct {
		response integration.Response
		err      error
	}
	received := make(chan result, 1)
	go func() {
		if _, err := p.stdin.Write(encoded); err != nil {
			received <- result{err: fmt.Errorf("write to integration: %w", err)}
			return
		}
		if !p.scanner.Scan() {
			err := p.scanner.Err()
			if err == nil {
				err = io.EOF
			}
			received <- result{err: err}
			return
		}
		var response integration.Response
		err := json.Unmarshal(p.scanner.Bytes(), &response)
		received <- result{response: response, err: err}
	}()
	select {
	case <-ctx.Done():
		_ = p.cmd.Process.Kill()
		return integration.Response{}, ctx.Err()
	case result := <-received:
		if result.err != nil {
			_ = p.cmd.Process.Kill()
			return integration.Response{}, fmt.Errorf("read integration response: %w", result.err)
		}
		if result.response.Version != integration.Version || result.response.ID != request.ID {
			_ = p.cmd.Process.Kill()
			return integration.Response{}, fmt.Errorf("integration returned incompatible response")
		}
		if result.response.Error != "" {
			return integration.Response{}, fmt.Errorf("integration: %s", result.response.Error)
		}
		return result.response, nil
	}
}

func (p *IntegrationWorkload) Close() {
	if p == nil {
		return
	}
	_ = p.stdin.Close()
	_ = p.cmd.Process.Kill()
	<-p.done
}

var _ Workload = (*IntegrationWorkload)(nil)
