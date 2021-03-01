package bittrex

import (
	"fmt"
	"strings"
)

type SubscriptionError struct {
	Streams []string
}

func (e *SubscriptionError) Error() string {
	return fmt.Sprintf("subscription failed: %s", strings.Join(e.Streams, ", "))
}

type SequenceError struct {
	Expected, Actual int
}

func (e *SequenceError) Error() string {
	return fmt.Sprintf("invalid sequence: expected %d, got %d", e.Expected, e.Actual)
}

type OrderNotFoundError struct {
	ID string
}

func (e *OrderNotFoundError) Error() string {
	return fmt.Sprintf("order %q not found", e.ID)
}
