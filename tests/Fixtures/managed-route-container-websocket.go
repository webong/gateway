package main

import (
	"bufio"
	"crypto/sha1"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"time"
)

const payload = "gateway-websocket-smoke"

func main() {
	if len(os.Args) < 2 {
		log.Fatal("usage: websocket-fixture server | client ADDRESS")
	}
	switch os.Args[1] {
	case "server":
		server := &http.Server{Addr: ":9091", ReadHeaderTimeout: 5 * time.Second}
		server.Handler = http.HandlerFunc(serveWebSocket)
		log.Fatal(server.ListenAndServe())
	case "client":
		if len(os.Args) != 3 {
			log.Fatal("usage: websocket-fixture client ADDRESS")
		}
		if err := checkWebSocket(os.Args[2]); err != nil {
			log.Fatal(err)
		}
	default:
		log.Fatal("unknown websocket-fixture mode")
	}
}

func serveWebSocket(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/socket/app" || r.Host != "managed.example.test" ||
		!strings.EqualFold(r.Header.Get("Upgrade"), "websocket") || r.Header.Get("Sec-WebSocket-Key") == "" {
		http.Error(w, "invalid WebSocket request", http.StatusBadRequest)
		return
	}
	connection, buffered, err := w.(http.Hijacker).Hijack()
	if err != nil {
		return
	}
	defer connection.Close()
	_ = connection.SetDeadline(time.Now().Add(5 * time.Second))
	accept := websocketAccept(r.Header.Get("Sec-WebSocket-Key"))
	if _, err := fmt.Fprintf(buffered, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: %s\r\n\r\n", accept); err != nil {
		return
	}
	if err := buffered.Flush(); err != nil {
		return
	}
	frame := make([]byte, 2)
	if _, err := io.ReadFull(buffered, frame); err != nil || frame[0] != 0x81 || frame[1]&0x80 == 0 || frame[1]&0x7f != byte(len(payload)) {
		return
	}
	mask := make([]byte, 4)
	message := make([]byte, len(payload))
	if _, err := io.ReadFull(buffered, mask); err != nil {
		return
	}
	if _, err := io.ReadFull(buffered, message); err != nil {
		return
	}
	for index := range message {
		message[index] ^= mask[index%4]
	}
	if string(message) != payload {
		return
	}
	_, _ = buffered.Write(append([]byte{0x81, byte(len(message))}, message...))
	_ = buffered.Flush()
}

func checkWebSocket(address string) error {
	connection, err := net.DialTimeout("tcp", address, 5*time.Second)
	if err != nil {
		return err
	}
	defer connection.Close()
	_ = connection.SetDeadline(time.Now().Add(10 * time.Second))
	key := base64.StdEncoding.EncodeToString([]byte("gateway-smoke-key"))
	if _, err := fmt.Fprintf(connection, "GET /socket/app HTTP/1.1\r\nHost: managed.example.test\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Version: 13\r\nSec-WebSocket-Key: %s\r\n\r\n", key); err != nil {
		return err
	}
	reader := bufio.NewReader(connection)
	status, err := reader.ReadString('\n')
	if err != nil {
		return err
	}
	if !strings.HasPrefix(status, "HTTP/1.1 101 ") {
		return fmt.Errorf("WebSocket upgrade failed: %s", strings.TrimSpace(status))
	}
	accepted := false
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return err
		}
		if line == "\r\n" {
			break
		}
		if strings.EqualFold(strings.TrimSpace(line), "Sec-WebSocket-Accept: "+websocketAccept(key)) {
			accepted = true
		}
	}
	if !accepted {
		return errors.New("WebSocket response lacked a valid accept header")
	}
	mask := []byte{1, 2, 3, 4}
	message := []byte(payload)
	frame := []byte{0x81, 0x80 | byte(len(message))}
	frame = append(frame, mask...)
	for index, value := range message {
		frame = append(frame, value^mask[index%4])
	}
	if _, err := connection.Write(frame); err != nil {
		return err
	}
	response := make([]byte, 2+len(message))
	if _, err := io.ReadFull(reader, response); err != nil {
		return err
	}
	if response[0] != 0x81 || response[1] != byte(len(message)) || string(response[2:]) != payload {
		return fmt.Errorf("unexpected WebSocket echo: %q", response)
	}
	return nil
}

func websocketAccept(key string) string {
	digest := sha1.Sum([]byte(key + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
	return base64.StdEncoding.EncodeToString(digest[:])
}
