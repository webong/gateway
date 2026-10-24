package companion

import (
	"errors"
	"testing"
	"time"
)

func TestInvocationRequiresNamedInstalledCodeAndStableIdentity(t *testing.T) {
	deadline := time.Now().Add(time.Minute)
	valid := Invocation{
		ProtocolVersion:  ProtocolVersion,
		Action:           "email.create-ticket",
		CompanionVersion: "1.2.0",
		ExecutionID:      "execution-1",
		IdempotencyKey:   "mail-message-1",
		Deadline:         &deadline,
		Input:            map[string]any{"subject": "Help"},
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid invocation rejected: %v", err)
	}

	invalid := valid
	invalid.Action = "<?php eval($input);"
	if err := invalid.Validate(); !errors.Is(err, ErrInvalidInvocation) {
		t.Fatalf("expected invalid action name, got %v", err)
	}

	invalid = valid
	invalid.CompanionVersion = ""
	invalid.CompanionDigest = ""
	if err := invalid.Validate(); !errors.Is(err, ErrInvalidInvocation) {
		t.Fatalf("expected missing provenance to fail, got %v", err)
	}
}

func TestResultDistinguishesActionFailureFromTransportFailure(t *testing.T) {
	failed := Result{
		ProtocolVersion: ProtocolVersion,
		ExecutionID:     "execution-1",
		Status:          StatusFailed,
		Error:           &Failure{Code: "ticket_rejected", Message: "Ticket was rejected."},
	}
	if err := failed.Validate(); err != nil {
		t.Fatalf("valid failure rejected: %v", err)
	}

	failed.Error = nil
	if err := failed.Validate(); !errors.Is(err, ErrInvalidResult) {
		t.Fatalf("expected missing structured failure to be rejected, got %v", err)
	}
}
