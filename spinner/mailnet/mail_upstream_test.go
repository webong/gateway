package mailnet

import (
	"context"
	"net"
	"testing"
	"time"
)

func TestMailEgressRejectsPrivateAndSpecialAddressesByDefault(t *testing.T) {
	for _, address := range []string{"127.0.0.1", "::1", "10.2.3.4", "172.20.1.4", "192.168.1.2", "169.254.169.254", "100.64.1.1", "192.0.2.5", "2001:db8::1"} {
		if mailAddressAllowed(net.ParseIP(address), "public") {
			t.Errorf("public mail scope accepted %s", address)
		}
	}
	if !mailAddressAllowed(net.ParseIP("8.8.8.8"), "public") ||
		!mailAddressAllowed(net.ParseIP("10.2.3.4"), "private") ||
		mailAddressAllowed(net.ParseIP("127.0.0.1"), "private") ||
		mailAddressAllowed(net.ParseIP("169.254.169.254"), "private") {
		t.Fatal("mail network scopes did not apply their intended IP policy")
	}
}

func TestMailDialPinsValidatedDNSAddress(t *testing.T) {
	var dialed []string
	lookup := func(_ context.Context, host string) ([]net.IPAddr, error) {
		if host != "provider.example" {
			t.Fatalf("unexpected DNS host %q", host)
		}
		return []net.IPAddr{{IP: net.ParseIP("10.2.3.4")}, {IP: net.ParseIP("8.8.8.8")}}, nil
	}
	dial := func(_ context.Context, network, address string) (net.Conn, error) {
		if network != "tcp" {
			t.Fatalf("unexpected network %q", network)
		}
		dialed = append(dialed, address)
		connection, peer := net.Pipe()
		_ = peer.Close()
		return connection, nil
	}
	connection, err := dialMailUpstream(context.Background(), "provider.example:993", "public", time.Second, lookup, dial)
	if err != nil {
		t.Fatal(err)
	}
	_ = connection.Close()
	if len(dialed) != 1 || dialed[0] != "8.8.8.8:993" {
		t.Fatalf("mail dial was not pinned to the checked public IP: %v", dialed)
	}

	dialed = nil
	lookup = func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("10.2.3.4")}}, nil
	}
	if _, err := dialMailUpstream(context.Background(), "provider.example:993", "public", time.Second, lookup, dial); err == nil || len(dialed) != 0 {
		t.Fatalf("private DNS answer was dialed under public scope: %v, %v", dialed, err)
	}
	connection, err = dialMailUpstream(context.Background(), "provider.example:993", "private", time.Second, lookup, dial)
	if err != nil {
		t.Fatal(err)
	}
	_ = connection.Close()
	if len(dialed) != 1 || dialed[0] != "10.2.3.4:993" {
		t.Fatalf("explicit private scope did not pin its approved IP: %v", dialed)
	}
}

func TestMailEndpointRequiresValidShapeAndScope(t *testing.T) {
	if !ValidMailUpstream("mail.example:993", "mail.example", "public") ||
		ValidMailUpstream("mail.example:0", "mail.example", "public") ||
		ValidMailUpstream("mail.example:993", "mail.example", "unknown") ||
		ValidMailUpstream("mail.example:993", "mail.example/path", "public") {
		t.Fatal("mail endpoint validation accepted malformed input")
	}
}
