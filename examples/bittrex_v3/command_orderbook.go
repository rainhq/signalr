package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/rainhq/signalr/v2/bittrex"
	"golang.org/x/sync/errgroup"
)

type OrderBookCommand struct {
	marketSymbol string
	depth        int
}

func (c *OrderBookCommand) Parse(args []string) {
	fs := flag.NewFlagSet("bittrex_v3 orderbook", flag.ExitOnError)
	marketSymbol := fs.String("marketSymbol", "BTC-USD", "market symbol")
	depth := fs.Int("depth", 25, "order book depth (1, 25 or 500)")
	_ = fs.Parse(args)

	if *depth != 1 && *depth != 25 && *depth != 500 {
		fmt.Fprintln(os.Stderr, "invalid value for depth")
		fs.Usage()
		os.Exit(1)
	}

	c.marketSymbol = *marketSymbol
	c.depth = *depth
}

func (c *OrderBookCommand) Run(ctx context.Context, client *bittrex.Client) error {
	g, ctx := errgroup.WithContext(ctx)
	g.Go(func() error {
		return client.SubscribeOrderBook(ctx, c.marketSymbol, c.depth, func(orderBook *bittrex.OrderBook, delta *bittrex.OrderBookDelta) error {
			// printOrderBook(t, orderBook, delta)
			return nil
		})
	})
	g.Go(func() error { return client.Run(ctx) })

	return g.Wait()
}

// func printOrderBook(t *Terminal, orderBook *bittrex.OrderBook, delta *bittrex.OrderBookDelta) {
// 	color := func(entry bittrex.OrderBookEntry, deltas bittrex.OrderBookDeltaEntries) []byte {
// 		i := deltas.SearchRate(entry.Rate, decimal.Decimal.Equal)
// 		if i == len(deltas) {
// 			return nil
// 		}

// 		return actionToColor(t, deltas[i].Action)
// 	}

// 	t.Clear()
// 	t.PrintEscape(t.Escape.Bold, fmt.Sprintf("| %s | %s |\n", center("bids", 30), center("asks", 30)))
// 	t.PrintEscape(t.Escape.Bold, fmt.Sprintf("| %s | %s | %s | %s |\n", center("rate", 15), center("quantity", 12), center("rate", 15), center("quantity", 12)))

// 	for i := 0; i < orderBook.Depth; i++ {
// 		bid, ask := orderBook.Bids[i], orderBook.Asks[i]
// 		t.PrintEscape(color(bid, delta.Bids), fmt.Sprintf("| %15s | %12s ", bid.Rate.StringFixed(8), bid.Quantity.StringFixed(4)))
// 		t.PrintEscape(color(ask, delta.Asks), fmt.Sprintf("| %15s | %12s |\n", ask.Rate.StringFixed(8), bid.Quantity.StringFixed(4)))
// 	}
// }
