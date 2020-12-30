package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"time"

	"github.com/rainhq/signalr/v2"
	"github.com/rainhq/signalr/v2/bittrex"
	"golang.org/x/sync/errgroup"
)

func main() {
	config := parseArgs(os.Args[1:])

	err := run(config)
	switch {
	case errors.Is(err, context.Canceled):
		os.Exit(130)
	case err != nil:
		log.Fatal(err)
	}
}

func run(config *Config) error {
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
		"https://socket-v3.bittrex.com/signalr",
		`[{"name":"c3"}]`,
	)
	if err != nil {
		return fmt.Errorf("failed to connect to bittrex: %w", err)
	}

	signalrClient := signalr.NewClient("c3", conn)
	client := bittrex.NewClient(http.DefaultClient, signalrClient, config.APIKey, config.APISecret)

	errg, ctx := errgroup.WithContext(ctx)
	errg.Go(func() error { return client.Run(ctx) })
	errg.Go(func() error { return config.Command.Run(ctx, client) })

	return errg.Wait()
}
