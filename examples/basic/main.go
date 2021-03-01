package main

import (
	"context"
	"log"

	"github.com/rainhq/signalr/v2"
)

func main() {
	ctx := context.Background()

	// Prepare a SignalR client.
	c, err := signalr.Dial(
		ctx,
		"https://fake-server.definitely-not-real/signalr",
		`[{"name":"awesomehub"}]`,
	)
	if err != nil {
		log.Fatal(err)
	}

	var msg signalr.Message
	for {
		if err := c.ReadMessage(ctx, &msg); err != nil {
			log.Fatal(err)
		}

		log.Println(msg)
	}
}
