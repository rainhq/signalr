package main

import (
	"bytes"
	"compress/flate"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"time"

	"github.com/rainhq/signalr/v2"
	"golang.org/x/sync/errgroup"
)

func main() {
	config, err := parseConfig()
	if err != nil {
		log.Fatal(err)
	}

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
		//nolint:gocritic
		log.Fatal(err)
	}

	client := signalr.NewClient("c3", conn)

	errg, ctx := errgroup.WithContext(ctx)
	errg.Go(func() error { return client.Run(ctx) })
	errg.Go(func() error {
		if err := authenticate(ctx, client, config.APIKey, config.APISecret); err != nil {
			return err
		}

		streams := make([]string, len(config.Symbols))
		for i, symbol := range config.Symbols {
			streams[i] = fmt.Sprintf("orderbook_%s_%d", symbol, config.Depth)
		}

		if err := subscribe(ctx, client, streams); err != nil {
			return err
		}

		callback := client.Callback(ctx, "orderBook")
		defer callback.Close()

		log.Print("Subscribed to exchange deltas")

		for {
			var data []byte
			if err := callback.Read(&data); err != nil {
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

	err = errg.Wait()
	switch {
	case errors.Is(err, context.Canceled):
		os.Exit(130)
	case err != nil:
		log.Fatal(err)
	}
}

type SocketResponse struct {
	Success   bool   `json:"success"`
	ErrorCode string `json:"error_code"`
}

type SubscriptionError struct {
	Streams []string
}

func (e *SubscriptionError) Error() string {
	return fmt.Sprintf("subscription failed: %s", strings.Join(e.Streams, ", "))
}

type config struct {
	APIKey    string
	APISecret string
	Symbols   []string
	Depth     int
}

func parseConfig() (*config, error) {
	apiKey, ok := os.LookupEnv("BITTREX_API_KEY")
	if !ok {
		return nil, errors.New("BITTREX_API_KEY is required")
	}

	apiSecret, ok := os.LookupEnv("BITTREX_API_SECRET")
	if !ok {
		return nil, errors.New("BITTREX_API_SECRET is required")
	}

	fs := flag.NewFlagSet("bittrex_v3", flag.ContinueOnError)
	symbols := fs.String("symbols", "BTC-USD,", "comma separated list order book market symbols")
	depth := fs.Int("depth", 25, "order book depth (1, 25 or 500)")
	if err := fs.Parse(os.Args[1:]); err != nil {
		return nil, err
	}

	if *depth != 1 && *depth != 25 && *depth != 500 {
		return nil, errors.New("invalid value for depth")
	}

	return &config{
		APIKey:    apiKey,
		APISecret: apiSecret,
		Symbols:   strings.Split(*symbols, ","),
		Depth:     *depth,
	}, nil
}

func authenticate(ctx context.Context, client *signalr.Client, apiKey, apiSecret string) error {
	ts := strconv.FormatInt(time.Now().UnixNano()/int64(time.Millisecond), 10)
	randomContent := randomString(32)
	signedContent := signature(apiSecret, ts+randomContent)

	ictx, icancel := context.WithTimeout(ctx, 5*time.Second)
	defer icancel()

	return client.Invoke(ictx, "Authenticate", apiKey, ts, randomContent, signedContent).Exec()
}

func subscribe(ctx context.Context, client *signalr.Client, streams []string) error {
	ictx, icancel := context.WithTimeout(ctx, 5*time.Second)
	defer icancel()

	var res []SocketResponse
	if err := client.Invoke(ictx, "Subscribe", streams).Unmarshal(&res); err != nil {
		return err
	}

	serr := &SubscriptionError{}
	for i, v := range res {
		if v.Success {
			continue
		}

		serr.Streams = append(serr.Streams, streams[i])
	}

	if len(serr.Streams) != 0 {
		return serr
	}

	return nil
}

func randomString(size int) string {
	b := make([]byte, base64.URLEncoding.DecodedLen(size))
	_, _ = rand.Read(b)

	return base64.URLEncoding.EncodeToString(b)
}

func signature(apiSecret, data string) string {
	hash := hmac.New(sha512.New, []byte(apiSecret))
	_, _ = hash.Write([]byte(data))
	return hex.EncodeToString(hash.Sum(nil))
}
