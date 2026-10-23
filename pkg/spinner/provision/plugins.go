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

	"github.com/webong/gateway/driver/workloadplugin"
)

const pluginCallTimeout = 15 * time.Second

// PluginPaths is an explicit installation list. No directory is scanned or
// executable selected by a server record received from Planner.
func PluginPaths() ([]string, error) {
	raw := strings.TrimSpace(os.Getenv("GATEWAY_EXTENSION_PATHS"))
	if raw == "" {
		return nil, nil
	}
	paths := filepath.SplitList(raw)
	for _, path := range paths {
		if !filepath.IsAbs(path) {
			return nil, fmt.Errorf("GATEWAY_EXTENSION_PATHS requires absolute executable paths")
		}
	}
	return paths, nil
}

// PluginSet owns the long-lived extension processes. Close it after the
// reconciler has stopped so no workload call uses a closed process.
type PluginSet struct{ plugins []*PluginWorkload }

func LoadPlugins(paths []string) (*PluginSet, []Workload, error) {
	set := &PluginSet{}
	workloads := make([]Workload, 0, len(paths))
	types := make(map[string]bool)
	for _, path := range paths {
		plugin, err := OpenPlugin(path)
		if err == nil && types[plugin.Type()] {
			plugin.Close()
			err = fmt.Errorf("workload extension %q is configured twice", plugin.Type())
		}
		if err != nil {
			set.Close()
			return nil, nil, err
		}
		types[plugin.Type()] = true
		set.plugins = append(set.plugins, plugin)
		workloads = append(workloads, plugin)
	}
	return set, workloads, nil
}

func (s *PluginSet) Close() {
	if s == nil {
		return
	}
	for _, plugin := range s.plugins {
		plugin.Close()
	}
}

type PluginWorkload struct {
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

func OpenPlugin(path string) (*PluginWorkload, error) {
	if !filepath.IsAbs(path) {
		return nil, fmt.Errorf("extension path must be absolute: %q", path)
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("stat workload extension: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 {
		return nil, fmt.Errorf("workload extension %q must be an executable file", path)
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
		return nil, fmt.Errorf("start workload extension %q: %w", path, err)
	}
	plugin := &PluginWorkload{cmd: cmd, stdin: stdin, scanner: bufio.NewScanner(stdout), done: make(chan struct{}), drivers: make(map[string]bool)}
	plugin.scanner.Buffer(make([]byte, 64*1024), workloadplugin.MaxMessageBytes)
	go func() { plugin.waitErr = cmd.Wait(); close(plugin.done) }()
	ctx, cancel := context.WithTimeout(context.Background(), pluginCallTimeout)
	defer cancel()
	response, err := plugin.call(ctx, workloadplugin.Request{Method: "describe"})
	if err != nil {
		plugin.Close()
		return nil, fmt.Errorf("describe workload extension %q: %w", path, err)
	}
	if strings.TrimSpace(response.Type) == "" || len(response.Drivers) == 0 {
		plugin.Close()
		return nil, fmt.Errorf("workload extension %q did not advertise a type and driver", path)
	}
	for _, name := range response.Drivers {
		if name != DriverLocal && name != DriverDocker && name != DriverKubernetes {
			plugin.Close()
			return nil, fmt.Errorf("workload extension %q advertised unsupported driver %q", path, name)
		}
		plugin.drivers[name] = true
	}
	plugin.typeName = response.Type
	return plugin, nil
}

func (p *PluginWorkload) Type() string                { return p.typeName }
func (p *PluginWorkload) Supports(driver string) bool { return p.drivers[driver] }

func (p *PluginWorkload) Validate(spec ServerSpec) error {
	ctx, cancel := context.WithTimeout(context.Background(), pluginCallTimeout)
	defer cancel()
	_, err := p.call(ctx, workloadplugin.Request{Method: "validate", Spec: &spec})
	return err
}

func (p *PluginWorkload) Launch(spec ServerSpec, runtime, host string, port int) (LaunchSpec, error) {
	ctx, cancel := context.WithTimeout(context.Background(), pluginCallTimeout)
	defer cancel()
	response, err := p.call(ctx, workloadplugin.Request{Method: "launch", Spec: &spec, Runtime: runtime, Host: host, Port: port})
	if err != nil {
		return LaunchSpec{}, err
	}
	if response.Launch == nil {
		return LaunchSpec{}, fmt.Errorf("workload extension returned no launch specification")
	}
	return *response.Launch, nil
}

func (p *PluginWorkload) call(ctx context.Context, request workloadplugin.Request) (workloadplugin.Response, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	select {
	case <-p.done:
		return workloadplugin.Response{}, fmt.Errorf("workload extension exited: %v", p.waitErr)
	default:
	}
	p.nextID++
	request.ID = p.nextID
	request.Version = workloadplugin.Version
	encoded, err := workloadplugin.Encode(request)
	if err != nil {
		return workloadplugin.Response{}, err
	}
	type result struct {
		response workloadplugin.Response
		err      error
	}
	received := make(chan result, 1)
	go func() {
		if _, err := p.stdin.Write(encoded); err != nil {
			received <- result{err: fmt.Errorf("write to workload extension: %w", err)}
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
		var response workloadplugin.Response
		err := json.Unmarshal(p.scanner.Bytes(), &response)
		received <- result{response: response, err: err}
	}()
	select {
	case <-ctx.Done():
		_ = p.cmd.Process.Kill()
		return workloadplugin.Response{}, ctx.Err()
	case result := <-received:
		if result.err != nil {
			_ = p.cmd.Process.Kill()
			return workloadplugin.Response{}, fmt.Errorf("read workload extension response: %w", result.err)
		}
		if result.response.Version != workloadplugin.Version || result.response.ID != request.ID {
			_ = p.cmd.Process.Kill()
			return workloadplugin.Response{}, fmt.Errorf("workload extension returned incompatible response")
		}
		if result.response.Error != "" {
			return workloadplugin.Response{}, fmt.Errorf("workload extension: %s", result.response.Error)
		}
		return result.response, nil
	}
}

func (p *PluginWorkload) Close() {
	if p == nil {
		return
	}
	_ = p.stdin.Close()
	_ = p.cmd.Process.Kill()
	<-p.done
}

var _ Workload = (*PluginWorkload)(nil)
