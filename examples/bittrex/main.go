package main

import (
	"context"
	"log"

	"github.com/rainhq/signalr/v2"
	"github.com/rainhq/signalr/v2/hubs"
	"golang.org/x/sync/errgroup"
)

// For more extensive use cases and capabilities, please see
// https://github.com/carterjones/bittrex.

func main() {
	// Prepare a SignalR client.
	c := signalr.New(
		"socket.bittrex.com",
		"1.5",
		"/signalr",
		`[{"name":"c2"}]`,
		nil,
	)

	// Define message and error handlers.
	msgHandler := func(_ context.Context, msg signalr.Message) error {
		log.Println(msg)
		return nil
	}

	ctx := context.Background()
	errg, ctx := errgroup.WithContext(ctx)

	errg.Go(func() error { return c.Run(ctx, msgHandler) })
	errg.Go(func() error {
		// Subscribe to the USDT-BTC feed.
		return c.Send(hubs.ClientMsg{
			H: "corehub",
			M: "SubscribeToExchangeDeltas",
			A: []interface{}{"USDT-BTC"},
			I: 1,
		})
	})

	if err := errg.Wait(); err != nil {
		log.Fatal(err)
	}
}
