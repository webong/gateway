// Package automation defines the shared interface between Gateway hosts and
// optional automation language extensions. It owns language dispatch and
// outcome validation without depending on Spinner or any interpreter.
package automation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

const (
	MaxDeliveries = 32
	MaxSchedules  = 8
)

var ErrUnsupportedLanguage = errors.New("unsupported automation language")
var ErrBusy = errors.New("automation executor is busy")

type Language string

// ExecutorMode selects Spinner's local execution mechanism for one script.
// The empty value asks Spinner to use its configured default.
type ExecutorMode string

const (
	ExecutorDefault   ExecutorMode = ""
	ExecutorDirect    ExecutorMode = "direct"
	ExecutorGoroutine ExecutorMode = "goroutine"
	ExecutorErgo      ExecutorMode = "ergo"
)

func ValidExecutorMode(mode ExecutorMode) bool {
	switch mode {
	case ExecutorDefault, ExecutorDirect, ExecutorGoroutine, ExecutorErgo:
		return true
	default:
		return false
	}
}

type Script struct {
	Language Language
	Source   string
	Executor ExecutorMode
}

type Input struct {
	Event     map[string]any
	Variables map[string]any
}

type Delivery struct {
	URL                string              `json:"url"`
	Method             string              `json:"method,omitempty"`
	Headers            map[string][]string `json:"headers,omitempty"`
	Body               string              `json:"body,omitempty"`
	ExpectedStatus     int                 `json:"expected_status,omitempty"`
	ExpectedBodySHA256 string              `json:"expected_body_sha256,omitempty"`
}

type Response struct {
	Status  int                 `json:"status"`
	Headers map[string][]string `json:"headers,omitempty"`
	Body    string              `json:"body,omitempty"`
}

// Schedule is a declarative request to create or update one Planner-owned
// schedule. Key is stable within the script origin, making retries idempotent.
type Schedule struct {
	Key      string         `json:"key"`
	Name     string         `json:"name"`
	Cron     string         `json:"cron"`
	Timezone string         `json:"timezone,omitempty"`
	Language Language       `json:"language"`
	Source   string         `json:"source"`
	Payload  map[string]any `json:"payload,omitempty"`
	Enabled  *bool          `json:"enabled,omitempty"`
}

type Outcome struct {
	RunID      string `json:"-"`
	RunFailed  bool   `json:"-"`
	Variables  map[string]any
	Deliveries []Delivery
	Schedules  []Schedule
	Response   *Response
}

// ScheduleRegistrar is the control-plane capability used by automation hosts.
// Engines emit declarations; they never receive database or registry access.
type ScheduleRegistrar interface {
	RegisterSchedules(context.Context, string, []Schedule) error
}

// Engine is implemented by one optional language package. Engines receive no
// delivery client; they can only return declarative deliveries to the host.
type Engine interface {
	Language() Language
	Execute(context.Context, string, Input) (Outcome, error)
}

// Executor is the narrow capability consumed by ingress and schedule paths.
type Executor interface {
	Execute(context.Context, Script, Input) (Outcome, error)
	Supports(Language) bool
}

// Stream carries raw hook output after a host has selected a route and
// admitted the execution. The host owns HTTP framing and backpressure.
type Stream struct {
	ID     string
	Chunks <-chan []byte
	Done   <-chan error
}

type StreamEngine interface {
	OpenStream(context.Context, string, Input) (*Stream, error)
}

type StreamingExecutor interface {
	OpenStream(context.Context, Script, Input) (*Stream, error)
}

// LanguageCatalog is implemented by executors that can report the engines
// present in the running host. Hosts advertise this list to Planner so it does
// not confuse an installed PHP companion with an available Go runtime.
type LanguageCatalog interface {
	Languages() []Language
}

// Runtime dispatches scripts to explicitly installed language engines.
// A zero-value Runtime supports no languages and therefore fails closed.
type Runtime struct {
	engines map[Language]Engine
}

func NewRuntime(engines ...Engine) *Runtime {
	runtime := &Runtime{engines: make(map[Language]Engine, len(engines))}
	for _, engine := range engines {
		if engine == nil || strings.TrimSpace(string(engine.Language())) == "" {
			continue
		}
		runtime.engines[engine.Language()] = engine
	}
	return runtime
}

func (r *Runtime) Supports(language Language) bool {
	if r == nil {
		return false
	}
	_, ok := r.engines[language]
	return ok
}

func (r *Runtime) Languages() []Language {
	if r == nil {
		return []Language{}
	}
	languages := make([]Language, 0, len(r.engines))
	for language := range r.engines {
		languages = append(languages, language)
	}
	sort.Slice(languages, func(i, j int) bool { return languages[i] < languages[j] })
	return languages
}

// Languages returns a defensive copy of the engines advertised by an
// executor. Custom executors that do not implement LanguageCatalog advertise
// no languages and therefore fail closed in Planner.
func Languages(executor Executor) []Language {
	catalog, ok := executor.(LanguageCatalog)
	if !ok || catalog == nil {
		return []Language{}
	}
	return append([]Language(nil), catalog.Languages()...)
}

