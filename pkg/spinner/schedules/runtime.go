package schedules

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/webong/gateway/arranger/automation"
)

// Run describes one claimed Planner schedule execution.
type Run struct {
	ID           string              `json:"id"`
	ScheduleID   string              `json:"schedule_id"`
	ScheduledFor string              `json:"scheduled_for"`
	ClaimToken   string              `json:"claim_token"`
	Language     automation.Language `json:"language"`
	Source       string              `json:"source"`
	Payload      map[string]any      `json:"payload"`
}

type ControlPlane interface {
	Claim(context.Context) ([]Run, error)
	Complete(context.Context, Run, string, int, string) error
}

type NotificationControlPlane interface {
	DispatchNotifications(context.Context) error
}

type EnqueueFunc func(string, automation.Delivery) bool

type Runtime struct {
	Control    ControlPlane
	Executor   automation.Executor
	NativeIMAP func(context.Context, string) error
	Registrar  automation.ScheduleRegistrar
	Enqueue    EnqueueFunc
	LogError   func(string, ...any)
	Interval   time.Duration
}

func (r Runtime) Start(ctx context.Context) error {
	pool, err := newActorPool(r)
	if err != nil {
		return fmt.Errorf("start schedule actor pool: %w", err)
	}
	interval := r.Interval
	if interval <= 0 {
		interval = 5 * time.Second
	}
	go func() {
		defer pool.Stop()
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			r.poll(ctx, pool.Dispatch)
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	if notifier, ok := r.Control.(NotificationControlPlane); ok {
		go func() {
			cadence := interval
			if cadence < 5*time.Second {
				cadence = 5 * time.Second
			}
			ticker := time.NewTicker(cadence)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					requestCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
					if err := notifier.DispatchNotifications(requestCtx); err != nil {
						r.log("dispatch schedule notifications: %v", err)
					}
					cancel()
				}
			}
		}()
	}
	return nil
}

func (r Runtime) Poll(ctx context.Context) {
	r.poll(ctx, nil)
}

func (r Runtime) poll(ctx context.Context, dispatch func(context.Context, Run)) {
	if r.Control == nil || r.Enqueue == nil {
		return
	}
	requestCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	runs, err := r.Control.Claim(requestCtx)
	cancel()
	if err != nil {
		r.log("claim schedules: %v", err)
		return
	}
	var work sync.WaitGroup
	var slots chan struct{}
	if dispatch != nil {
		slots = make(chan struct{}, scheduleActorCount)
	}
	for _, run := range runs {
		if ctx.Err() != nil {
			break
		}
		if dispatch != nil || run.Language == "native.imap" {
			if slots != nil {
				select {
				case slots <- struct{}{}:
				case <-ctx.Done():
					work.Wait()
					return
				}
			}
			work.Add(1)
			go func(run Run) {
				defer work.Done()
				if slots != nil {
					defer func() { <-slots }()
				}
				if dispatch != nil {
					dispatch(ctx, run)
				} else {
					r.execute(ctx, run)
				}
			}(run)
			continue
		}
		r.execute(ctx, run)
	}
	work.Wait()
}

func (r Runtime) execute(ctx context.Context, run Run) {
	var result automation.Outcome
	var err error
	if run.Language == "native.imap" {
		if r.NativeIMAP == nil {
			err = fmt.Errorf("native IMAP poller is unavailable")
		} else {
			err = r.NativeIMAP(ctx, run.Source)
		}
	} else if r.Executor == nil {
		err = fmt.Errorf("automation language %q is not installed", run.Language)
	} else {
		result, err = r.Executor.Execute(ctx, automation.Script{
			Language: run.Language,
			Source:   run.Source,
		}, automation.Input{
			Event: map[string]any{
				"type": "schedule", "run_id": run.ID,
				"schedule_id": run.ScheduleID, "scheduled_for": run.ScheduledFor,
				"payload": run.Payload,
			},
			Variables: map[string]any{"payload": run.Payload},
		})
	}
	queued := 0
	if err == nil && len(result.Schedules) > 0 {
		if r.Registrar == nil {
			err = fmt.Errorf("schedule registration is not configured")
		} else {
			err = r.Registrar.RegisterSchedules(ctx, "schedule:"+run.ScheduleID, result.Schedules)
		}
	}
	if err == nil {
		if len(result.Deliveries) > 32 {
			err = fmt.Errorf("scheduled automation exceeded the delivery limit")
		}
	}
	if err == nil {
		for _, delivery := range result.Deliveries {
			if delivery.Method == "" {
				delivery.Method = http.MethodPost
			}
			if !r.Enqueue(run.ID, delivery) {
				err = fmt.Errorf("HTTP delivery queue unavailable after %d deliveries", queued)
				break
			}
			queued++
		}
	}
	status := "succeeded"
	message := ""
	if err != nil {
		status = "failed"
		message = err.Error()
		if len(message) > 2000 {
			message = message[:2000]
		}
	}
	reportCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if reportErr := r.Control.Complete(reportCtx, run, status, queued, message); reportErr != nil {
		r.log("complete schedule run %s: %v", run.ID, reportErr)
	}
}

