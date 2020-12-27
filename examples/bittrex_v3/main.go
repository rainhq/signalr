package main

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/rainhq/signalr/v2"
	"github.com/shopspring/decimal"
	"golang.org/x/sync/errgroup"
	"golang.org/x/term"
)

func main() {
	config, err := parseConfig()
	if err != nil {
		log.Fatal(err)
	}

	err = run(config)
	switch {
	case errors.Is(err, context.Canceled):
		os.Exit(130)
	case err != nil:
		log.Fatal(err)
	}
}

func run(config *config) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	state, err := term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		return err
	}

	t := term.NewTerminal(os.Stdin, "")
	fmt.Fprintf(t, "\033[?25l")

	defer func() {
		fmt.Fprintf(t, "\033[?25h")
		if err := term.Restore(int(os.Stdin.Fd()), state); err != nil {
			log.Print(err)
		}
	}()

	// handle Ctrl-C gracefully
	go func() {
		for {
			_, err := t.ReadLine()
			switch {
			case errors.Is(err, io.EOF):
				cancel()
				return
			case err != nil:
				log.Print(err)
			}
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

	errg, ctx := errgroup.WithContext(ctx)
	errg.Go(func() error { return signalrClient.Run(ctx) })
	errg.Go(func() error {
		client := NewClient(http.DefaultClient, signalrClient)
		return client.SubscribeOrderBook(ctx, config.Symbol, config.Depth, func(orderBook *OrderBook, diff *OrderBookDiff) error {
			printOrderBook(t, orderBook, diff)
			return nil
		})
	})

	return errg.Wait()
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

func printOrderBook(t *term.Terminal, orderBook *OrderBook, diff *OrderBookDiff) {
	printEscape := func(t *term.Terminal, esc []byte, s string) {
		fmt.Fprint(t, string(esc), s, string(t.Escape.Reset))
	}

	center := func(s string, l int) string {
		padding := l - len([]rune(s))
		lpad := padding / 2

		b := strings.Builder{}

		for i := 0; i < lpad; i++ {
			_, _ = b.WriteRune(' ')
		}

		_, _ = b.WriteString(s)

		for i := 0; i < padding-lpad; i++ {
			_, _ = b.WriteRune(' ')
		}

		return b.String()
	}

	color := func(entry OrderBookEntry, diffs OrderBookDiffEntries) []byte {
		i := diffs.SearchRate(entry.Rate, decimal.Decimal.Equal)
		switch {
		case i == len(diffs):
			return nil
		case diffs[i].Action == DeleteAction:
			return t.Escape.Red
		case diffs[i].Action == AddAction:
			return t.Escape.Green
		case diffs[i].Action == UpdateAction:
			return t.Escape.Blue
		default:
			return nil
		}
	}

	bold := []byte("\033[1m")
	printEscape(t, bold, fmt.Sprintf("| %s | %s |\n", center("bids", 30), center("asks", 30)))
	printEscape(t, bold, fmt.Sprintf("| %s | %s | %s | %s |\n", center("rate", 15), center("quantity", 12), center("rate", 15), center("quantity", 12)))

	for i := 0; i < orderBook.Depth; i++ {
		bid, ask := orderBook.Bids[i], orderBook.Asks[i]
		printEscape(t, color(bid, diff.Bids), fmt.Sprintf("| %15s | %12s ", bid.Rate.StringFixed(8), bid.Quantity.StringFixed(4)))
		printEscape(t, color(ask, diff.Asks), fmt.Sprintf("| %15s | %12s |\n", ask.Rate.StringFixed(8), bid.Quantity.StringFixed(4)))
	}
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
