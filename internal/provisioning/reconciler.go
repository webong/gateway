package provisioning

import (
	"context"
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
	NodeID            string
	Drivers           []string
	CoordinationLease time.Duration
	PollInterval      time.Duration
	StopTimeout       time.Duration
}

type managedProcess struct {
	instanceID string
	spec       ServerSpec
	process    Process
}

type Reconciler struct {
	config         ReconcilerConfig
	control        ControlPlane
	driver         Starter
	router         *Router
	logger         Logger
	processes      map[string]*managedProcess
	failures       map[string]string
	lastAssignment time.Time
}

func NewReconciler(config ReconcilerConfig, control ControlPlane, driver Starter, router *Router, logger Logger) (*Reconciler, error) {
	if config.NodeID == "" {
		return nil, fmt.Errorf("provisioning reconciler node ID is required")
	}
	if config.PollInterval <= 0 || config.StopTimeout <= 0 || config.CoordinationLease <= 0 {
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
	specifications, leaderID, err := r.control.Assignments(ctx, r.config.NodeID, r.config.Drivers)
	if err != nil {
		r.logger.Error("Failed to load provisioned assignments: %v", err)
		if !r.lastAssignment.IsZero() && time.Since(r.lastAssignment) >= r.config.CoordinationLease {
			r.logger.Warn("Provisioning assignment lease expired; stopping local workloads")
			r.shutdown()
		}
		return
	}
	r.lastAssignment = time.Now()
	if leaderID == r.config.NodeID {
		r.logger.Debug("Gateway node %s is the active provisioning placement leader", r.config.NodeID)
	}

	specs := make(map[string]ServerSpec, len(specifications))
	for _, spec := range specifications {
		specs[spec.AssignmentID] = spec
	}
	for assignmentID := range r.failures {
		if _, exists := specs[assignmentID]; !exists {
			delete(r.failures, assignmentID)
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

		desired, exists := specs[managed.instanceID]
		if !exists || desired.Revision != managed.spec.Revision {
			r.stop(managed)
			delete(r.processes, id)
		}
	}

	for _, spec := range specifications {
		if _, exists := r.processes[spec.AssignmentID]; exists {
			continue
		}
		if _, startErr := r.start(ctx, spec); startErr != nil {
			r.logger.Error("Failed to start provisioned server %s: %v", spec.ID, startErr)
		}
	}

	for _, managed := range r.processes {
		r.report(ctx, managed, "running", nil)
	}
}

func (r *Reconciler) start(ctx context.Context, spec ServerSpec) (*managedProcess, error) {
	instanceID := spec.AssignmentID
	process, err := r.driver.Start(ctx, spec, instanceID)
	if err != nil {
		r.failures[instanceID] = instanceID
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
	delete(r.failures, instanceID)
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
