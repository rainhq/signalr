package main

import (
	"context"
	"fmt"
	"time"

	"github.com/rainhq/signalr/v2/bittrex"
	"golang.org/x/sync/errgroup"
)

type OrdersCommand struct{}

func (c *OrdersCommand) Parse(args []string) {}

func (c *OrdersCommand) Run(ctx context.Context, client *bittrex.Client) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	t, err := NewTerminal(HideCursor())
	if err != nil {
		return err
	}
	defer t.Close()

	now := time.Now()
	start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)

	g, ctx := errgroup.WithContext(ctx)
	g.Go(func() error {
		return client.SubscribeOpenOrders(ctx, start, func(orders *bittrex.Orders, delta *bittrex.OrderDelta) error {
			printOrders(t, orders, delta)
			return nil
		})
	})
	g.Go(func() error {
		err := t.Wait()
		cancel()

		return err
	})

	return g.Wait()
}

func printOrders(t *Terminal, orders *bittrex.Orders, delta *bittrex.OrderDelta) {
	t.Clear()
	t.PrintEscape(t.Escape.Bold, fmt.Sprintf("| id | %s | %s | %s |\n", center("created", 20), center("limit", 15), center("quantity", 12)))

	for _, order := range orders.Data {
		var color []byte
		if order.ID == delta.Order.ID {
			color = actionToColor(t, delta.Action)
		}

		createdAt := order.CreatedAt.Format(time.RFC3339)
		limit := order.Limit.Decimal.StringFixed(4)
		quantity := order.FillQuantity.StringFixed(8)

		t.PrintEscape(color, fmt.Sprintf("| %s | %20s | %15s | %12s |\n", order.ID, createdAt, limit, quantity))
	}
}
