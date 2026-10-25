package provision

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"sync"
	"time"
)

type Config struct {
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
	router  *Router
	cancel  context.CancelFunc
	done    <-chan struct{}
	release func()
	once    sync.Once
}

func Start(config Config, backendURL, internalToken string, client *http.Client, logger Logger, workloads ...Extension) (*Runtime, error) {
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
	localDriver, err := NewLocalDriver(LocalDriverConfig{
		StartTimeout: config.StartTimeout,
		StopTimeout:  config.StopTimeout,
	})
	if err != nil {
		return nil, fmt.Errorf("initialize local provisioning driver: %w", err)
	}

	drivers := map[string]Driver{}
	if target == nil || target.Driver == DriverLocal {
		drivers[DriverLocal] = localDriver
	}
	if hasDriver(workloads, DriverDocker) && (target == nil || target.Driver == DriverDocker) {
		if target != nil {
			config.DockerPublishHost = target.Connection.PublishHost
			config.DockerRouteHost = target.Connection.RouteHost
			config.DockerNetwork = target.Connection.Network
		}
		dockerDriver, driverErr := NewDockerDriver(DockerDriverConfig{
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
		kubernetesDriver, driverErr := NewKubernetesDriver(KubernetesDriverConfig{
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

	driver, err := NewDriverRegistry(drivers, workloads)
	if err != nil {
		return nil, fmt.Errorf("initialize provisioning driver registry: %w", err)
	}
	// Claim a process-unique node session before loading or changing workloads.
	// A crashed predecessor keeps its claim only until its placement lease ends.
	if err := claimWorkerSession(config, control, supportedDrivers(drivers)); err != nil {
		return nil, fmt.Errorf("claim provisioning worker session: %w", err)
	}
	resetContext, cancelReset := context.WithTimeout(context.Background(), config.StopTimeout)
	resetErr := control.ResetNode(resetContext, config.NodeID)
	cancelReset()
	if resetErr != nil {
		releaseWorkerSession(config, control, logger)
		return nil, fmt.Errorf("reset stale provisioning reports: %w", resetErr)
	}
	router := NewRouter()
	reconciler, err := NewReconciler(ReconcilerConfig{
		NodeID:            config.NodeID,
		Drivers:           supportedDrivers(drivers),
		CoordinationLease: config.CoordinationLease,
		PollInterval:      config.PollInterval,
		StopTimeout:       config.StopTimeout,
	}, control, driver, router, logger)
	if err != nil {
		releaseWorkerSession(config, control, logger)
		return nil, fmt.Errorf("initialize provisioning reconciler: %w", err)
	}

	reconcileContext, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		reconciler.Run(reconcileContext)
	}()

	return &Runtime{router: router, cancel: cancel, done: done, release: func() { releaseWorkerSession(config, control, logger) }}, nil
}

func claimWorkerSession(config Config, control *HTTPControlPlane, drivers []string) error {
	deadline := config.CoordinationLease + 2*time.Second
	if deadline < config.StopTimeout {
		deadline = config.StopTimeout
	}
	ctx, cancel := context.WithTimeout(context.Background(), deadline)
	defer cancel()
	for {
		_, _, err := control.Assignments(ctx, config.NodeID, drivers)
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

func releaseWorkerSession(config Config, control *HTTPControlPlane, logger Logger) {
	ctx, cancel := context.WithTimeout(context.Background(), config.StopTimeout)
	defer cancel()
	if err := control.ReleaseNode(ctx, config.NodeID); err != nil {
		logger.Warn("Failed to release provisioning worker session: %v", err)
	}
}

func supportedDrivers(drivers map[string]Driver) []string {
	keys := make([]string, 0, len(drivers))
	for key := range drivers {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	return keys
}

func hasDriver(workloads []Extension, driver string) bool {
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

func (r *Runtime) Stop() {
	if r == nil {
		return
	}
	r.once.Do(func() {
		r.cancel()
		<-r.done
		if r.release != nil {
			r.release()
		}
	})
}
