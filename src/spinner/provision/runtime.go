package provision

import (
	"context"
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
	router *Router
	cancel context.CancelFunc
	done   <-chan struct{}
	once   sync.Once
}

func Start(config Config, backendURL, internalToken string, client *http.Client, logger Logger, workloads ...Workload) (*Runtime, error) {
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
	prepareContext, cancelPrepare := context.WithTimeout(context.Background(), config.StopTimeout)
	var prepareErr error
	if target != nil {
		// Authorize/register node ownership before resetting state or deleting workloads.
		_, _, prepareErr = control.Assignments(prepareContext, config.NodeID, supportedDrivers(drivers))
	}
	if prepareErr == nil {
		prepareErr = control.ResetNode(prepareContext, config.NodeID)
	}
	if prepareErr == nil {
		prepareErr = driver.Prepare(prepareContext)
	}
	cancelPrepare()
	if prepareErr != nil {
		return nil, fmt.Errorf("prepare provisioning runtime drivers: %w", prepareErr)
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
		return nil, fmt.Errorf("initialize provisioning reconciler: %w", err)
	}

	reconcileContext, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		reconciler.Run(reconcileContext)
	}()

	return &Runtime{router: router, cancel: cancel, done: done}, nil
}

func supportedDrivers(drivers map[string]Driver) []string {
	keys := make([]string, 0, len(drivers))
	for key := range drivers {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	return keys
}

func hasDriver(workloads []Workload, driver string) bool {
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
	})
}
