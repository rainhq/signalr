package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/juju/ansiterm"
	"github.com/rainhq/signalr/v2/bittrex"
	"golang.org/x/sync/errgroup"
)

type OpenOrdersCommand struct{}

func (c *OpenOrdersCommand) Parse(args []string) {}

func (c *OpenOrdersCommand) Run(ctx context.Context, client *bittrex.Client) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	now := time.Now()
	start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)

	w := ansiterm.NewTabWriter(os.Stdout, 8, 4, 1, ' ', 0)

	g, ctx := errgroup.WithContext(ctx)
	g.Go(func() error {
		return client.SubscribeOpenOrders(ctx, start, func(orders *bittrex.Orders, delta *bittrex.OrderDelta) error {
			printOpenOrders(w, orders, delta)
			return nil
		})
	})
	g.Go(func() error { return client.Run(ctx) })

	return g.Wait()
}

func printOpenOrders(w *ansiterm.TabWriter, orders *bittrex.Orders, delta *bittrex.OrderDelta) {
	w.SetStyle(ansiterm.Bold)
	fmt.Fprintf(w, "id\tcreated\tlimit\tquantity\t\n")
	w.Reset()

	for _, order := range orders.Data {
		createdAt := order.CreatedAt.Format(time.RFC3339)
		limit := order.Limit.Decimal.StringFixed(4)
		quantity := order.FillQuantity.StringFixed(8)

		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t\n", order.ID, createdAt, limit, quantity)
	}

	w.Flush()
}
