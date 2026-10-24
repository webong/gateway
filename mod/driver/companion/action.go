// Package companion defines the transport-neutral contract used by Spinner
// extensions to invoke trusted PHP companion actions in the Planner host.
package companion

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

const ProtocolVersion = "v1"

type Status string

const (
	StatusSucceeded  Status = "succeeded"
	StatusFailed     Status = "failed"
	StatusInProgress Status = "in_progress"
)

var (
	actionPattern = regexp.MustCompile(`^[a-z0-9]+(?:[._-][a-z0-9]+)*$`)
	digestPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

	ErrInvalidInvocation = errors.New("invalid companion action invocation")
	ErrInvalidResult     = errors.New("invalid companion action result")
)

// Capability identifies one trusted PHP action installed in the Planner host.
type Capability struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
	Digest  string `json:"digest,omitempty"`
}

func (c Capability) Validate() error {
	if !validAction(c.Name) {
		return fmt.Errorf("%w: action name is invalid", ErrInvalidInvocation)
	}
	if strings.TrimSpace(c.Version) == "" && strings.TrimSpace(c.Digest) == "" {
		return fmt.Errorf("%w: companion version or digest is required", ErrInvalidInvocation)
	}
	if len(c.Version) > 64 {
		return fmt.Errorf("%w: companion version is too long", ErrInvalidInvocation)
	}
	if c.Digest != "" && !digestPattern.MatchString(c.Digest) {
		return fmt.Errorf("%w: companion digest must be a lowercase SHA-256 digest", ErrInvalidInvocation)
	}
	return nil
}

// Invocation is data, never source code. The action name resolves trusted PHP
// that was installed with the Planner application.
type Invocation struct {
	ProtocolVersion  string         `json:"protocol_version"`
	Action           string         `json:"action"`
	CompanionVersion string         `json:"companion_version,omitempty"`
	CompanionDigest  string         `json:"companion_digest,omitempty"`
	ExecutionID      string         `json:"execution_id"`
	IdempotencyKey   string         `json:"idempotency_key"`
	Deadline         *time.Time     `json:"deadline,omitempty"`
	Input            map[string]any `json:"input"`
}

func (i Invocation) Validate() error {
	if i.ProtocolVersion != ProtocolVersion {
		return fmt.Errorf("%w: unsupported protocol version %q", ErrInvalidInvocation, i.ProtocolVersion)
	}
	if !validAction(i.Action) {
		return fmt.Errorf("%w: action name is invalid", ErrInvalidInvocation)
	}
	if strings.TrimSpace(i.CompanionVersion) == "" && strings.TrimSpace(i.CompanionDigest) == "" {
		return fmt.Errorf("%w: companion version or digest is required", ErrInvalidInvocation)
	}
	if len(i.CompanionVersion) > 64 {
		return fmt.Errorf("%w: companion version is too long", ErrInvalidInvocation)
	}
	if i.CompanionDigest != "" && !digestPattern.MatchString(i.CompanionDigest) {
		return fmt.Errorf("%w: companion digest must be a lowercase SHA-256 digest", ErrInvalidInvocation)
	}
	if strings.TrimSpace(i.ExecutionID) == "" || len(i.ExecutionID) > 128 {
		return fmt.Errorf("%w: execution ID is required and cannot exceed 128 bytes", ErrInvalidInvocation)
	}
	if strings.TrimSpace(i.IdempotencyKey) == "" || len(i.IdempotencyKey) > 255 {
		return fmt.Errorf("%w: idempotency key is required and cannot exceed 255 bytes", ErrInvalidInvocation)
	}
	if i.Deadline != nil && !i.Deadline.After(time.Now()) {
		return fmt.Errorf("%w: deadline has expired", ErrInvalidInvocation)
	}
	if i.Input == nil {
		return fmt.Errorf("%w: input must be an object", ErrInvalidInvocation)
	}
	return nil
}

type Failure struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable"`
}

type Result struct {
	ProtocolVersion string         `json:"protocol_version"`
	ExecutionID     string         `json:"execution_id"`
	Status          Status         `json:"status"`
	Output          map[string]any `json:"output,omitempty"`
	Error           *Failure       `json:"error,omitempty"`
}

func (r Result) Validate() error {
	if r.ProtocolVersion != ProtocolVersion {
		return fmt.Errorf("%w: unsupported protocol version %q", ErrInvalidResult, r.ProtocolVersion)
	}
	if strings.TrimSpace(r.ExecutionID) == "" {
		return fmt.Errorf("%w: execution ID is required", ErrInvalidResult)
	}
	switch r.Status {
	case StatusSucceeded:
		if r.Error != nil {
			return fmt.Errorf("%w: successful result cannot contain an error", ErrInvalidResult)
		}
	case StatusFailed:
		if r.Error == nil || strings.TrimSpace(r.Error.Code) == "" || strings.TrimSpace(r.Error.Message) == "" {
			return fmt.Errorf("%w: failed result requires an error", ErrInvalidResult)
		}
	case StatusInProgress:
		if r.Error != nil {
			return fmt.Errorf("%w: in-progress result cannot contain an error", ErrInvalidResult)
		}
	default:
		return fmt.Errorf("%w: unsupported status %q", ErrInvalidResult, r.Status)
	}
	return nil
}

// Runner is the deep interface presented to Spinner extensions. Implementations
// hide private HTTP or in-process Planner transport and protocol validation.
type Runner interface {
	Capabilities(context.Context) ([]Capability, error)
	Invoke(context.Context, Invocation) (Result, error)
}

func validAction(action string) bool {
	return len(action) <= 160 && actionPattern.MatchString(action)
}