func (r Runtime) log(format string, args ...any) {
	if r.LogError != nil {
		r.LogError(format, args...)
	}
}

type HTTPControlPlane struct {
	client     *http.Client
	base       *url.URL
	token      string
	languages  []automation.Language
	nativeIMAP bool
}

// EnableNativeIMAP advertises the built-in executor to Planner's scheduler.
func (c *HTTPControlPlane) EnableNativeIMAP() { c.nativeIMAP = true }

func NewHTTPControlPlane(baseURL, token string, client *http.Client, languages ...automation.Language) (*HTTPControlPlane, error) {
	base, err := url.Parse(baseURL)
	if err != nil || base.Host == "" || (base.Scheme != "http" && base.Scheme != "https") {
		return nil, fmt.Errorf("invalid schedule control-plane URL")
	}
	if strings.TrimSpace(token) == "" {
		return nil, fmt.Errorf("GATEWAY_INTERNAL_TOKEN is required for schedules")
	}
	if client == nil {
		client = http.DefaultClient
	}
	return &HTTPControlPlane{
		client:    client,
		base:      base,
		token:     token,
		languages: append([]automation.Language{}, languages...),
	}, nil
}

// NewHandlerControlPlane uses the same private schedule endpoints through an
// embedded Planner handler. No network listener or public route is involved.
func NewHandlerControlPlane(handler http.Handler, token string, languages ...automation.Language) (*HTTPControlPlane, error) {
	if handler == nil {
		return nil, fmt.Errorf("embedded schedule Planner handler is required")
	}
	client := &http.Client{
		Transport:     handlerTransport{handler: handler},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	return NewHTTPControlPlane("http://gateway.internal", token, client, languages...)
}

type handlerTransport struct{ handler http.Handler }

func (transport handlerTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	response := httptest.NewRecorder()
	transport.handler.ServeHTTP(response, request)
	return response.Result(), nil
}

func (c *HTTPControlPlane) Claim(ctx context.Context) ([]Run, error) {
	request := map[string]any{"automation_languages": c.languages}
	if c.nativeIMAP {
		request["native_jobs"] = []string{"imap"}
	}
	payload, err := json.Marshal(request)
	if err != nil {
		return nil, err
	}
	response, err := c.call(ctx, "/_internal/gateway/schedules/claim", payload)
	if err != nil {
		return nil, err
	}
	var result struct {
		Data []Run `json:"data"`
	}
	if err := json.Unmarshal(response, &result); err != nil {
		return nil, fmt.Errorf("decode schedule claims: %w", err)
	}
	return result.Data, nil
}

func (c *HTTPControlPlane) Complete(ctx context.Context, run Run, status string, queued int, message string) error {
	payload, err := json.Marshal(map[string]any{
		"claim_token": run.ClaimToken, "status": status,
		"deliveries_queued": queued, "error": message,
	})
	if err != nil {
		return err
	}
	_, err = c.call(ctx, "/_internal/gateway/schedule-runs/"+url.PathEscape(run.ID)+"/complete", payload)
	return err
}

func (c *HTTPControlPlane) RegisterSchedules(ctx context.Context, origin string, schedules []automation.Schedule) error {
	payload, err := json.Marshal(map[string]any{
		"origin": origin, "schedules": schedules,
	})
	if err != nil {
		return err
	}
	_, err = c.call(ctx, "/_internal/gateway/schedules/register", payload)
	return err
}

func (c *HTTPControlPlane) DispatchNotifications(ctx context.Context) error {
	_, err := c.call(ctx, "/_internal/gateway/schedules/notify", []byte(`{}`))
	return err
}

func (c *HTTPControlPlane) call(ctx context.Context, endpoint string, payload []byte) ([]byte, error) {
	target := *c.base
	target.Path = strings.TrimRight(c.base.Path, "/") + endpoint
	target.RawQuery = ""
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, target.String(), bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	request.Header.Set("X-Gateway-Internal", c.token)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	response, err := c.client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("schedule control plane returned HTTP %d", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, 4*1024*1024+1))
	if err != nil || len(body) > 4*1024*1024 {
		return nil, fmt.Errorf("schedule control-plane response too large or unreadable")
	}
	return body, nil
}

var _ automation.ScheduleRegistrar = (*HTTPControlPlane)(nil)
