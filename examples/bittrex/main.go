package main

import (
	"bytes"
	"compress/flate"
	"context"
	"errors"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"os/signal"

	"github.com/rainhq/signalr/v2"
	"golang.org/x/sync/errgroup"
)

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt)

	go func() {
		select {
		case <-c:
			cancel()
		case <-ctx.Done():
		}
	}()

	// Prepare a SignalR client.
	conn, err := signalr.Dial(
		ctx,
		"https://socket.bittrex.com/signalr",
		`[{"name":"c2"}]`,
	)
	if err != nil {
		//nolint:gocritic
		log.Fatal(err)
	}

	client := signalr.NewClient("c2", conn)

	if err := client.Invoke(ctx, "SubscribeToExchangeDeltas", "USD-BTC").Exec(); err != nil {
		log.Fatal(err)
	}

	log.Print("Subscribed to exchange deltas")

	errg, ctx := errgroup.WithContext(ctx)
	errg.Go(func() error { return client.Run(ctx) })
	errg.Go(func() error {
		stream := client.Callback(ctx, "uE")
		defer stream.Close()

		for {
			var data []byte
			if err := stream.Read(&data); err != nil {
				return err
			}

			reader := flate.NewReader(bytes.NewReader(data))
			decompressed, err := ioutil.ReadAll(reader)
			if err != nil {
				return err
			}

			fmt.Println(string(decompressed))
		}
	})

	if err := errg.Wait(); err != nil && !errors.Is(err, context.Canceled) {
		log.Fatal(err)
	}
}
