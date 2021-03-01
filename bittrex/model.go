package bittrex

import (
	"fmt"
	"sort"
	"strconv"
	"time"

	"github.com/shopspring/decimal"
)

type OrderBook struct {
	MarketSymbol string
	Depth        int
	Sequence     int
	Bids         OrderBookEntries
	Asks         OrderBookEntries
}

func (b *OrderBook) Apply(delta *OrderBook) *OrderBookDelta {
	bids, bidsDiff := b.Bids.Apply(delta.Bids, decimal.Decimal.LessThanOrEqual)
	asks, asksDiff := b.Asks.Apply(delta.Asks, decimal.Decimal.GreaterThanOrEqual)

	b.Sequence, b.Bids, b.Asks = delta.Sequence, bids, asks

	return &OrderBookDelta{
		MarketSymbol: b.MarketSymbol,
		Sequence:     b.Sequence,
		Bids:         bidsDiff,
		Asks:         asksDiff,
	}
}

type OrderBookEntries []OrderBookEntry

func (e OrderBookEntries) String() string {
	return fmt.Sprintf("%+v", []OrderBookEntry(e))
}

func (e OrderBookEntries) OrderBookDeltaEntries(action Action) OrderBookDeltaEntries {
	res := make(OrderBookDeltaEntries, len(e))
	for i, v := range e {
		res[i] = v.OrderBookDiffEntry(action)
	}

	return res
}

func (e OrderBookEntries) SearchRate(rate decimal.Decimal, f func(l, r decimal.Decimal) bool) int {
	return sort.Search(len(e), func(i int) bool {
		return f(e[i].Rate, rate)
	})
}

func (e OrderBookEntries) Apply(deltas OrderBookEntries, cmp func(l, r decimal.Decimal) bool) (OrderBookEntries, OrderBookDeltaEntries) {
	res := make(OrderBookEntries, 0, len(e))
	diffs := make(OrderBookDeltaEntries, 0, len(deltas))
	for _, delta := range deltas {
		n := len(e)
		j := e.SearchRate(delta.Rate, cmp)

		res = append(res, e[:j]...)

		if j == n {
			e = e[j:]
			break
		}

		var action Action
		switch {
		case delta.Quantity.IsZero():
			action = DeleteAction
			e = e[j+1:]
		case delta.Rate.Equal(e[j].Rate):
			action = UpdateAction
			res = append(res, delta)
			e = e[j+1:]
		default:
			action = AddAction
			res = append(res, delta)
			e = e[j:]
		}

		diffs = append(diffs, delta.OrderBookDiffEntry(action))
	}

	res = append(res, e...)

	// add remaining deltas
	deltas = deltas[len(diffs):]
	res = append(res, deltas...)
	diffs = append(diffs, deltas.OrderBookDeltaEntries(AddAction)...)

	return res, diffs
}

type OrderBookEntry struct {
	Rate     decimal.Decimal
	Quantity decimal.Decimal
}

func (e OrderBookEntry) OrderBookDiffEntry(action Action) OrderBookDeltaEntry {
	return OrderBookDeltaEntry{
		Action:   action,
		Rate:     e.Rate,
		Quantity: e.Quantity,
	}
}

type OrderBookDelta struct {
	MarketSymbol string
	Depth        int
	Sequence     int
	Bids         OrderBookDeltaEntries
	Asks         OrderBookDeltaEntries
}

type OrderBookDeltaEntries []OrderBookDeltaEntry

func (e OrderBookDeltaEntries) SearchRate(rate decimal.Decimal, cmp func(l, r decimal.Decimal) bool) int {
	return sort.Search(len(e), func(i int) bool {
		return cmp(e[i].Rate, rate)
	})
}

func (e OrderBookDeltaEntries) String() string {
	return fmt.Sprintf("%+v", []OrderBookDeltaEntry(e))
}

type OrderBookDeltaEntry struct {
	Action   Action
	Rate     decimal.Decimal
	Quantity decimal.Decimal
}

type Action string

const (
	AddAction    = Action("add")
	UpdateAction = Action("update")
	DeleteAction = Action("delete")
)

type Orders struct {
	Sequence int
	Data     []*Order
}

func (o *Orders) Apply(sequence int, order *Order) (*OrderDelta, error) {
	n := len(o.Data)
	i := o.SearchOrder(order)
	switch {
	case i == n && order.Status == OrderStatusClosed:
		return nil, &OrderNotFoundError{ID: order.ID}
	case i == n:
		o.Data = append(o.Data, order)
		return &OrderDelta{Sequence: sequence, Action: AddAction, Order: order}, nil
	case order.Status == OrderStatusClosed:
		o.Data = append(o.Data[i:], o.Data[i+1:]...)
		return &OrderDelta{Sequence: sequence, Action: DeleteAction, Order: order}, nil
	default:
		o.Data[i] = order
		return &OrderDelta{Sequence: sequence, Action: UpdateAction, Order: order}, nil
	}
}

func (o *Orders) SearchOrder(order *Order) int {
	n := len(o.Data)
	i := sort.Search(n, func(i int) bool {
		return o.Data[i].CreatedAt.Before(order.CreatedAt)
	})

	if i == n {
		return n
	}

	for j, v := range o.Data[i:] {
		switch {
		case v.ID == order.ID:
			return i + j
		case v.CreatedAt.After(order.CreatedAt):
			break
		}
	}

	return n
}

type OrderDelta struct {
	Sequence int
	Action   Action
	Order    *Order
}

type Order struct {
	ID            string              `json:"id"`
	ClientOrderID string              `json:"clientOrderId"`
	MarketSymbol  string              `json:"marketSymbol"`
	Side          string              `json:"direction"`
	Type          string              `json:"type"`
	Quantity      decimal.NullDecimal `json:"quantity"`
	Ceiling       decimal.NullDecimal `json:"ceiling"`
	Limit         decimal.NullDecimal `json:"limit"`
	Option        string              `json:"timeInForce"`
	FillQuantity  decimal.Decimal     `json:"fillQuantity"`
	Commission    decimal.Decimal     `json:"commission"`
	Proceeds      decimal.Decimal     `json:"proceeds"`
	Status        OrderStatus         `json:"status"`
	CreatedAt     time.Time           `json:"createdAt"`
	UpdatedAt     time.Time           `json:"updatedAt"`
	ClosedAt      time.Time           `json:"closedAt"`
}

type OrderStatus string

const (
	OrderStatusOpen   = OrderStatus("OPEN")
	OrderStatusClosed = OrderStatus("CLOSED")
)

func (s *OrderStatus) UnmarshalJSON(data []byte) error {
	v, err := strconv.Unquote(string(data))
	if err != nil {
		return fmt.Errorf("invalid value")
	}

	if v != string(OrderStatusOpen) && v != string(OrderStatusClosed) {
		return fmt.Errorf("invalid OrderStatus: %q", v)
	}

	*s = OrderStatus(v)
	return nil
}

type Balance struct {
	CurrencySymbol string          `json:"currencySymbol"`
	Total          decimal.Decimal `json:"decimal"`
	Available      decimal.Decimal `json:"available"`
}
