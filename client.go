package signalr

import (
	"context"
	"sync"
	"sync/atomic"
)

type Client struct {
	mtx  sync.Mutex
	hub  string
	conn *Conn

	invocationID int32
	invocations  map[string]chan invocationResult
	callbacks    map[string]chan callbackResult
}

type CallbackFunc func(messages []ClientMsg) error

func NewClient(hub string, conn *Conn) *Client {
	return &Client{
		hub:         hub,
		conn:        conn,
		invocations: make(map[string]chan invocationResult),
		callbacks:   make(map[string]chan callbackResult),
	}
}

func (c *Client) Run(ctx context.Context) error {
	for {
		select {
		case <-ctx.Done():
			c.cleanup()
		default:
		}

		var msg Message
		if err := c.conn.ReadMessage(ctx, &msg); err != nil {
			return err
		}

		if len(msg.Messages) == 0 {
			continue
		}

		c.process(msg)
	}
}

func (c *Client) Invoke(ctx context.Context, method string, args ...interface{}) (interface{}, error) {
	invocationID := int(atomic.AddInt32(&c.invocationID, 1))
	req := ClientMsg{Hub: c.hub, Method: method, Args: args, InvocationID: invocationID}

	ch, err := c.addInvocation(method)
	if err != nil {
		return nil, err
	}
	defer c.removeInvocation(method)

	if err := c.conn.WriteMessage(ctx, req); err != nil {
		return nil, err
	}

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case res := <-ch:
		return res.Result, res.Error
	}
}

func (c *Client) Callback(ctx context.Context, method string, callback CallbackFunc) error {
	ch, err := c.addCallback(method)
	if err != nil {
		return err
	}
	defer c.removeCallback(method)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case res := <-ch:
			if res.Error != nil {
				return err
			}

			if err := callback(res.Messages); err != nil {
				return err
			}
		}
	}
}

func (c *Client) process(msg Message) {
	c.mtx.Lock()
	defer c.mtx.Unlock()

	method := msg.Messages[0].Method

	for m, ch := range c.invocations {
		if m != method {
			continue
		}

		var err error
		if msg.Error != "" {
			err = &InvocationError{
				method:  method,
				message: msg.Error,
			}
		}

		ch <- invocationResult{Result: msg.Result, Error: err}
		c.removeInvocation(method)
	}

	for m, ch := range c.callbacks {
		if m != method {
			continue
		}

		ch <- callbackResult{Messages: msg.Messages}
	}
}

func (c *Client) addInvocation(method string) (chan invocationResult, error) {
	c.mtx.Lock()
	defer c.mtx.Unlock()

	if _, ok := c.invocations[method]; ok {
		c.mtx.Unlock()
		return nil, &DuplicateInvocationError{method: method}
	}

	ch := make(chan invocationResult, 1)
	c.invocations[method] = ch

	return ch, nil
}

func (c *Client) removeInvocation(method string) {
	c.mtx.Lock()
	defer c.mtx.Unlock()

	ch, ok := c.invocations[method]
	if ok {
		close(ch)
		return
	}

	delete(c.invocations, method)
}

func (c *Client) addCallback(method string) (chan callbackResult, error) {
	c.mtx.Lock()
	defer c.mtx.Unlock()

	if _, ok := c.callbacks[method]; ok {
		return nil, &DuplicateCallbackError{method: method}
	}

	ch := make(chan callbackResult, 1)
	c.callbacks[method] = ch

	return ch, nil
}

func (c *Client) removeCallback(method string) {
	c.mtx.Lock()
	defer c.mtx.Unlock()

	ch, ok := c.callbacks[method]
	if ok {
		close(ch)
		return
	}

	delete(c.callbacks, method)
}

func (c *Client) cleanup() {
	c.mtx.Lock()
	defer c.mtx.Unlock()

	for _, ch := range c.callbacks {
		ch <- callbackResult{Error: context.Canceled}
		close(ch)
	}

	c.callbacks = make(map[string]chan callbackResult)

	for _, ch := range c.invocations {
		ch <- invocationResult{Error: context.Canceled}
		close(ch)
	}

	c.invocations = make(map[string]chan invocationResult)
}

type invocationResult struct {
	Result interface{}
	Error  error
}

type callbackResult struct {
	Messages []ClientMsg
	Error    error
}
