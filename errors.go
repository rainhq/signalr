package signalr

import (
	"errors"
	"fmt"
)

type NegotiateError struct {
	cause error
}

func (e *NegotiateError) Error() string {
	return fmt.Sprintf("negotiate failed: %+v", e.cause)
}

func (e *NegotiateError) Unwrap() error {
	return e.cause
}

type ConnectError struct {
	cause error
}

func (e *ConnectError) Error() string {
	return fmt.Sprintf("connect failed: %+v", e.cause)
}

func (e *ConnectError) Unwrap() error {
	return e.cause
}

type StartError struct {
	cause error
}

func (e *StartError) Error() string {
	return fmt.Sprintf("start failed: %+v", e.cause)
}

func (e *StartError) Unwrap() error {
	return e.cause
}

type DialError struct {
	status int
	cause  error
}

func (e *DialError) Error() string {
	if e.status != 0 {
		return fmt.Sprintf("dial failed (%d): %v", e.status, e.cause)
	}

	return fmt.Sprintf("dial failed: %v", e.cause)
}

func (e *DialError) Unwrap() error {
	return e.cause
}

type CloseError struct {
	code int
	text string
}

func (e *CloseError) Error() string {
	if e.text != "" {
		return fmt.Sprintf("websocket closed %d: %s", e.code, e.text)
	}

	return fmt.Sprintf("websocket closed %d", e.code)
}

func IsCloseError(err error, codes ...int) bool {
	closeErr := &CloseError{}
	if !errors.As(err, &closeErr) {
		return false
	}

	for _, code := range codes {
		if closeErr.code == code {
			return true
		}
	}

	return false
}

type ReadError struct {
	cause error
}

func (e *ReadError) Error() string {
	return fmt.Sprintf("read failed: %+v", e.cause)
}

func (e *ReadError) Unwrap() error {
	return e.cause
}

type WriteError struct {
	cause error
}

func (e *WriteError) Error() string {
	return fmt.Sprintf("write failed: %+v", e.cause)
}

func (e *WriteError) Unwrap() error {
	return e.cause
}

type InvalidStartResponseError struct {
	actual string
}

func (e *InvalidStartResponseError) Error() string {
	return fmt.Sprintf("expected start response %q, got %q", "started", e.actual)
}

type InvalidInitMessageError struct {
	actual int
}

func (e *InvalidInitMessageError) Error() string {
	return fmt.Sprintf("expected init message S value %d, got %d", 1, e.actual)
}

type DuplicateCallbackError struct {
	method string
}

func (e *DuplicateCallbackError) Error() string {
	return fmt.Sprintf("duplicate callback for method %q", e.method)
}

type InvocationError struct {
	method  string
	id      int
	message string
}

func (e *InvocationError) Error() string {
	return fmt.Sprintf("failed to invoke %q (%d): %s", e.method, e.id, e.message)
}
