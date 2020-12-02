package signalr

import "fmt"

type NegotiateError struct {
	cause error
}

func (e NegotiateError) Error() string {
	return fmt.Sprintf("negotiate failed: %+v", e.cause)
}

func (e NegotiateError) Unwrap() error {
	return e.cause
}

type ConnectError struct {
	cause error
}

func (e ConnectError) Error() string {
	return fmt.Sprintf("connect failed: %+v", e.cause)
}

func (e ConnectError) Unwrap() error {
	return e.cause
}

type StartError struct {
	cause error
}

func (e StartError) Error() string {
	return fmt.Sprintf("start failed: %+v", e.cause)
}

func (e StartError) Unwrap() error {
	return e.cause
}
