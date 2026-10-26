package spinner

import (
	"context"
	"net"
	"testing"
	"time"

	mdns "github.com/miekg/dns"
)

func TestRecursiveResolverExchangesOneBoundedQuery(t *testing.T) {
	server := &mdns.Server{Net: "udp", Handler: mdns.HandlerFunc(func(writer mdns.ResponseWriter, request *mdns.Msg) {
		response := new(mdns.Msg)
		response.SetReply(request)
		response.RecursionAvailable = true
		response.Answer = []mdns.RR{&mdns.MX{Hdr: mdns.RR_Header{Name: "example.test.", Rrtype: mdns.TypeMX, Class: mdns.ClassINET, Ttl: 60}, Preference: 10, Mx: "mail.example.test."}}
		_ = writer.WriteMsg(response)
	})}
	connection, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server.PacketConn = connection
	go func() { _ = server.ActivateAndServe() }()
	t.Cleanup(func() { _ = server.Shutdown() })

	resolver, err := NewRecursiveResolver(connection.LocalAddr().String(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	query := new(mdns.Msg)
	query.SetQuestion("example.test.", mdns.TypeMX)
	wire, _ := query.Pack()
	answer, err := resolver.Exchange(t.Context(), wire)
	if err != nil {
		t.Fatal(err)
	}
	response := new(mdns.Msg)
	if err := response.Unpack(answer); err != nil || len(response.Answer) != 1 || !response.RecursionAvailable {
		t.Fatalf("unexpected recursive answer: %+v err=%v", response, err)
	}
}

func TestRecursiveResolverRejectsTransferQueries(t *testing.T) {
	resolver := &RecursiveResolver{servers: []string{"127.0.0.1:53"}, timeout: time.Second}
	query := new(mdns.Msg)
	query.SetAxfr("example.test.")
	wire, _ := query.Pack()
	if _, err := resolver.Exchange(context.Background(), wire); err == nil {
		t.Fatal("zone transfer query was accepted")
	}
}
