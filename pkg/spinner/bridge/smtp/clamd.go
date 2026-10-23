package smtp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"time"
)

type ScanVerdict struct {
	Infected  bool
	Signature string
}

type MalwareScanner interface {
	Scan(context.Context, []byte) (ScanVerdict, error)
}

// ClamdScanner speaks clamd's framed INSTREAM protocol. The socket must be
// private: clamd's TCP protocol does not authenticate or encrypt requests.
type ClamdScanner struct {
	network string
	address string
	timeout time.Duration
}

func NewClamdScanner(network, address string, timeout time.Duration) (*ClamdScanner, error) {
	if network != "tcp" && network != "unix" {
		return nil, errors.New("clamd network must be tcp or unix")
	}
	if strings.TrimSpace(address) == "" || timeout <= 0 {
		return nil, errors.New("clamd address and positive timeout are required")
	}
	return &ClamdScanner{network: network, address: address, timeout: timeout}, nil
}

func (scanner *ClamdScanner) Scan(ctx context.Context, message []byte) (ScanVerdict, error) {
	if scanner == nil {
		return ScanVerdict{}, errors.New("clamd scanner is not configured")
	}
	ctx, cancel := context.WithTimeout(ctx, scanner.timeout)
	defer cancel()
	connection, err := (&net.Dialer{}).DialContext(ctx, scanner.network, scanner.address)
	if err != nil {
		return ScanVerdict{}, fmt.Errorf("connect to clamd: %w", err)
	}
	defer connection.Close()
	stop := context.AfterFunc(ctx, func() { _ = connection.Close() })
	defer stop()
	deadline, ok := ctx.Deadline()
	if ok {
		if err := connection.SetDeadline(deadline); err != nil {
			return ScanVerdict{}, err
		}
	}
	if _, err := io.WriteString(connection, "zINSTREAM\x00"); err != nil {
		return ScanVerdict{}, fmt.Errorf("send clamd command: %w", err)
	}
	for len(message) > 0 {
		chunk := message
		if len(chunk) > 64*1024 {
			chunk = message[:64*1024]
		}
		if err := binary.Write(connection, binary.BigEndian, uint32(len(chunk))); err != nil {
			return ScanVerdict{}, fmt.Errorf("send clamd chunk length: %w", err)
		}
		if _, err := io.Copy(connection, bytes.NewReader(chunk)); err != nil {
			return ScanVerdict{}, fmt.Errorf("send clamd chunk: %w", err)
		}
		message = message[len(chunk):]
	}
	if err := binary.Write(connection, binary.BigEndian, uint32(0)); err != nil {
		return ScanVerdict{}, fmt.Errorf("end clamd stream: %w", err)
	}
	reply, err := bufio.NewReader(io.LimitReader(connection, 4096)).ReadString(0)
	if err != nil {
		return ScanVerdict{}, fmt.Errorf("read clamd result: %w", err)
	}
	reply = strings.TrimSuffix(reply, "\x00")
	if reply == "stream: OK" {
		return ScanVerdict{}, nil
	}
	if strings.HasPrefix(reply, "stream: ") && strings.HasSuffix(reply, " FOUND") {
		signature := strings.TrimSuffix(strings.TrimPrefix(reply, "stream: "), " FOUND")
		if signature != "" && !strings.ContainsAny(signature, "\x00\r\n") {
			if len(signature) > 255 {
				signature = signature[:255]
			}
			return ScanVerdict{Infected: true, Signature: signature}, nil
		}
	}
	return ScanVerdict{}, fmt.Errorf("clamd did not return a clean or infected verdict")
}

var _ MalwareScanner = (*ClamdScanner)(nil)
