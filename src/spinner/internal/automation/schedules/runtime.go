package schedules

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/webong/gateway/src/spinner/internal/automation"
)

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

type EnqueueFunc func(automation.Delivery) bool

type Runtime struct {
	Control  ControlPlane
	Enqueue  EnqueueFunc
	LogError func(string, ...any)
	Interval time.Duration
}

func (r Runtime) Start(ctx context.Context) {
	interval := r.Interval
	if interval <= 0 {
		interval = 5 * time.Second
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			r.Poll(ctx)
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}

func (r Runtime) Poll(ctx context.Context) {
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
	for _, run := range runs {
		if ctx.Err() != nil {
			return
		}
		r.execute(ctx, run)
	}
}

func (r Runtime) execute(ctx context.Context, run Run) {
	result, err := (automation.Runtime{}).Execute(ctx, automation.Script{
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
	queued := 0
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
			if !r.Enqueue(delivery) {
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
	client *http.Client
	base   *url.URL
	token  string
}

func NewHTTPControlPlane(baseURL, token string, client *http.Client) (*HTTPControlPlane, error) {
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
	return &HTTPControlPlane{client: client, base: base, token: token}, nil
}

func (c *HTTPControlPlane) Claim(ctx context.Context) ([]Run, error) {
	response, err := c.call(ctx, "/_internal/gateway/schedules/claim", []byte("{}"))
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
