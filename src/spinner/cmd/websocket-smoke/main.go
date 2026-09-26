package main

import (
	"flag"
	"log"
	"time"

	"github.com/gorilla/websocket"
)

func main() {
	address := flag.String("addr", "127.0.0.1:28282", "WebSocket listener address")
	path := flag.String("path", "/channels/smoke", "WebSocket route path")
	flag.Parse()

	connection, _, err := websocket.DefaultDialer.Dial("ws://"+*address+*path, nil)
	if err != nil {
		log.Fatalf("dial WebSocket gateway: %v", err)
	}
	defer connection.Close()

	if err := connection.WriteMessage(websocket.TextMessage, []byte("message from WebSocket\n")); err != nil {
		log.Fatalf("write WebSocket message: %v", err)
	}
	if err := connection.WriteControl(
		websocket.CloseMessage,
		websocket.FormatCloseMessage(websocket.CloseNormalClosure, "smoke complete"),
		time.Now().Add(time.Second),
	); err != nil {
		log.Fatalf("close WebSocket connection: %v", err)
	}
}
