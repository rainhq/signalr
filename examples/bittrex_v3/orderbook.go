package main

import (
	"fmt"
	"sort"

	"github.com/shopspring/decimal"
)

type OrderBook struct {
	MarketSymbol string
	Depth        int
	Sequence     int64
	Bids         OrderBookEntries
	Asks         OrderBookEntries
}

func (b *OrderBook) Apply(delta *OrderBook) *OrderBookDiff {
	bids, bidsDiff := b.Bids.Apply(delta.Bids, decimal.Decimal.LessThanOrEqual)
	asks, asksDiff := b.Asks.Apply(delta.Asks, decimal.Decimal.GreaterThanOrEqual)

	b.Sequence, b.Bids, b.Asks = delta.Sequence, bids, asks

	return &OrderBookDiff{
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

func (e OrderBookEntries) OrderBookDiffEntries(action Action) OrderBookDiffEntries {
	res := make(OrderBookDiffEntries, len(e))
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

func (e OrderBookEntries) Apply(deltas OrderBookEntries, cmp func(l, r decimal.Decimal) bool) (OrderBookEntries, OrderBookDiffEntries) {
	res := make(OrderBookEntries, 0, len(e))
	diffs := make(OrderBookDiffEntries, 0, len(deltas))
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
	diffs = append(diffs, deltas.OrderBookDiffEntries(AddAction)...)

	return res, diffs
}

type OrderBookEntry struct {
	Rate     decimal.Decimal `json:"rate"`
	Quantity decimal.Decimal `json:"quantity"`
}

func (e OrderBookEntry) OrderBookDiffEntry(action Action) OrderBookDiffEntry {
	return OrderBookDiffEntry{
		Action:   action,
		Rate:     e.Rate,
		Quantity: e.Quantity,
	}
}

type OrderBookDiff struct {
	MarketSymbol string
	Depth        int
	Sequence     int64
	Bids         OrderBookDiffEntries
	Asks         OrderBookDiffEntries
}

type OrderBookDiffEntries []OrderBookDiffEntry

func (e OrderBookDiffEntries) SearchRate(rate decimal.Decimal, cmp func(l, r decimal.Decimal) bool) int {
	return sort.Search(len(e), func(i int) bool {
		return cmp(e[i].Rate, rate)
	})
}

func (e OrderBookDiffEntries) String() string {
	return fmt.Sprintf("%+v", []OrderBookDiffEntry(e))
}

type OrderBookDiffEntry struct {
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
