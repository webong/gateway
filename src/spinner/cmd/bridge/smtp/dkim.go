package smtp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"

	"github.com/emersion/go-msgauth/dkim"
)

const maxDKIMVerifications = 5

// DKIMSignatureEvidence represents one verified or rejected signature. Only a
// signature with Result=pass can contribute to DMARC alignment in Planner.
type DKIMSignatureEvidence struct {
	Domain               string `json:"domain"`
	OrganizationalDomain string `json:"organizational_domain,omitempty"`
	Result               string `json:"result"`
}

// DKIMEvidence summarizes bounded verification without altering the original
// RFC 5322 message or trusting an inbound Authentication-Results header.
type DKIMEvidence struct {
	Result     string
	Signatures []DKIMSignatureEvidence
	Truncated  bool
}

// DKIMVerifier permits a deployment to supply controlled DNS lookup behavior.
type DKIMVerifier interface {
	Verify(context.Context, []byte) DKIMEvidence
}

type dnsDKIMVerifier struct {
	lookupTXT func(context.Context, string) ([]string, error)
}

func (v dnsDKIMVerifier) Verify(ctx context.Context, message []byte) DKIMEvidence {
	lookupTXT := v.lookupTXT
	if lookupTXT == nil {
		lookupTXT = net.DefaultResolver.LookupTXT
	}
	verifications, err := dkim.VerifyWithOptions(bytes.NewReader(message), &dkim.VerifyOptions{
		MaxVerifications: maxDKIMVerifications,
		LookupTXT: func(domain string) ([]string, error) {
			return lookupTXT(ctx, domain)
		},
	})
	evidence := DKIMEvidence{Result: "none"}
	if errors.Is(err, dkim.ErrTooManySignatures) {
		evidence.Truncated = true
	} else if err != nil {
		evidence.Result = "temperror"
	}
	hasPass, hasTempError, hasPermError, hasFail := false, false, false, false
	for _, verification := range verifications {
		if verification == nil {
			continue
		}
		signature := DKIMSignatureEvidence{
			Domain:               normalizedMailDomain(verification.Domain),
			OrganizationalDomain: organizationalDomain(verification.Domain),
			Result:               dkimSignatureResult(verification.Err),
		}
		evidence.Signatures = append(evidence.Signatures, signature)
		switch signature.Result {
		case "pass":
			hasPass = true
		case "temperror":
			hasTempError = true
		case "permerror":
			hasPermError = true
		default:
			hasFail = true
		}
	}
	switch {
	case hasPass:
		evidence.Result = "pass"
	case evidence.Result == "temperror" || hasTempError:
		evidence.Result = "temperror"
	case evidence.Truncated || hasPermError:
		evidence.Result = "permerror"
	case hasFail:
		evidence.Result = "fail"
	}
	if ctx.Err() != nil {
		evidence.Result = "temperror"
		for index := range evidence.Signatures {
			if evidence.Signatures[index].Result == "pass" {
				evidence.Signatures[index].Result = "temperror"
			}
		}
	}
	return evidence
}

func dkimSignatureResult(err error) string {
	if err == nil {
		return "pass"
	}
	if dkim.IsTempFail(err) {
		return "temperror"
	}
	if dkim.IsPermFail(err) {
		return "permerror"
	}
	return "fail"
}

func (s *session) dkimAttributes(ctx context.Context, message []byte) map[string]string {
	verifier := s.config.DKIMVerifier
	if verifier == nil {
		verifier = dnsDKIMVerifier{}
	}
	evidence := verifier.Verify(ctx, message)
	attributes := map[string]string{"dkim_result": evidence.Result}
	if len(evidence.Signatures) > 0 {
		signatures, err := json.Marshal(evidence.Signatures)
		if err == nil {
			attributes["dkim_signatures"] = string(signatures)
		}
	}
	if evidence.Truncated {
		attributes["dkim_truncated"] = "true"
	}
	return attributes
}
