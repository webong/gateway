// Package attributes defines private metadata understood only by the Email
// Extension's ingress, delivery adapters, and failure handler.
package attributes

const (
	FailureNoticeTo   = "email_failure_notice_to"
	DKIMSigningDomain = "email_dkim_signing_domain"
)
