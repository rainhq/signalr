package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/rainhq/signalr/v2/bittrex"
)

type ClosedOrdersCommand struct {
	start time.Time
}

func (c *ClosedOrdersCommand) Parse(args []string) {
	fs := flag.NewFlagSet("bittrex_v3 closedorders", flag.ExitOnError)
	start := fs.String("start", "", "start date in RFC3339 format")
	_ = fs.Parse(args)

	switch {
	case *start == "":
		now := time.Now()
		c.start = time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	default:
		ts, err := time.Parse(time.RFC3339, *start)
		if err != nil {
			fmt.Fprintln(os.Stderr, "invalid value for start")
			fs.Usage()
			os.Exit(1)
		}

		c.start = ts
	}
}

func (c *ClosedOrdersCommand) Run(ctx context.Context, client *bittrex.Client) error {
	orders, err := client.GetClosedOrders(ctx, c.start)
	if err != nil {
		return err
	}

	t, err := NewTerminal()
	if err != nil {
		return err
	}
	defer t.Close()

	printClosedOrders(t, orders)
	return nil
}

func printClosedOrders(t *Terminal, orders *bittrex.Orders) {
	t.PrintEscape(t.Escape.Bold, fmt.Sprintf("| %s | %s | %s | %s | %s |\n", center("id", 36), center("symbol", 8), center("closed", 20), center("limit", 15), center("quantity", 16)))

	for _, order := range orders.Data {
		closedAt := order.ClosedAt.Format(time.RFC3339)
		limit := order.Limit.Decimal.StringFixed(4)
		quantity := order.FillQuantity.StringFixed(8)

		fmt.Fprintf(t, "| %36s | %8s | %20s | %15s | %16s |\n", order.ID, order.MarketSymbol, closedAt, limit, quantity)
	}
}
