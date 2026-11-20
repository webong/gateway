package provision

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"

	"sort"
	"sync"
	"time"

	"github.com/webong/workload"
)

type Config struct {
	StateDirectory                 string
	SupervisorBinary               string
	LegacyDrained                  bool
	TargetID                       string
	TargetToken                    string
	CoordinationLease              time.Duration
	PollInterval                   time.Duration
	StartTimeout                   time.Duration
	StopTimeout                    time.Duration
	NodeID                         string
	DockerBinary                   string
	DockerNetwork                  string
	DockerEnvironmentFile          string
	DockerPublishHost              string
	DockerRouteHost                string
	KubernetesKubectl              string
	KubernetesNamespace            string
	KubernetesServiceAccount       string
	KubernetesEnvironmentSecret    string
	KubernetesEnvironmentConfigMap string
	KubernetesImagePullPolicy      string
	KubernetesRouteMode            string
}

// Runtime owns the shared reconciler lifecycle and exposes its dynamic route
// table to the Gateway HTTP host.
type Runtime struct {
	router      *Router
	cancel      context.CancelFunc
	done        <-chan struct{}
	release     func(context.Context) error
	logger      Logger
	stopTimeout time.Duration
	stopped     chan struct{}
	core        *workload.Reconciler
	owner       io.Closer
	stopMu      sync.Mutex
	released    bool
	once        sync.Once
}

func Start(config Config, backendURL, internalToken string, client *http.Client, logger Logger, workloads ...Provisioner) (*Runtime, error) {
	if config.TargetID != "" {
		internalToken = config.TargetToken
	}
	control, err := NewHTTPControlPlane(backendURL, internalToken, client)
	if err != nil {
		return nil, fmt.Errorf("initialize provisioning control plane: %w", err)
	}
	control.targetID = config.TargetID
	var target *DeploymentTarget
	if config.TargetID != "" {
		ctx, cancel := context.WithTimeout(context.Background(), config.StartTimeout)
		target, err = control.Target(ctx)
		if err == nil {
			err = target.Check(ctx, config, execCommandRunner{})
		}
		cancel()
		if err != nil {
			return nil, fmt.Errorf("verify deployment target: %w", err)
		}
	}
	owner, err := acquireWorkerOwnership(config)
	if err != nil {
		return nil, err
	}
	started := false
	defer func() {
		if !started {
			_ = owner.Close()
		}
	}()
	control.ownerID = owner.id
	control.legacyDrained = config.LegacyDrained
	var localDriver *workload.LocalDriver
	if target == nil || target.Driver == DriverLocal {
		if !filepath.IsAbs(config.SupervisorBinary) {
			return nil, fmt.Errorf("local execution requires an absolute supervisor binary")
		}
		localDriver, err = workload.NewLocalDriver(workload.LocalDriverConfig{
			NodeID: config.NodeID, StateDirectory: filepath.Join(owner.directory, "local"), SupervisorBinary: config.SupervisorBinary,
			StartTimeout: config.StartTimeout, StopTimeout: config.StopTimeout,
		})
		if err != nil {
			return nil, fmt.Errorf("initialize local Workload driver: %w", err)
		}
	}

	drivers := map[string]workload.Driver{}
	if target == nil || target.Driver == DriverLocal {
		drivers[DriverLocal] = localDriver
	}
	if hasDriver(workloads, DriverDocker) && (target == nil || target.Driver == DriverDocker) {
		if target != nil {
			config.DockerPublishHost = target.Connection.PublishHost
			config.DockerRouteHost = target.Connection.RouteHost
			config.DockerNetwork = target.Connection.Network
		}
		dockerDriver, driverErr := workload.NewDockerDriver(workload.DockerDriverConfig{
			TargetID:        config.TargetID,
			Context:         targetContext(target),
			Binary:          config.DockerBinary,
			NodeID:          config.NodeID,
			Network:         config.DockerNetwork,
			EnvironmentFile: config.DockerEnvironmentFile,
			PublishHost:     config.DockerPublishHost,
			RouteHost:       config.DockerRouteHost,
			StartTimeout:    config.StartTimeout,
			StopTimeout:     config.StopTimeout,
		})
		if driverErr != nil {
			return nil, fmt.Errorf("initialize Docker provisioning driver: %w", driverErr)
		}
		drivers[DriverDocker] = dockerDriver
	}
	if hasDriver(workloads, DriverKubernetes) && (target == nil || target.Driver == DriverKubernetes) {
		kubeconfig := ""
		if target != nil {
			kubeconfig = target.Connection.Kubeconfig
			config.KubernetesNamespace = target.Connection.Namespace
			config.KubernetesRouteMode = target.Connection.RouteMode
		}
		kubernetesDriver, driverErr := workload.NewKubernetesDriver(workload.KubernetesDriverConfig{
			TargetID:             config.TargetID,
			Context:              targetContext(target),
			Kubeconfig:           kubeconfig,
			KubectlBinary:        config.KubernetesKubectl,
			NodeID:               config.NodeID,
			Namespace:            config.KubernetesNamespace,
			ServiceAccount:       config.KubernetesServiceAccount,
			EnvironmentSecret:    config.KubernetesEnvironmentSecret,
			EnvironmentConfigMap: config.KubernetesEnvironmentConfigMap,
			ImagePullPolicy:      config.KubernetesImagePullPolicy,
			RouteMode:            config.KubernetesRouteMode,
			StartTimeout:         config.StartTimeout,
			StopTimeout:          config.StopTimeout,
		})
		if driverErr != nil {
			return nil, fmt.Errorf("initialize Kubernetes provisioning driver: %w", driverErr)
		}
		drivers[DriverKubernetes] = kubernetesDriver
	}
	if target != nil && !hasDriver(workloads, target.Driver) {
		return nil, fmt.Errorf("no enabled workload supports target driver %s", target.Driver)
	}

	definitions := make([]workload.Definition, 0, len(workloads))
	for _, definition := range workloads {
		definitions = append(definitions, definitionAdapter{source: definition})
	}
	driver, err := workload.NewDriverRegistry(drivers, definitions)
	if err != nil {
		return nil, fmt.Errorf("initialize Workload definitions/drivers: %w", err)
	}

	router := NewRouter()
	adapter := &workloadControl{control: control, router: router, maxLease: config.CoordinationLease}
	reconciler, err := workload.NewReconciler(workload.ReconcilerConfig{
		NodeID:            config.NodeID,
		Drivers:           supportedDrivers(drivers),
		CoordinationLease: config.CoordinationLease,
		PollInterval:      config.PollInterval,
		StopTimeout:       config.StopTimeout,
		StartTimeout:      config.StartTimeout,
	}, adapter, driver, adapter, logger)
	if err != nil {
		return nil, fmt.Errorf("initialize provisioning reconciler: %w", err)
	}

	// Claim a process-unique node session before loading or changing workloads.
	// Persistent ownership fences replacement controllers even after lease expiry.
	if err := claimWorkerSession(config, control, supportedDrivers(drivers)); err != nil {
		return nil, fmt.Errorf("claim provisioning worker session: %w", err)
	}

	reconcileContext, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		if err := reconciler.Run(reconcileContext); err != nil {
			logger.Error("Workload reconciliation stopped with unconfirmed cleanup: %v", err)
		}
	}()

	started = true
	return &Runtime{
		router: router, cancel: cancel, done: done, core: reconciler, owner: owner,
		release:     func(ctx context.Context) error { return control.ReleaseNode(ctx, config.NodeID) },
		logger:      logger,
		stopTimeout: config.StopTimeout, stopped: make(chan struct{}),
	}, nil
}