func (r *Runtime) Execute(ctx context.Context, script Script, input Input) (Outcome, error) {
	if strings.TrimSpace(script.Source) == "" {
		return Outcome{}, fmt.Errorf("automation source is required")
	}
	if r == nil {
		return Outcome{}, fmt.Errorf("%w %q", ErrUnsupportedLanguage, script.Language)
	}
	engine, ok := r.engines[script.Language]
	if !ok {
		return Outcome{}, fmt.Errorf("%w %q", ErrUnsupportedLanguage, script.Language)
	}

	input.Variables = CloneVariables(input.Variables)
	outcome, err := engine.Execute(ctx, script.Source, input)
	if err != nil {
		return Outcome{}, err
	}
	if outcome.Variables == nil {
		outcome.Variables = input.Variables
	}
	if err := validateOutcome(outcome); err != nil {
		return Outcome{}, fmt.Errorf("%s automation: %w", script.Language, err)
	}
	return outcome, nil
}

func (r *Runtime) OpenStream(ctx context.Context, script Script, input Input) (*Stream, error) {
	if r == nil || strings.TrimSpace(script.Source) == "" {
		return nil, ErrUnsupportedLanguage
	}
	engine, ok := r.engines[script.Language].(StreamEngine)
	if !ok {
		return nil, fmt.Errorf("%w %q", ErrUnsupportedLanguage, script.Language)
	}
	input.Variables = CloneVariables(input.Variables)
	return engine.OpenStream(ctx, script.Source, input)
}

func CloneVariables(values map[string]any) map[string]any {
	cloned := make(map[string]any, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}

func validateOutcome(outcome Outcome) error {
	if len(outcome.Deliveries) > MaxDeliveries {
		return fmt.Errorf("exceeded the delivery limit")
	}
	if outcome.Response != nil && (outcome.Response.Status < 100 || outcome.Response.Status > 599) {
		return fmt.Errorf("returned an invalid response status")
	}
	for _, delivery := range outcome.Deliveries {
		if strings.TrimSpace(delivery.URL) == "" {
			return fmt.Errorf("returned a delivery without a URL")
		}
		if delivery.ExpectedStatus != 0 && (delivery.ExpectedStatus < 100 || delivery.ExpectedStatus > 599) {
			return fmt.Errorf("returned a delivery with an invalid expected response status")
		}
		if delivery.ExpectedBodySHA256 != "" && !bodySHA256Pattern.MatchString(delivery.ExpectedBodySHA256) {
			return fmt.Errorf("returned a delivery with an invalid expected response body digest")
		}
	}
	if len(outcome.Schedules) > MaxSchedules {
		return fmt.Errorf("exceeded the schedule limit")
	}
	seenScheduleKeys := make(map[string]struct{}, len(outcome.Schedules))
	for _, schedule := range outcome.Schedules {
		if err := validateSchedule(schedule); err != nil {
			return err
		}
		if _, exists := seenScheduleKeys[schedule.Key]; exists {
			return fmt.Errorf("returned duplicate schedule key %q", schedule.Key)
		}
		seenScheduleKeys[schedule.Key] = struct{}{}
	}
	return nil
}

var scheduleKeyPattern = regexp.MustCompile(`^[A-Za-z0-9_.:-]+$`)
var bodySHA256Pattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

func validateSchedule(schedule Schedule) error {
	if strings.TrimSpace(schedule.Key) == "" || len(schedule.Key) > 255 || !scheduleKeyPattern.MatchString(schedule.Key) {
		return fmt.Errorf("returned a schedule with an invalid key")
	}
	if strings.TrimSpace(schedule.Name) == "" || len(schedule.Name) > 255 {
		return fmt.Errorf("returned schedule %q without a valid name", schedule.Key)
	}
	if strings.TrimSpace(schedule.Cron) == "" || len(schedule.Cron) > 100 {
		return fmt.Errorf("returned schedule %q without a valid cron expression", schedule.Key)
	}
	if schedule.Timezone != "" && len(schedule.Timezone) > 100 {
		return fmt.Errorf("returned schedule %q with an invalid timezone", schedule.Key)
	}
	if strings.TrimSpace(string(schedule.Language)) == "" || len(schedule.Language) > 64 {
		return fmt.Errorf("returned schedule %q without a valid language", schedule.Key)
	}
	if strings.TrimSpace(schedule.Source) == "" || len(schedule.Source) > 65535 {
		return fmt.Errorf("returned schedule %q without valid source", schedule.Key)
	}
	payload, err := json.Marshal(schedule.Payload)
	if err != nil || len(payload) > 65535 {
		return fmt.Errorf("returned schedule %q with an invalid payload", schedule.Key)
	}
	return nil
}

var _ Executor = (*Runtime)(nil)
var _ LanguageCatalog = (*Runtime)(nil)
