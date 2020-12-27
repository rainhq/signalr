package main

import (
	"errors"
	"flag"
	"os"
)

type config struct {
	APIKey    string
	APISecret string
	Symbol    string
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
	symbol := fs.String("symbol", "BTC-USD", "market symbol")
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
		Symbol:    *symbol,
		Depth:     *depth,
	}, nil
}
