package main

import (
	"bytes"
	"compress/flate"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"net/http"
	"strconv"

	"github.com/rainhq/signalr/v2"
)

var ErrInvalidSequence = errors.New("invalid sequence")

type Client struct {
	httpClient    *http.Client
	signalrClient *signalr.Client
}

func NewClient(httpClient *http.Client, signalrClient *signalr.Client) *Client {
	return &Client{
		httpClient:    httpClient,
		signalrClient: signalrClient,
	}
}

func (c *Client) GetOrderBook(ctx context.Context, marketSymbol string, depth int) (*OrderBook, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("https://api.bittrex.com/v3/markets/%s/orderbook?depth=%d", marketSymbol, depth), nil)
	if err != nil {
		return nil, fmt.Errorf("failed to prepare orderbook request: %w", err)
	}

	httpRes, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to get orderbook: %w", err)
	}
	defer httpRes.Body.Close()

	data, err := ioutil.ReadAll(httpRes.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read orderbook: %w", err)
	}

	if httpRes.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to get orderbook (%s): %s", httpRes.Status, string(data))
	}

	sequence, err := strconv.ParseInt(httpRes.Header.Get("Sequence"), 10, 64)
	if err != nil {
		return nil, fmt.Errorf("invalid sequence number: %w", err)
	}

	var res orderBookResponse
	if err := json.Unmarshal(data, &res); err != nil {
		return nil, fmt.Errorf("failed to parse orderbook: %w", err)
	}

	return &OrderBook{
		MarketSymbol: marketSymbol,
		Depth:        depth,
		Sequence:     sequence,
		Bids:         res.Bids,
		Asks:         res.Asks,
	}, nil
}

func (c *Client) SubscribeOrderBook(ctx context.Context, marketSymbol string, depth int, callback func(orderBook *OrderBook, diff *OrderBookDiff) error) error {
	streamName := fmt.Sprintf("orderbook_%s_%d", marketSymbol, depth)
	if err := subscribe(ctx, c.signalrClient, []string{streamName}); err != nil {
		return fmt.Errorf("failed to subscribe to orderbook %q: %w", marketSymbol, err)
	}

	orderBook, err := c.GetOrderBook(ctx, marketSymbol, depth)
	if err != nil {
		return fmt.Errorf("failed to get orderbook %q: %w", marketSymbol, err)
	}

	if err := callback(orderBook, &OrderBookDiff{MarketSymbol: marketSymbol, Depth: depth}); err != nil {
		return err
	}

	stream := c.signalrClient.Callback(ctx, "orderBook")
	defer stream.Close()

	for {
		var deltaRes orderBookDeltaResponse
		if err := readOrderBookDelta(stream, &deltaRes); err != nil {
			return fmt.Errorf("failed to read orderbook delta %q: %w", marketSymbol, err)
		}

		if deltaRes.MarketSymbol != marketSymbol || deltaRes.Depth != depth {
			continue
		}

		seqDiff := deltaRes.Sequence - orderBook.Sequence
		switch {
		case seqDiff < 1:
			continue
		case seqDiff > 1:
			return ErrInvalidSequence
		}

		delta := OrderBook(deltaRes)
		diff := orderBook.Apply(&delta)

		if err := callback(orderBook, diff); err != nil {
			return err
		}
	}
}

// https://bittrex.github.io/api/v3#operation--markets--marketSymbol--orderbook-get
type orderBookResponse struct {
	Bids OrderBookEntries `json:"bid"`
	Asks OrderBookEntries `json:"ask"`
}

// https://bittrex.github.io/api/v3#operation--markets--marketSymbol--orderbook-get
type orderBookDeltaResponse struct {
	MarketSymbol string           `json:"marketSymbol"`
	Depth        int              `json:"depth"`
	Sequence     int64            `json:"sequence"`
	Bids         OrderBookEntries `json:"bidDeltas"`
	Asks         OrderBookEntries `json:"askDeltas"`
}

func readOrderBookDelta(stream *signalr.CallbackStream, dest *orderBookDeltaResponse) error {
	var data []byte
	if err := stream.Read(&data); err != nil {
		return err
	}

	reader := flate.NewReader(bytes.NewReader(data))
	decompressed, err := ioutil.ReadAll(reader)
	if err != nil {
		return err
	}

	return json.Unmarshal(decompressed, &dest)
}
