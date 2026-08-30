package provisioning

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

type LocalDriverConfig struct {
	StartTimeout time.Duration
	StopTimeout  time.Duration
}

type LocalDriver struct {
	config LocalDriverConfig
}

func NewLocalDriver(config LocalDriverConfig) (*LocalDriver, error) {
	if config.StartTimeout <= 0 || config.StopTimeout <= 0 {
		return nil, fmt.Errorf("provisioning process timeouts must be positive")
	}

	return &LocalDriver{config: config}, nil
}

func (d *LocalDriver) Start(ctx context.Context, spec ServerSpec, _ string, workload Workload) (Process, error) {
	if err := spec.Validate(); err != nil {
		return nil, err
	}
	if spec.Driver != DriverLocal {
		return nil, fmt.Errorf("local provisioning driver cannot start %q runtime", spec.Driver)
	}

	host := "127.0.0.1"
	port, err := availablePort(host)
	if err != nil {
		return nil, fmt.Errorf("allocate provisioned listener: %w", err)
	}
	launch, err := workload.Launch(spec, DriverLocal, host, port)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(launch.Executable) == "" || strings.TrimSpace(launch.WorkingDirectory) == "" {
		return nil, fmt.Errorf("local workload executable and working directory are required")
	}

	command := exec.Command(launch.Executable, launch.Arguments...)
	command.Dir = launch.WorkingDirectory
	command.Env = append(os.Environ(), launch.Environment...)
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	if err := command.Start(); err != nil {
		return nil, fmt.Errorf("start provisioned process: %w", err)
	}

	process := &localProcess{
		command: command,
		host:    host,
		port:    port,
		done:    make(chan struct{}),
	}
	go process.wait()

	startupContext, cancel := context.WithTimeout(ctx, d.config.StartTimeout)
	defer cancel()
	if err := waitForListener(startupContext, process); err != nil {
		stopContext, stopCancel := context.WithTimeout(context.Background(), d.config.StopTimeout)
		_ = process.Stop(stopContext)
		stopCancel()
		return nil, fmt.Errorf("wait for provisioned listener: %w", err)
	}

	return process, nil
}

type localProcess struct {
	command  *exec.Cmd
	host     string
	port     int
	done     chan struct{}
	errMu    sync.RWMutex
	err      error
	stopOnce sync.Once
}

func (p *localProcess) wait() {
	err := p.command.Wait()
	p.errMu.Lock()
	p.err = err
	p.errMu.Unlock()
	close(p.done)
}

func (p *localProcess) PID() int              { return p.command.Process.Pid }
func (p *localProcess) Host() string          { return p.host }
func (p *localProcess) Port() int             { return p.port }
func (p *localProcess) Done() <-chan struct{} { return p.done }

func (p *localProcess) Err() error {
	p.errMu.RLock()
	defer p.errMu.RUnlock()
	return p.err
}

func (p *localProcess) Stop(ctx context.Context) error {
	var signalErr error
	p.stopOnce.Do(func() {
		select {
		case <-p.done:
			return
		default:
		}
		signalErr = p.command.Process.Signal(os.Interrupt)
	})
	if signalErr != nil {
		return signalErr
	}

	select {
	case <-p.done:
		return nil
	case <-ctx.Done():
		if err := p.command.Process.Kill(); err != nil {
			return fmt.Errorf("kill provisioned process after timeout: %w", err)
		}
		<-p.done
		return ctx.Err()
	}
}

func availablePort(host string) (int, error) {
	listener, err := net.Listen("tcp", net.JoinHostPort(host, "0"))
	if err != nil {
		return 0, err
	}
	defer listener.Close()

	return listener.Addr().(*net.TCPAddr).Port, nil
}

func waitForListener(ctx context.Context, process Process) error {
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	address := net.JoinHostPort(process.Host(), strconv.Itoa(process.Port()))

	for {
		connection, err := (&net.Dialer{Timeout: 100 * time.Millisecond}).DialContext(ctx, "tcp", address)
		if err == nil {
			_ = connection.Close()
			return nil
		}

		select {
		case <-process.Done():
			return fmt.Errorf("process exited before accepting connections: %w", process.Err())
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

var _ Driver = (*LocalDriver)(nil)
var _ Process = (*localProcess)(nil)
