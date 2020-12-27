package main

import (
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
)

func TestOrderBookEntriesApply(t *testing.T) {
	entries := OrderBookEntries{
		{
			Quantity: decimal.RequireFromString("0.05007319"),
			Rate:     decimal.RequireFromString("23949.50000000"),
		},
		{
			Quantity: decimal.RequireFromString("0.01200000"),
			Rate:     decimal.RequireFromString("23949.49900000"),
		},
		{
			Quantity: decimal.RequireFromString("0.49868000"),
			Rate:     decimal.RequireFromString("23949.49800000"),
		},
		{
			Quantity: decimal.RequireFromString("0.08302760"),
			Rate:     decimal.RequireFromString("23949.40100000"),
		},
		{
			Quantity: decimal.RequireFromString("0.10000000"),
			Rate:     decimal.RequireFromString("23949.39900000"),
		},
	}

	updateFirst := OrderBookEntry{
		Quantity: decimal.RequireFromString("0.03007319"),
		Rate:     decimal.RequireFromString("23949.50000000"),
	}

	addFirst := OrderBookEntry{
		Quantity: decimal.RequireFromString("0.01000000"),
		Rate:     decimal.RequireFromString("23949.50100000"),
	}

	addMiddle := OrderBookEntry{
		Quantity: decimal.RequireFromString("0.08302760"),
		Rate:     decimal.RequireFromString("23949.40000000"),
	}

	addLast := OrderBookEntry{
		Quantity: decimal.RequireFromString("0.10000000"),
		Rate:     decimal.RequireFromString("23949.39800000"),
	}

	tests := []struct {
		name            string
		deltas          OrderBookEntries
		expectedEntries OrderBookEntries
		expectedDiff    OrderBookDiffEntries
	}{
		{
			name:   "should update delta",
			deltas: OrderBookEntries{updateFirst},
			expectedEntries: append(
				OrderBookEntries{updateFirst},
				entries[1:]...,
			),
			expectedDiff: OrderBookDiffEntries{
				updateFirst.OrderBookDiffEntry(UpdateAction),
			},
		},
		{
			name:   "should add first delta",
			deltas: OrderBookEntries{addFirst},
			expectedEntries: append(
				OrderBookEntries{addFirst},
				entries...,
			),
			expectedDiff: OrderBookDiffEntries{
				addFirst.OrderBookDiffEntry(AddAction),
			},
		},
		{
			name:   "should add last delta",
			deltas: OrderBookEntries{addLast},
			expectedEntries: append(
				entries,
				addLast,
			),
			expectedDiff: OrderBookDiffEntries{
				addLast.OrderBookDiffEntry(AddAction),
			},
		},
		{
			name:   "should add multiple deltas",
			deltas: OrderBookEntries{addFirst, addMiddle, addLast},
			expectedEntries: append(
				OrderBookEntries{addFirst},
				entries[0],
				entries[1],
				entries[2],
				entries[3],
				addMiddle,
				entries[4],
				addLast,
			),
			expectedDiff: OrderBookDiffEntries{
				addFirst.OrderBookDiffEntry(AddAction),
				addMiddle.OrderBookDiffEntry(AddAction),
				addLast.OrderBookDiffEntry(AddAction),
			},
		},
		{
			name: "should delete first delta",
			deltas: OrderBookEntries{
				{Quantity: decimal.Zero, Rate: entries[0].Rate},
			},
			expectedEntries: entries[1:],
			expectedDiff: OrderBookDiffEntries{
				{Action: DeleteAction, Quantity: decimal.Zero, Rate: entries[0].Rate},
			},
		},
		{
			name: "should delete multiple deltas",
			deltas: OrderBookEntries{
				{Quantity: decimal.Zero, Rate: entries[1].Rate},
				{Quantity: decimal.Zero, Rate: entries[2].Rate},
			},
			expectedEntries: OrderBookEntries{
				entries[0],
				entries[3],
				entries[4],
			},
			expectedDiff: OrderBookDiffEntries{
				{Action: DeleteAction, Quantity: decimal.Zero, Rate: entries[1].Rate},
				{Action: DeleteAction, Quantity: decimal.Zero, Rate: entries[2].Rate},
			},
		},
		{
			name: "should delete last delta",
			deltas: OrderBookEntries{
				{Quantity: decimal.Zero, Rate: entries[4].Rate},
			},
			expectedEntries: entries[:4],
			expectedDiff: OrderBookDiffEntries{
				{Action: DeleteAction, Quantity: decimal.Zero, Rate: entries[4].Rate},
			},
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			e, d := entries.Apply(test.deltas, decimal.Decimal.LessThanOrEqual)
			assert.Equal(t, test.expectedEntries.String(), e.String(), "entries")
			assert.Equal(t, test.expectedDiff, d, "diff")
		})
	}
}
