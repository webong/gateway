package gateway

import "context"

type recordingExecutor struct {
	delivered []Delivery
	queued    []Delivery
	response  Response
}

func (e *recordingExecutor) Deliver(_ context.Context, delivery Delivery) (Response, error) {
	e.delivered = append(e.delivered, delivery)
	return e.response, nil
}

func (e *recordingExecutor) Enqueue(delivery Delivery) bool {
	e.queued = append(e.queued, delivery)
	return true
}
