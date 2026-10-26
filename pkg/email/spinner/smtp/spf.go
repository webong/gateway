package smtp

import (
	"context"
	"net"
	"strings"

	"github.com/miekg/dns"
	"github.com/wttw/spf"
)

// SPFEvidence is the result of checking one SMTP envelope identity against the
// actual TCP peer. It is evidence for Planner, not an SMTP acceptance decision.
type SPFEvidence struct {
	Result   string
	Identity string
	Domain   string
}

// SPFEvaluator permits a deployment to supply a controlled DNS resolver and
// makes the transaction boundary testable without external DNS.
type SPFEvaluator interface {
	Evaluate(context.Context, net.IP, string, string) SPFEvidence
}

type dnsSPFEvaluator struct{ resolver spf.Resolver }

// NewDNSSPFEvaluator binds SPF evaluation to a controlled DNS capability.
func NewDNSSPFEvaluator(resolver spf.Resolver) SPFEvaluator {
	return dnsSPFEvaluator{resolver: resolver}
}

func (e dnsSPFEvaluator) Evaluate(ctx context.Context, peer net.IP, from, helo string) SPFEvidence {
	identity, domain, sender := spfIdentity(from, helo)
	if domain == "" {
		return SPFEvidence{Result: "none"}
	}
	checker := spf.NewChecker()
	if e.resolver != nil {
		checker.Resolver = e.resolver
	}
	result := checker.CheckHost(ctx, peer, dns.Fqdn(domain), sender, helo)
	if ctx.Err() != nil {
		return SPFEvidence{Result: "temperror", Identity: identity, Domain: domain}
	}
	return SPFEvidence{Result: result.Type.String(), Identity: identity, Domain: domain}
}

func spfIdentity(from, helo string) (identity, domain, sender string) {
	if from != "" {
		at := strings.LastIndexByte(from, '@')
		if at < 1 || at == len(from)-1 {
			return "", "", ""
		}
		return "mailfrom", from[at+1:], from
	}
	if helo != "" {
		return "helo", helo, "postmaster@" + helo
	}
	return "", "", ""
}

func (s *session) spfAttributes(ctx context.Context) map[string]string {
	attributes := map[string]string{"spf_result": "unavailable"}
	peerIP, ok := smtpPeerIP(s.remoteAddr)
	if !ok {
		return attributes
	}
	peer := net.ParseIP(peerIP)
	if peer == nil {
		return attributes
	}
	evaluator := s.config.SPFEvaluator
	if evaluator == nil {
		evaluator = dnsSPFEvaluator{}
	}
	evidence := evaluator.Evaluate(ctx, peer, s.from, s.helo)
	attributes["spf_result"] = evidence.Result
	if evidence.Identity != "" {
		attributes["spf_identity"] = evidence.Identity
	}
	if evidence.Domain != "" {
		attributes["spf_domain"] = evidence.Domain
		if organization := organizationalDomain(evidence.Domain); organization != "" {
			attributes["spf_organizational_domain"] = organization
		}
	}
	return attributes
}
