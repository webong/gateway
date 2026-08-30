package provisioning

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sort"
	"time"
)

type Logger interface {
	Debug(string, ...interface{})
	Info(string, ...interface{})
	Warn(string, ...interface{})
	Error(string, ...interface{})
}

type ReconcilerConfig struct {
	NodeID       string
	PollInterval time.Duration
	StopTimeout  time.Duration
}

type managedProcess struct {
	instanceID string
	spec       ServerSpec
	process    Process
}

type Reconciler struct {
	config    ReconcilerConfig
	control   ControlPlane
	driver    Starter
	router    *Router
	logger    Logger
	processes map[string]*managedProcess
	failures  map[string]string
}

func NewReconciler(config ReconcilerConfig, control ControlPlane, driver Starter, router *Router, logger Logger) (*Reconciler, error) {
	if config.NodeID == "" {
		return nil, fmt.Errorf("provisioning reconciler node ID is required")
	}
	if config.PollInterval <= 0 || config.StopTimeout <= 0 {
		return nil, fmt.Errorf("provisioning reconciler intervals must be positive")
	}
	if control == nil || driver == nil || router == nil || logger == nil {
		return nil, fmt.Errorf("provisioning reconciler dependencies are required")
	}

	return &Reconciler{
		config:    config,
		control:   control,
		driver:    driver,
		router:    router,
		logger:    logger,
		processes: make(map[string]*managedProcess),
		failures:  make(map[string]string),
	}, nil
}

func (r *Reconciler) Run(ctx context.Context) {
	r.reconcile(ctx)
	ticker := time.NewTicker(r.config.PollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			r.shutdown()
			return
		case <-ticker.C:
			r.reconcile(ctx)
		}
	}
}

func (r *Reconciler) reconcile(ctx context.Context) {
	specifications, err := r.control.Servers(ctx)
	if err != nil {
		r.logger.Error("Failed to load provisioned desired state: %v", err)
		return
	}

	specs := make(map[string]ServerSpec, len(specifications))
	for _, spec := range specifications {
		specs[spec.ID] = spec
	}
	for serverID := range r.failures {
		spec, exists := specs[serverID]
		if !exists || spec.DesiredState == DesiredStopped {
			delete(r.failures, serverID)
		}
	}

	ids := make([]string, 0, len(r.processes))
	for id := range r.processes {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		managed := r.processes[id]
		select {
		case <-managed.process.Done():
			r.router.Remove(managed.spec.ID, managed.instanceID)
			r.report(context.Background(), managed, "failed", managed.process.Err())
			delete(r.processes, id)
			continue
		default:
		}

		desired, exists := specs[managed.spec.ID]
		if !exists || desired.DesiredState == DesiredStopped || desired.Revision != managed.spec.Revision {
			r.stop(managed)
			delete(r.processes, id)
		}
	}

	for _, spec := range specifications {
		if spec.DesiredState != DesiredRunning {
			continue
		}
		current := r.forServer(spec.ID)
		for len(current) > spec.Replicas {
			managed := current[len(current)-1]
			r.stop(managed)
			delete(r.processes, managed.instanceID)
			current = current[:len(current)-1]
		}
		for len(current) < spec.Replicas {
			managed, startErr := r.start(ctx, spec)
			if startErr != nil {
				r.logger.Error("Failed to start provisioned server %s: %v", spec.ID, startErr)
				break
			}
			current = append(current, managed)
		}
	}

	for _, managed := range r.processes {
		r.report(ctx, managed, "running", nil)
	}
}

func (r *Reconciler) start(ctx context.Context, spec ServerSpec) (*managedProcess, error) {
	instanceID := r.failures[spec.ID]
	if instanceID == "" {
		var err error
		instanceID, err = randomID()
		if err != nil {
			return nil, err
		}
	}
	process, err := r.driver.Start(ctx, spec, instanceID)
	if err != nil {
		r.failures[spec.ID] = instanceID
		report := InstanceReport{
			NodeID:   r.config.NodeID,
			Runtime:  spec.Driver,
			State:    "failed",
			Revision: spec.Revision,
			Error:    err.Error(),
		}
		if reportErr := r.control.Report(ctx, spec.ID, instanceID, report); reportErr != nil {
			r.logger.Warn("Failed to report provisioned instance %s: %v", instanceID, reportErr)
		}
		return nil, err
	}
	delete(r.failures, spec.ID)
	managed := &managedProcess{instanceID: instanceID, spec: spec, process: process}
	r.processes[instanceID] = managed
	r.router.Add(spec, instanceID, process.Host(), process.Port())
	r.report(ctx, managed, "running", nil)
	r.logger.Info("Started provisioned instance %s for server %s on %s:%d", instanceID, spec.ID, process.Host(), process.Port())

	return managed, nil
}

func (r *Reconciler) stop(managed *managedProcess) {
	r.router.Remove(managed.spec.ID, managed.instanceID)
	ctx, cancel := context.WithTimeout(context.Background(), r.config.StopTimeout)
	err := managed.process.Stop(ctx)
	cancel()
	if err != nil && err != context.DeadlineExceeded {
		r.logger.Warn("Failed to stop provisioned instance %s gracefully: %v", managed.instanceID, err)
	}
	r.report(context.Background(), managed, "stopped", err)
	r.logger.Info("Stopped provisioned instance %s for server %s", managed.instanceID, managed.spec.ID)
}

func (r *Reconciler) shutdown() {
	ids := make([]string, 0, len(r.processes))
	for id := range r.processes {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		r.stop(r.processes[id])
		delete(r.processes, id)
	}
}

func (r *Reconciler) forServer(serverID string) []*managedProcess {
	processes := make([]*managedProcess, 0)
	for _, managed := range r.processes {
		if managed.spec.ID == serverID {
			processes = append(processes, managed)
		}
	}
	sort.Slice(processes, func(i, j int) bool { return processes[i].instanceID < processes[j].instanceID })

	return processes
}

func (r *Reconciler) report(ctx context.Context, managed *managedProcess, state string, processErr error) {
	report := InstanceReport{
		NodeID:   r.config.NodeID,
		Runtime:  managed.spec.Driver,
		PID:      managed.process.PID(),
		Host:     managed.process.Host(),
		Port:     managed.process.Port(),
		State:    state,
		Revision: managed.spec.Revision,
	}
	if processErr != nil {
		report.Error = processErr.Error()
	}
	if err := r.control.Report(ctx, managed.spec.ID, managed.instanceID, report); err != nil {
		r.logger.Warn("Failed to report provisioned instance %s: %v", managed.instanceID, err)
	}
}

func randomID() (string, error) {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("generate provisioned instance ID: %w", err)
	}
	bytes[6] = (bytes[6] & 0x0f) | 0x40
	bytes[8] = (bytes[8] & 0x3f) | 0x80
	hexValue := hex.EncodeToString(bytes)

	return hexValue[0:8] + "-" + hexValue[8:12] + "-" + hexValue[12:16] + "-" + hexValue[16:20] + "-" + hexValue[20:32], nil
}
