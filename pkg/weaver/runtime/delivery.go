package runtime

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"regexp"
	"time"

	plugin "github.com/webong/ext/pkg/plugin-go/contract"
	arranger "github.com/webong/gateway/arranger/integration"
)

const workAdapter = "weaver.work"

var opaqueID = regexp.MustCompile(`^[a-f0-9]{32}$`)

type workEvent struct {
	ID      string `json:"id"`
	Type    string `json:"type"`
	Payload struct {
		ClaimID string `json:"claim_id"`
	} `json:"payload"`
	Token string `json:"token"`
}

func reverse(ctx context.Context, caller plugin.ReverseCaller, operation string, input, output any) error {
	raw, err := json.Marshal(input)
	if err != nil {
		return err
	}
	raw, err = caller.Call(ctx, arranger.DeliveryContract, operation, raw)
	if err != nil {
		return err
	}
	return plugin.Decode(raw, output)
}
func durableReady(ctx context.Context, caller plugin.ReverseCaller) error {
	var result arranger.DurableResponse
	if err := reverse(ctx, caller, arranger.OperationDurableIdempotent, struct{}{}, &result); err != nil {
		return err
	}
	if !result.Enabled {
		return errors.New("durable reserved-receipt capability unavailable")
	}
	return nil
}
func adapterReady(ctx context.Context, caller plugin.ReverseCaller) (bool, error) {
	var result arranger.DurableResponse
	err := reverse(ctx, caller, arranger.OperationOwnAdapterReady, arranger.DeliveryAdapterSupportRequest{Adapter: workAdapter}, &result)
	return result.Enabled, err
}
func workEnqueue(event workEvent, now time.Time) (arranger.IdempotentEnqueueRequest, error) {
	if event.Type != "work.ready" || !opaqueID.MatchString(event.ID) || !opaqueID.MatchString(event.Token) || !opaqueID.MatchString(event.Payload.ClaimID) {
		return arranger.IdempotentEnqueueRequest{}, errors.New("invalid work notification")
	}
	delivery := arranger.GatewayDelivery{Protocol: arranger.ProtocolHTTP, Adapter: workAdapter, Target: workAdapter, Payload: []byte(event.Payload.ClaimID), Attributes: map[string]string{"event_id": event.ID}}
	raw, _ := json.Marshal(delivery)
	id := sha256.Sum256([]byte("webong/gateway-weaver\x00" + event.ID))
	fingerprint := sha256.Sum256(raw)
	return arranger.IdempotentEnqueueRequest{Delivery: delivery, ID: hex.EncodeToString(id[:16]), Fingerprint: hex.EncodeToString(fingerprint[:]), ExpiresAt: now.UTC().Add(24 * time.Hour)}, nil
}
func enqueueWork(ctx context.Context, caller plugin.ReverseCaller, event workEvent) error {
	request, err := workEnqueue(event, time.Now())
	if err != nil {
		return err
	}
	var result arranger.IdempotentEnqueueResponse
	if err = reverse(ctx, caller, arranger.OperationEnqueueGatewayIdempotent, request, &result); err != nil {
		return err
	}
	if result.Conflict {
		return arranger.ErrIdempotencyConflict
	}
	return nil
}

func (s *Service) control(ctx context.Context, method string, params, output any) error {
	s.mu.Lock()
	backend, token := s.backend, s.controlToken
	s.mu.Unlock()
	raw, err := json.Marshal(struct {
		Version string `json:"jsonrpc"`
		ID      int    `json:"id"`
		Method  string `json:"method"`
		Params  any    `json:"params"`
	}{"2.0", 1, method, params})
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, "POST", "http://"+backend+"/internal", bytes.NewReader(raw))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+token)
	timeout := 5 * time.Second
	if method == "work.handle" {
		timeout = 25 * time.Second
	}
	response, err := (&http.Client{Timeout: timeout}).Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return errors.New("private work control unavailable")
	}
	var envelope struct {
		Result json.RawMessage `json:"result"`
		Error  json.RawMessage `json:"error"`
	}
	if err = json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&envelope); err != nil {
		return err
	}
	if len(envelope.Error) > 0 {
		return errors.New("private work control rejected request")
	}
	if output != nil {
		return plugin.Decode(envelope.Result, output)
	}
	return nil
}

