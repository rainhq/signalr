package main

import (
	"bytes"
	"compress/flate"
	"context"
	"errors"
	"io/ioutil"
	"log"
	"os"
	"os/signal"
	"time"

	"github.com/rainhq/signalr/v2"
	"golang.org/x/sync/errgroup"
)

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// handle Ctrl-C gracefully
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt)
	go func() {
		select {
		case <-c:
			cancel()
		case <-ctx.Done():
		}
	}()

	dctx, dcancel := context.WithTimeout(ctx, 5*time.Second)
	defer dcancel()

	// Prepare a SignalR client.
	conn, err := signalr.Dial(
		dctx,
		"https://socket.bittrex.com/signalr",
		`[{"name":"c2"}]`,
	)
	if err != nil {
		//nolint:gocritic
		log.Fatal(err)
	}

	client := signalr.NewClient("c2", conn)

	ictx, icancel := context.WithTimeout(ctx, 5*time.Second)
	defer icancel()

	if err := client.Invoke(ictx, "SubscribeToExchangeDeltas", "USD-BTC").Exec(); err != nil {
		log.Fatal(err)
	}

	log.Print("Subscribed to exchange deltas")

	errg, ctx := errgroup.WithContext(ctx)
	errg.Go(func() error { return client.Run(ctx) })
	errg.Go(func() error {
		stream, err := client.Callback(ctx, "uE")
		if err != nil {
			return err
		}
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

			log.Println(string(decompressed))
		}
	})

	err = errg.Wait()
	switch {
	case errors.Is(err, context.Canceled):
		os.Exit(130)
	case err != nil:
		log.Fatal(err)
	}
}
