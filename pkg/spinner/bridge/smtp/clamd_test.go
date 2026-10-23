package smtp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"net"
	"os"
	"testing"
	"time"
)

func TestClamdScannerStreamsExactMessageAndParsesVerdicts(t *testing.T) {
	for _, test := range []struct {
		name      string
		reply     string
		infected  bool
		wantError bool
	}{
		{"clean", "stream: OK\x00", false, false},
		{"infected", "stream: Eicar-Test-Signature FOUND\x00", true, false},
		{"scanner error", "stream: INSTREAM size limit exceeded ERROR\x00", false, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			message := bytes.Repeat([]byte("test"), 20_000)
			served := make(chan error, 1)
			go func() {
				connection, err := listener.Accept()
				if err != nil {
					served <- err
					return
				}
				defer connection.Close()
				reader := bufio.NewReader(connection)
				command, err := reader.ReadString(0)
				if err != nil {
					served <- err
					return
				}
				if command != "zINSTREAM\x00" {
					served <- io.ErrUnexpectedEOF
					return
				}
				var received []byte
				for {
					var length uint32
					if err := binary.Read(reader, binary.BigEndian, &length); err != nil {
						served <- err
						return
					}
					if length == 0 {
						break
					}
					chunk := make([]byte, length)
					if _, err := io.ReadFull(reader, chunk); err != nil {
						served <- err
						return
					}
					received = append(received, chunk...)
				}
				if !bytes.Equal(received, message) {
					served <- io.ErrUnexpectedEOF
					return
				}
				_, err = io.WriteString(connection, test.reply)
				served <- err
			}()
			scanner, err := NewClamdScanner("tcp", listener.Addr().String(), time.Second)
			if err != nil {
				t.Fatal(err)
			}
			verdict, err := scanner.Scan(context.Background(), message)
			if (err != nil) != test.wantError || verdict.Infected != test.infected {
				t.Fatalf("unexpected verdict %+v, error %v", verdict, err)
			}
			if err := <-served; err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestClamdScannerAgainstRealService(t *testing.T) {
	address := os.Getenv("GATEWAY_TEST_CLAMD_ADDR")
	if address == "" {
		t.Skip("set GATEWAY_TEST_CLAMD_ADDR to run against a real clamd service")
	}
	scanner, err := NewClamdScanner("tcp", address, 20*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	clean, err := scanner.Scan(context.Background(), []byte("Subject: clean\r\n\r\nordinary test mail\r\n"))
	if err != nil || clean.Infected {
		t.Fatalf("real clamd did not accept clean mail: %+v, %v", clean, err)
	}
	// EICAR's harmless 68-byte standard test file exercises signature detection.
	const eicar = "X5O!P%@AP[4\\PZX54(P^)7CC)7}$EICAR-STANDARD-ANTIVIRUS-TEST-FILE!$H+H*"
	if len(eicar) != 68 {
		t.Fatal("invalid EICAR test string")
	}
	blocked, err := scanner.Scan(context.Background(), []byte(eicar))
	if err != nil || !blocked.Infected || blocked.Signature == "" {
		t.Fatalf("real clamd did not detect EICAR: %+v, %v", blocked, err)
	}
}

func TestClamdScannerRejectsInvalidConfiguration(t *testing.T) {
	if _, err := NewClamdScanner("udp", "127.0.0.1:3310", time.Second); err == nil {
		t.Fatal("accepted unsupported network")
	}
	if _, err := NewClamdScanner("tcp", "", time.Second); err == nil {
		t.Fatal("accepted empty address")
	}
	if _, err := NewClamdScanner("tcp", "127.0.0.1:3310", 0); err == nil {
		t.Fatal("accepted missing timeout")
	}
}