func claimWorkerSession(config Config, control *HTTPControlPlane, drivers []string) error {
	deadline := config.CoordinationLease + 2*time.Second
	if deadline < config.StopTimeout {
		deadline = config.StopTimeout
	}
	ctx, cancel := context.WithTimeout(context.Background(), deadline)
	defer cancel()
	for {
		_, _, err := control.executionAssignments(ctx, config.NodeID, drivers, config.CoordinationLease)
		if err == nil || !errors.Is(err, ErrNodeSessionActive) {
			return err
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("node is still owned by another worker after %s: %w", deadline, err)
		case <-time.After(500 * time.Millisecond):
		}
	}
}

func supportedDrivers[T any](drivers map[string]T) []string {
	keys := make([]string, 0, len(drivers))
	for key := range drivers {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	return keys
}

func hasDriver(workloads []Provisioner, driver string) bool {
	for _, workload := range workloads {
		if workload != nil && workload.Supports(driver) {
			return true
		}
	}

	return false
}

func (r *Runtime) ServeIfMatched(writer http.ResponseWriter, request *http.Request) bool {
	return r != nil && r.router.ServeIfMatched(writer, request)
}

// Done closes after the reconciler has stopped managing workloads. The node
// session may still be held until StopContext completes its release.
func (r *Runtime) Done() <-chan struct{} {
	if r == nil {
		return nil
	}
	return r.done
}

// StopContext cancels reconciliation and waits only for the caller's deadline.
// If it times out, the worker session remains fenced until reconciliation
// really exits and cleanup is confirmed. Lease expiry never proves termination.
func (r *Runtime) StopContext(ctx context.Context) error {
	if r == nil {
		return nil
	}
	r.cancel()
	select {
	case <-r.done:
	case <-ctx.Done():
		return ctx.Err()
	}
	r.stopMu.Lock()
	defer r.stopMu.Unlock()
	if r.released {
		return nil
	}
	if r.core != nil {
		if err := r.core.Shutdown(ctx); err != nil {
			return err
		}
	}
	if r.release != nil {
		if err := r.release(ctx); err != nil {
			return err
		}
	}
	if r.owner != nil {
		if err := r.owner.Close(); err != nil {
			return err
		}
	}
	r.released = true
	r.once.Do(func() { close(r.stopped) })
	return nil

}

// Stop retains the standalone lifecycle API with a bounded wait.
func (r *Runtime) Stop() {
	if r == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), r.stopTimeout)
	defer cancel()
	_ = r.StopContext(ctx)
}
