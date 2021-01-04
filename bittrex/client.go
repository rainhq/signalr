package bittrex

import (
	"bytes"
	"compress/flate"
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"strconv"
	"time"

	"github.com/rainhq/signalr/v2"
	"golang.org/x/sync/errgroup"
)

const (
	Endpoint = "https://api.bittrex.com/v3"
)

type Client struct {
	httpClient    *http.Client
	signalrClient *signalr.Client
	authenticator *authenticator
}

func NewClient(httpClient *http.Client, signalrClient *signalr.Client, apiKey, apiSecret string) *Client {
	return &Client{
		httpClient:    httpClient,
		signalrClient: signalrClient,
		authenticator: newAuthenticator(signalrClient, apiKey, apiSecret),
	}
}

func (c *Client) Run(ctx context.Context) error {
	g, ctx := errgroup.WithContext(ctx)
	g.Go(func() error { return c.signalrClient.Run(ctx) })
	g.Go(func() error { return c.authenticator.Run(ctx) })

	return g.Wait()
}

func (c *Client) GetOrderBook(ctx context.Context, marketSymbol string, depth int) (*OrderBook, error) {
	endpoint := fmt.Sprintf("%s/markets/%s/orderbook?depth=%d", Endpoint, marketSymbol, depth)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to prepare orderbook request: %w", err)
	}

	var res struct {
		Bids OrderBookEntries `json:"bid"`
		Asks OrderBookEntries `json:"ask"`
	}
	sequence, err := doRequest(c.httpClient, req, &res)
	if err != nil {
		return nil, err
	}

	return &OrderBook{
		MarketSymbol: marketSymbol,
		Depth:        depth,
		Sequence:     sequence,
		Bids:         res.Bids,
		Asks:         res.Asks,
	}, nil
}

func (c *Client) SubscribeOrderBook(ctx context.Context, marketSymbol string, depth int, callback func(orderBook *OrderBook, delta *OrderBookDelta) error) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	streamName := fmt.Sprintf("orderbook_%s_%d", marketSymbol, depth)
	if err := subscribe(ctx, c.signalrClient, []string{streamName}); err != nil {
		return err
	}

	orderBook, err := c.GetOrderBook(ctx, marketSymbol, depth)
	if err != nil {
		return fmt.Errorf("failed to get orderbook %q: %w", marketSymbol, err)
	}

	offset := orderBook.Sequence
	orderBook.Sequence = 0

	if err := callback(orderBook, &OrderBookDelta{MarketSymbol: marketSymbol, Depth: depth}); err != nil {
		return err
	}

	stream := c.signalrClient.Callback(ctx, "orderBook")
	defer stream.Close()

	for {
		var data []byte
		if err := stream.Read(&data); err != nil {
			return err
		}

		reader := flate.NewReader(bytes.NewReader(data))
		decompressed, err := ioutil.ReadAll(reader)
		if err != nil {
			return err
		}

		var res struct {
			MarketSymbol string           `json:"marketSymbol"`
			Depth        int              `json:"depth"`
			Sequence     int              `json:"sequence"`
			Bids         OrderBookEntries `json:"bidDeltas"`
			Asks         OrderBookEntries `json:"askDeltas"`
		}
		if err := json.Unmarshal(decompressed, &res); err != nil {
			return err
		}

		update := OrderBook(res)
		update.Sequence -= offset

		if update.MarketSymbol != marketSymbol || update.Depth != depth {
			continue
		}

		diff := update.Sequence - orderBook.Sequence
		switch {
		case diff < 1:
			continue
		case diff > 1:
			return &SequenceError{Expected: orderBook.Sequence + 1, Actual: update.Sequence}
		}

		delta := orderBook.Apply(&update)

		if err := callback(orderBook, delta); err != nil {
			return err
		}
	}
}

