package smtp

import (
	"errors"
	"net"
	"testing"
	"time"
)

func TestLimitedListenerWaitsForConnectionSlot(t *testing.T) {
	base, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	listener := newLimitedListener(base, 1)
	defer listener.Close()

	firstClient, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer firstClient.Close()
	firstServer, err := listener.Accept()
	if err != nil {
		t.Fatal(err)
	}

	accepted := make(chan net.Conn, 1)
	acceptErr := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			acceptErr <- err
			return
		}
		accepted <- conn
	}()
	secondClient, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer secondClient.Close()

	select {
	case conn := <-accepted:
		conn.Close()
		t.Fatal("accepted a second connection while the first slot was occupied")
	case err := <-acceptErr:
		t.Fatalf("accept failed early: %v", err)
	case <-time.After(30 * time.Millisecond):
	}
	if err := firstServer.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case conn := <-accepted:
		conn.Close()
	case err := <-acceptErr:
		t.Fatalf("accept failed after slot release: %v", err)
	case <-time.After(time.Second):
		t.Fatal("second connection was not accepted after slot release")
	}
}

func TestLimitedListenerCloseUnblocksFullAccept(t *testing.T) {
	base, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	listener := newLimitedListener(base, 1)
	firstClient, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer firstClient.Close()
	firstServer, err := listener.Accept()
	if err != nil {
		t.Fatal(err)
	}
	defer firstServer.Close()

	acceptErr := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if conn != nil {
			conn.Close()
		}
		acceptErr <- err
	}()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-acceptErr:
		if !errors.Is(err, net.ErrClosed) {
			t.Fatalf("expected closed listener, got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("closing the listener did not unblock Accept")
	}
}
