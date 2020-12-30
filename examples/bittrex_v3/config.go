package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/rainhq/signalr/v2/bittrex"
)

type Config struct {
	APIKey    string
	APISecret string
	Command   Command
}

type Command interface {
	Parse(args []string)
	Run(ctx context.Context, client *bittrex.Client) error
}

func parseArgs(args []string) *Config {
	fs := flag.NewFlagSet("bittrex_v3", flag.ExitOnError)
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), "usage: bittrex_v3 COMMAND")
		fmt.Fprintln(fs.Output(), "commands:")
		fmt.Fprintln(fs.Output(), " - orders\tWatch orders")
		fmt.Fprintln(fs.Output(), " - orderbook\tWatch orderbook")
		fs.PrintDefaults()
	}
	_ = fs.Parse(args)

	apiKey, ok := os.LookupEnv("BITTREX_API_KEY")
	if !ok {
		fmt.Fprint(os.Stderr, "BITTREX_API_KEY is required")
		os.Exit(1)
	}

	apiSecret, ok := os.LookupEnv("BITTREX_API_SECRET")
	if !ok {
		fmt.Fprint(os.Stderr, "BITTREX_API_SECRET is required")
		os.Exit(1)
	}

	var cmd Command
	switch fs.Arg(0) {
	case "orderbook":
		cmd = &OrderBookCommand{}
	case "orders":
		cmd = &OrdersCommand{}
	default:
		fmt.Fprintf(os.Stderr, "invalid command %q\n", fs.Arg(0))
		fs.Usage()
		os.Exit(1)
	}

	cmd.Parse(fs.Args()[1:])

	return &Config{
		APIKey:    apiKey,
		APISecret: apiSecret,
		Command:   cmd,
	}
}
