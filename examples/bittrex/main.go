package main

import (
	"bytes"
	"compress/flate"
	"context"
	"fmt"
	"io/ioutil"
	"log"

	"github.com/rainhq/signalr/v2"
	"golang.org/x/sync/errgroup"
)

func main() {
	ctx := context.Background()

	// Prepare a SignalR client.
	conn, err := signalr.Dial(
		ctx,
		"https://socket.bittrex.com/signalr",
		`[{"name":"c2"}]`,
	)
	if err != nil {
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
		stream := client.Callback("uE")
		defer stream.Close()

		for {
			var data []byte
			if err := stream.Read(ctx, &data); err != nil {
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

	if err := errg.Wait(); err != nil {
		log.Fatal(err)
	}
}
