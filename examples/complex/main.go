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
		map[string]string{"custom-key": "custom-value"},
	)

	// Perform any optional modifications to the client here. Read the docs for
	// all the available options that are exposed via public fields.

	// Define message and error handlers.
	msgHandler := func(_ context.Context, msg signalr.Message) error {
		log.Println(msg)
		return nil
	}

	ctx := context.Background()

	// Manually perform the initialization routine.
	if err := c.Negotiate(ctx); err != nil {
		log.Fatal(err)
	}

	conn, err := c.Connect(ctx)
	if err != nil {
		log.Fatal(err)
	}

	if err := c.Start(ctx, conn); err != nil {
		log.Fatal(err)
	}

	// Begin the message reading loop.
	if err := c.ReadMessages(ctx, msgHandler); err != nil {
		log.Fatal(err)
	}
}