func (c *Client) GetClosedOrders(ctx context.Context, start time.Time) (*Orders, error) {
	startDate := start.UTC().Format(time.RFC3339)
	endpoint := fmt.Sprintf("%s/orders/closed?startDate=%s", Endpoint, startDate)
	req, err := c.authenticator.Request(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}

	var res []*Order
	if _, err := doRequest(c.httpClient, req, &res); err != nil {
		return nil, err
	}

	if len(res) == 0 {
		return &Orders{}, nil
	}

	for {
		last := res[len(res)-1]
		endpoint := fmt.Sprintf("%s/orders/closed?startDate=%s&nextPageToken=%s", Endpoint, startDate, last.ID)

		req, err := c.authenticator.Request(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			return nil, err
		}

		var orders []*Order
		_, err = doRequest(c.httpClient, req, &orders)
		if err != nil {
			return nil, err
		}

		res = append(res, orders...)

		if len(orders) == 0 {
			break
		}
	}

	return &Orders{Data: res}, nil
}

func (c *Client) GetOpenOrders(ctx context.Context) (*Orders, error) {
	endpoint := fmt.Sprintf("%s/orders/open", Endpoint)
	req, err := c.authenticator.Request(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}

	var res []*Order
	sequence, err := doRequest(c.httpClient, req, &res)
	if err != nil {
		return nil, err
	}

	return &Orders{
		Sequence: sequence,
		Data:     res,
	}, nil
}

func (c *Client) SubscribeOpenOrders(ctx context.Context, start time.Time, callback func(*Orders, *OrderDelta) error) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	if err := c.authenticator.authenticate(ctx); err != nil {
		return err
	}

	if err := subscribe(ctx, c.signalrClient, []string{"order"}); err != nil {
		return err
	}

	orders, err := c.GetOpenOrders(ctx)
	if err != nil {
		return fmt.Errorf("failed to get open orders: %w", err)
	}

	offset := orders.Sequence
	orders.Sequence = 0

	if err := callback(orders, nil); err != nil {
		return err
	}

	stream := c.signalrClient.Callback(ctx, "order")
	for {
		var res struct {
			Sequence int   `json:"sequence"`
			Delta    Order `json:"delta"`
		}
		if err := readCallback(stream, &res); err != nil {
			return err
		}

		res.Sequence -= offset

		diff := res.Sequence - orders.Sequence
		switch {
		case diff < 1:
			continue
		case diff > 1:
			return &SequenceError{Expected: orders.Sequence + 1, Actual: res.Sequence}
		}

		delta, err := orders.Apply(res.Sequence, &res.Delta)
		if err != nil {
			return err
		}

		if err := callback(orders, delta); err != nil {
			return err
		}
	}
}

func subscribe(ctx context.Context, client *signalr.Client, streams []string) error {
	ictx, icancel := context.WithTimeout(ctx, 5*time.Second)
	defer icancel()

	var res []struct {
		Success   bool   `json:"success"`
		ErrorCode string `json:"error_code"`
	}
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

func doRequest(httpClient *http.Client, req *http.Request, dest interface{}) (int, error) {
	req.Header.Add("Accept", "application/json")

	httpRes, err := httpClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("failed to %s %s: %w", req.Method, req.URL.Path, err)
	}
	defer httpRes.Body.Close()

	data, err := ioutil.ReadAll(httpRes.Body)
	if err != nil {
		return 0, fmt.Errorf("failed to read response to %s %s: %w", req.Method, req.URL.Path, err)
	}

	if httpRes.StatusCode != 200 {
		return 0, fmt.Errorf("failed to %s %s: %d %s", req.Method, req.URL.Path, httpRes.StatusCode, string(data))
	}

	if err := json.Unmarshal(data, dest); err != nil {
		return 0, fmt.Errorf("failed to parse response to %s %s: %w", req.Method, req.URL.Path, err)
	}

	rawSequence := httpRes.Header.Get("Sequence")
	if rawSequence == "" {
		return 0, nil
	}

	sequence, err := strconv.ParseInt(rawSequence, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid sequence number: %w", err)
	}

	return int(sequence), nil
}

func readCallback(stream *signalr.CallbackStream, dest interface{}) error {
	var data []byte
	if err := stream.Read(&data); err != nil {
		return err
	}

	reader := flate.NewReader(bytes.NewReader(data))
	decompressed, err := ioutil.ReadAll(reader)
	if err != nil {
		return err
	}

	return json.Unmarshal(decompressed, dest)
}
