package spinner

import (
	"strconv"
	"strings"
	"time"

	mdns "github.com/miekg/dns"
	bridge "github.com/webong/gateway/arranger/integration"
)

const maxObservedAnswerTypes = 16

type ObservationQueue interface {
	Enqueue(bridge.Observation) bool
}

type dnsObservation struct {
	id             string
	started        time.Time
	question       mdns.Question
	sourceAddress  string
	transport      string
	mode           string
	zone           string
	hook           string
	data           string
	resolver       string
	decisionAction string
	decisionStatus int
	failure        string
}

func newDNSObservation(writer mdns.ResponseWriter, question mdns.Question, id string) *dnsObservation {
	return &dnsObservation{
		id: id, started: time.Now().UTC(), question: question,
		sourceAddress: remoteAddress(writer), transport: transport(writer),
	}
}

func (s *Server) writeObservedResponse(writer mdns.ResponseWriter, request, response *mdns.Msg, observed *dnsObservation) {
	if err := s.writeResponse(writer, request, response); err != nil && observed != nil && observed.failure == "" {
		observed.failure = "response_write"
	}
	if s.observations == nil || observed == nil || observed.mode == "" {
		return
	}
	_ = s.observations.Enqueue(observed.document(response))
}

func (o *dnsObservation) document(response *mdns.Msg) bridge.Observation {
	attributes := map[string]string{
		"mode":          o.mode,
		"qname":         canonicalName(o.question.Name),
		"qtype":         typeName(o.question.Qtype),
		"qclass":        className(o.question.Qclass),
		"query_id":      strconv.Itoa(int(response.Id)),
		"response_code": strconv.Itoa(response.Rcode),
		"response_name": mdns.RcodeToString[response.Rcode],
		"authoritative": strconv.FormatBool(response.Authoritative),
		"truncated":     strconv.FormatBool(response.Truncated),
		"answer_count":  strconv.Itoa(len(response.Answer)),
		"elapsed_ms":    strconv.FormatInt(time.Since(o.started).Milliseconds(), 10),
	}
	optionalObservationAttribute(attributes, "source_address", o.sourceAddress)
	optionalObservationAttribute(attributes, "transport", o.transport)
	optionalObservationAttribute(attributes, "answer_types", observedAnswerTypes(response.Answer))
	optionalObservationAttribute(attributes, "zone", o.zone)
	optionalObservationAttribute(attributes, "hook", o.hook)
	optionalObservationAttribute(attributes, "data", o.data)
	optionalObservationAttribute(attributes, "resolver", o.resolver)
	optionalObservationAttribute(attributes, "decision_action", o.decisionAction)
	if o.decisionStatus != 0 {
		attributes["decision_status_code"] = strconv.Itoa(o.decisionStatus)
	}
	optionalObservationAttribute(attributes, "failure", o.failure)

	return bridge.Observation{
		Version: bridge.ObservationVersion, ID: o.id, Type: "dns.query",
		OccurredAt: o.started, Attributes: attributes,
	}
}

func observedAnswerTypes(records []mdns.RR) string {
	counts := make(map[string]int)
	order := make([]string, 0, maxObservedAnswerTypes)
	for _, record := range records {
		name := typeName(record.Header().Rrtype)
		if _, exists := counts[name]; !exists {
			if len(order) >= maxObservedAnswerTypes {
				continue
			}
			order = append(order, name)
		}
		counts[name]++
	}
	parts := make([]string, 0, len(order))
	for _, name := range order {
		parts = append(parts, name+":"+strconv.Itoa(counts[name]))
	}
	return strings.Join(parts, ",")
}

func optionalObservationAttribute(attributes map[string]string, key, value string) {
	if value != "" {
		attributes[key] = value
	}
}
