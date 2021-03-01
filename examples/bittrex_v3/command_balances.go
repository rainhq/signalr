package main

import (
	"context"
	"fmt"
	"os"

	"github.com/juju/ansiterm"
	"github.com/rainhq/signalr/v2/bittrex"
)

type BalancesCommand struct{}

func (c *BalancesCommand) Parse(args []string) {}

func (c *BalancesCommand) Run(ctx context.Context, client *bittrex.Client) error {
	balances, err := client.GetBalances(ctx)
	if err != nil {
		return err
	}

	w := ansiterm.NewTabWriter(os.Stdout, 8, 4, 1, ' ', 0)
	w.SetStyle(ansiterm.Bold)
	fmt.Fprintf(w, "currency\ttotal\tavailable\t\n")
	w.Reset()

	for _, balance := range balances {
		fmt.Fprintf(w, "%s\t%s\t%s\t\n", balance.CurrencySymbol, balance.Total, balance.Available)
	}

	w.Flush()

	return nil
}
