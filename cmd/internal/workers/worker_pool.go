package workers

import (
	"sync"

	"github.com/webong/gateway/cmd/internal/forwarding"
	"github.com/webong/gateway/cmd/internal/logging"
)

type WorkerPool struct {
	workers   int
	taskQueue chan forwarding.ForwardRequest
	wg        sync.WaitGroup
	forwarder *forwarding.Forwarder
	logger    *logging.Logger
}

func NewWorkerPool(workers, queueSize int, forwarder *forwarding.Forwarder, logger *logging.Logger) *WorkerPool {
	wp := &WorkerPool{
		workers:   workers,
		taskQueue: make(chan forwarding.ForwardRequest, queueSize),
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
		wp.logger.Debug("Worker %d received task for %s", id, task.TargetURL)
		wp.forwarder.Forward(task)
	}

	wp.logger.Debug("Worker %d stopped", id)
}

func (wp *WorkerPool) Submit(task forwarding.ForwardRequest) bool {
	select {
	case wp.taskQueue <- task:
		wp.logger.Debug("Task submitted for %s", task.TargetURL)
		return true
	default:
		wp.logger.Warn("Task queue full, dropping request to %s", task.TargetURL)
		return false
	}
}

func (wp *WorkerPool) Shutdown() {
	wp.logger.Info("Shutting down worker pool...")
	close(wp.taskQueue)
	wp.wg.Wait()
	wp.logger.Info("Worker pool shut down complete")
}
