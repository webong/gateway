// dns-delegation-fixture provides a disposable parent referral and recursive
// address view for the local two-Spinner DNS smoke. It is not a DNS server for
// public deployment.
package main

import (
	"flag"
	"log"
	"net"

	"github.com/miekg/dns"
)

func main() {
	address := flag.String("addr", "127.0.0.1:12555", "local UDP fixture address")
	flag.Parse()
	parentSOA := mustRR("example.test. 300 IN SOA ns1.example.net. hostmaster.example.test. 1 3600 600 86400 300")
	delegation := []dns.RR{
		mustRR("first.example.test. 300 IN NS ns1.example.net."),
		mustRR("first.example.test. 300 IN NS ns2.example.net."),
	}
	addressRecord := map[string]dns.RR{
		"ns1.example.net.": mustRR("ns1.example.net. 300 IN A 127.0.0.1"),
		"ns2.example.net.": mustRR("ns2.example.net. 300 IN A 127.0.0.1"),
	}
	packet, err := net.ListenPacket("udp", *address)
	if err != nil {
		log.Fatal(err)
	}
	server := &dns.Server{PacketConn: packet, Handler: dns.HandlerFunc(func(writer dns.ResponseWriter, query *dns.Msg) {
		response := new(dns.Msg)
		response.SetReply(query)
		if len(query.Question) != 1 {
			response.Rcode = dns.RcodeFormatError
		} else {
			question := query.Question[0]
			switch {
			case query.RecursionDesired && question.Qtype == dns.TypeA:
				response.RecursionAvailable = true
				if record := addressRecord[question.Name]; record != nil {
					response.Answer = []dns.RR{record}
				} else {
					response.Rcode = dns.RcodeNameError
				}
			case query.RecursionDesired && question.Qtype == dns.TypeAAAA:
				response.RecursionAvailable = true
				if addressRecord[question.Name] == nil {
					response.Rcode = dns.RcodeNameError
				}
			case !query.RecursionDesired && question.Name == "example.test." && question.Qtype == dns.TypeSOA:
				response.Authoritative = true
				response.Answer = []dns.RR{parentSOA}
			case !query.RecursionDesired && question.Name == "first.example.test." && question.Qtype == dns.TypeNS:
				response.Ns = delegation
			default:
				response.Rcode = dns.RcodeRefused
			}
		}
		_ = writer.WriteMsg(response)
	})}
	if err := server.ActivateAndServe(); err != nil {
		log.Fatal(err)
	}
}

func mustRR(value string) dns.RR {
	record, err := dns.NewRR(value)
	if err != nil {
		log.Fatal(err)
	}
	return record
}
