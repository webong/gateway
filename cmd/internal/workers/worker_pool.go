package workers

import (
	"sync"

	"github.com/webong/gateway/cmd/internal/forwarding"
	"github.com/webong/gateway/cmd/internal/logging"
)

type WorkerPool struct {
	workers   int
	taskQueue chan Task
	wg        sync.WaitGroup
	shutdown  sync.Once
	forwarder *forwarding.Forwarder
	logger    *logging.Logger
}

// Task is one bounded unit of asynchronous transport work. Protocol adapters
// use this without teaching the worker pool about HTTP, SMTP, or storage.
type Task struct {
	Label string
	Run   func() error
}

func NewWorkerPool(workers, queueSize int, forwarder *forwarding.Forwarder, logger *logging.Logger) *WorkerPool {
	wp := &WorkerPool{
		workers:   workers,
		taskQueue: make(chan Task, queueSize),
		forwarder: forwarder,
		logger:    logger,
	}

	wp.start()
	return wp
}

func (wp *WorkerPool) start() {
	for i := 0; i < wp.workers; i++ {
		wp.wg.Add(1)
		go wp.worker(i)
	}
	wp.logger.Info("Started %d workers", wp.workers)
}

func (wp *WorkerPool) worker(id int) {
	defer wp.wg.Done()
	wp.logger.Debug("Worker %d started", id)

	for task := range wp.taskQueue {
		wp.logger.Debug("Worker %d received task for %s", id, task.Label)
		if task.Run == nil {
			wp.logger.Error("Worker %d received an invalid task for %s", id, task.Label)
			continue
		}
		if err := task.Run(); err != nil {
			wp.logger.Error("Worker %d failed task for %s: %v", id, task.Label, err)
		}
	}

	wp.logger.Debug("Worker %d stopped", id)
}

func (wp *WorkerPool) Submit(task forwarding.ForwardRequest) bool {
	return wp.SubmitTask(Task{
		Label: task.TargetURL,
		Run: func() error {
			wp.forwarder.Forward(task)
			return nil
		},
	})
}

func (wp *WorkerPool) SubmitTask(task Task) bool {
	if task.Run == nil {
		return false
	}
	if task.Label == "" {
		task.Label = "unnamed transport task"
	}
	select {
	case wp.taskQueue <- task:
		wp.logger.Debug("Task submitted for %s", task.Label)
		return true
	default:
		wp.logger.Warn("Task queue full, dropping request to %s", task.Label)
		return false
	}
}

func (wp *WorkerPool) Shutdown() {
	wp.shutdown.Do(func() {
		wp.logger.Info("Shutting down worker pool...")
		close(wp.taskQueue)
		wp.wg.Wait()
		wp.logger.Info("Worker pool shut down complete")
	})
}