func (s *Service) publishLoop(caller plugin.ReverseCaller) {
	mounted := false
	until := time.Now().Add(10 * time.Second)
	for {
		select {
		case <-s.halt:
			return
		default:
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		err := durableReady(ctx, caller)
		ready := false
		if err == nil {
			ready, err = adapterReady(ctx, caller)
		}
		cancel()
		if err != nil || (!ready && (mounted || time.Now().After(until))) {
			s.close()
			return
		}
		if ready {
			if !mounted {
				s.mu.Lock()
				if s.closed {
					s.mu.Unlock()
					return
				}
				err = os.WriteFile(s.contextFile+".armed", []byte("ready"), 0600)
				s.mu.Unlock()
				if err != nil {
					s.close()
					return
				}
				mounted = true
			}
			ctx, cancel = context.WithTimeout(context.Background(), 5*time.Second)
			var batch struct {
				Events []workEvent `json:"events"`
			}
			err = s.control(ctx, "work.reserve", struct{}{}, &batch)
			cancel()
			// Transient control errors retain the outbox lease for recovery.
			// Child failure and capability loss independently stop admission.
			if err != nil {
				select {
				case <-s.halt:
					return
				case <-time.After(time.Second):
					continue
				}
			}
			for _, event := range batch.Events {
				ctx, cancel = context.WithTimeout(context.Background(), 3*time.Second)
				err = enqueueWork(ctx, caller, event)
				cancel()
				params := map[string]string{"id": event.ID, "token": event.Token}
				ctx, cancel = context.WithTimeout(context.Background(), 3*time.Second)
				if err == nil {
					err = s.control(ctx, "work.ack", params, nil)
				} else {
					_ = s.control(ctx, "work.retry", params, nil)
				}
				cancel()
				// Lost acknowledgements are recovered by reservation expiry;
				// failed enqueue attempts keep the same receipt on retry.
				if errors.Is(err, arranger.ErrIdempotencyConflict) {
					s.close()
					return
				}
			}
		}
		select {
		case <-s.halt:
			return
		case <-time.After(250 * time.Millisecond):
		}
	}
}

func (s *Service) HandleRuntimeOperation(ctx context.Context, request plugin.Request) (json.RawMessage, error) {
	if request.Contract != arranger.DeliveryAdapterContract || request.Operation != arranger.OperationDeliverAdapter {
		return nil, plugin.ErrUnsupported
	}
	var input arranger.DeliveryAdapterRequest
	if plugin.Decode(request.Payload, &input) != nil {
		return nil, plugin.ErrInvalid
	}
	response := arranger.DeliveryAdapterResponse{}
	if input.Adapter != workAdapter || input.Delivery.Adapter != workAdapter || input.Delivery.Target != workAdapter || input.Delivery.Protocol != arranger.ProtocolHTTP || !opaqueID.Match(input.Delivery.Payload) || len(input.Delivery.Batch) > 0 {
		response.Failure = arranger.AdapterFailurePermanent
		return json.Marshal(response)
	}
	s.mu.Lock()
	closed, broker, backend := s.closed, s.broker, s.backend
	s.mu.Unlock()
	if closed || !broker || backend == "" {
		return nil, plugin.ErrClosed
	}
	var result struct {
		Handled bool `json:"handled"`
	}
	if err := s.control(ctx, "work.handle", map[string]string{"claim_id": string(input.Delivery.Payload)}, &result); err != nil {
		response.Failure = arranger.AdapterFailureTransient
	} else {
		// A stale/duplicate/expired claim is consumed; queue recovery owns its next attempt.
		response.Result.ResponseStatus = http.StatusNoContent
	}
	return json.Marshal(response)
}
