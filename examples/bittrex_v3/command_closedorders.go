package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/juju/ansiterm"

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

	w := ansiterm.NewTabWriter(os.Stdout, 8, 4, 1, ' ', 0)
	w.SetStyle(ansiterm.Bold)
	fmt.Fprintf(w, "id\tsymbol\tclosed\tlimit\tquantity\t\n")
	w.Reset()

	for _, order := range orders.Data {
		closedAt := order.ClosedAt.Format(time.RFC3339)
		limit := order.Limit.Decimal.StringFixed(4)
		quantity := order.FillQuantity.StringFixed(8)

		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t\n", order.ID, order.MarketSymbol, closedAt, limit, quantity)
	}

	w.Flush()

	return nil
}
