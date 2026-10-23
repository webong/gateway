package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

func main() {
	address := flag.String("addr", "127.0.0.1:18382", "Gateway listener address")
	hostname := flag.String("hostname", "reverb.example.test", "managed Reverb public hostname")
	path := flag.String("path", "/reverb/app/reverb-smoke-key", "managed Reverb application path")
	flag.Parse()

	endpoint := "ws://" + *address + *path + "?protocol=7&client=js&version=8.4.0&flash=false"
	headers := http.Header{"Host": []string{*hostname}, "Origin": []string{"https://client.example.test"}}
	connection, response, err := websocket.DefaultDialer.Dial(endpoint, headers)
	if err != nil {
		if response != nil {
			log.Fatalf("dial managed Reverb: status=%d error=%v", response.StatusCode, err)
		}
		log.Fatalf("dial managed Reverb: %v", err)
	}
	defer connection.Close()
	_ = connection.SetReadDeadline(time.Now().Add(5 * time.Second))

	_, payload, err := connection.ReadMessage()
	if err != nil {
		log.Fatalf("read managed Reverb handshake: %v", err)
	}
	var message struct {
		Event string `json:"event"`
	}
	if err := json.Unmarshal(payload, &message); err != nil {
		log.Fatalf("decode managed Reverb handshake: %v", err)
	}
	if message.Event != "pusher:connection_established" {
		log.Fatalf("unexpected managed Reverb handshake: %s", strings.TrimSpace(string(payload)))
	}

	fmt.Println("Managed Reverb WebSocket handshake passed.")
}
