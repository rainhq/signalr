package main

import (
	"context"
	"log"

	"github.com/rainhq/signalr/v2"
)

func main() {
	// Prepare a SignalR client.
	c := signalr.New(
		"fake-server.definitely-not-real",
		"1.5",
		"/signalr",
		`[{"name":"awesomehub"}]`,
		nil,
	)

	// Define message and error handlers.
	msgHandler := func(_ context.Context, msg signalr.Message) error {
		log.Println(msg)
		return nil
	}

	// Start the connection.
	if err := c.Run(context.Background(), msgHandler); err != nil {
		log.Fatal(err)
	}

	// Wait indefinitely.
	select {}
}
