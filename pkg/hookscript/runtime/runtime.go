// Package runtime implements the HookScript automation guest. The guest owns
// no PHP runtime: it invokes the trusted HookScript companion installed in
// Planner through the plugin session's declared reverse-call interface.
package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	plugin "github.com/webong/ext/pkg/plugin-go/contract"
	"github.com/webong/gateway/driver/automation"
	"github.com/webong/gateway/driver/companion"
)

const (
	Language         automation.Language = "webhookscript"
	CompanionAction                      = "gateway.hookscript.execute"
	CompanionVersion                     = "1.0.0"
)

var companionContract = plugin.ContractRef{Name: "gateway.companion", Version: companion.ProtocolVersion}

type Engine struct {
	timeout        time.Duration
	maxSourceBytes int64
}

func New(opts ...Option) *Engine {
	e := &Engine{
		timeout:        5 * time.Second,
		maxSourceBytes: 65536,
	}
	for _, opt := range opts {
		opt(e)
	}
	return e
}

func (e *Engine) Language() automation.Language { return Language }

// ReverseContracts declares the one Planner capability this guest needs. The
// Spinner host rejects every undeclared companion action and action-name
// substitution inside the invocation payload.
func (e *Engine) ReverseContracts() []plugin.Contract {
	return []plugin.Contract{{
		ContractRef: companionContract,
		Operations:  []plugin.Operation{{Name: CompanionAction}},
	}}
}

type Option func(*Engine)

func WithTimeout(d time.Duration) Option {
	return func(e *Engine) { e.timeout = d }
}

func WithMaxSourceBytes(n int64) Option {
	return func(e *Engine) { e.maxSourceBytes = n }
}

// Execute asks the enabled PHP companion to evaluate one HookScript and
// converts its structured result back into the language-neutral automation
// outcome consumed by Spinner.
func (e *Engine) Execute(ctx context.Context, source string, input automation.Input) (automation.Outcome, error) {
	if strings.TrimSpace(source) == "" {
		return automation.Outcome{}, errors.New("hookscript source is required")
	}
	if e.maxSourceBytes <= 0 || int64(len(source)) > e.maxSourceBytes {
		return automation.Outcome{}, errors.New("hookscript source exceeds configured limit")
	}
	caller := plugin.ReverseFrom(ctx)
	if caller == nil {
		return automation.Outcome{}, errors.New("hookscript companion host is unavailable")
	}

	actionInput := map[string]any{
		"source":    source,
		"event":     objectOrEmpty(input.Event),
		"variables": objectOrEmpty(input.Variables),
	}
	fingerprint, err := json.Marshal(actionInput)
	if err != nil {
		return automation.Outcome{}, fmt.Errorf("encode hookscript companion input: %w", err)
	}
	digest := sha256.Sum256(fingerprint)
	digestText := hex.EncodeToString(digest[:])

	deadline := time.Now().Add(e.timeout)
	if existing, ok := ctx.Deadline(); ok && existing.Before(deadline) {
		deadline = existing
	}
	invocation := companion.Invocation{
		ProtocolVersion:  companion.ProtocolVersion,
		Action:           CompanionAction,
		CompanionVersion: CompanionVersion,
		ExecutionID:      "hookscript-" + digestText[:32],
		IdempotencyKey:   "hookscript:" + digestText,
		Deadline:         &deadline,
		Input:            actionInput,
	}
	payload, err := json.Marshal(invocation)
	if err != nil {
		return automation.Outcome{}, fmt.Errorf("encode hookscript companion invocation: %w", err)
	}
	raw, err := caller.Call(ctx, companionContract, CompanionAction, payload)
	if err != nil {
		return automation.Outcome{}, fmt.Errorf("invoke hookscript companion: %w", err)
	}

	var result companion.Result
	if err := json.Unmarshal(raw, &result); err != nil {
		return automation.Outcome{}, fmt.Errorf("decode hookscript companion result: %w", err)
	}
	if err := result.Validate(); err != nil {
		return automation.Outcome{}, fmt.Errorf("validate hookscript companion result: %w", err)
	}
	if result.ExecutionID != invocation.ExecutionID {
		return automation.Outcome{}, errors.New("hookscript companion returned the wrong execution ID")
	}
	if result.Status == companion.StatusInProgress {
		return automation.Outcome{}, errors.New("hookscript companion execution is still in progress")
	}
	if result.Status == companion.StatusFailed {
		return automation.Outcome{}, fmt.Errorf("hookscript companion failed: %s", result.Error.Message)
	}

	outcomeValue, ok := result.Output["outcome"]
	if !ok || outcomeValue == nil {
		return automation.Outcome{}, errors.New("hookscript companion returned no automation outcome")
	}
	encodedOutcome, err := json.Marshal(outcomeValue)
	if err != nil {
		return automation.Outcome{}, fmt.Errorf("encode hookscript companion outcome: %w", err)
	}
	var outcome automation.Outcome
	if err := json.Unmarshal(encodedOutcome, &outcome); err != nil {
		return automation.Outcome{}, fmt.Errorf("decode hookscript companion outcome: %w", err)
	}
	return outcome, nil
}

func objectOrEmpty(value map[string]any) map[string]any {
	if value == nil {
		return map[string]any{}
	}
	return value
}

var _ automation.Engine = (*Engine)(nil)

// Plugin returns the engine for composition roots and dynamic loaders.
func Plugin() (automation.Engine, error) { return New(), nil }
