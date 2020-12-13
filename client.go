package signalr

import (
	"context"
	"sync"
)

type Client struct {
	mtx  sync.Mutex
	hub  string
	conn *Conn

	invocationID int
	invocations  map[int]invocation
	callbacks    map[string]callback
	backlog      map[string][]ClientMsg
}

type CallbackFunc func(messages []ClientMsg) error

func NewClient(hub string, conn *Conn) *Client {
	return &Client{
		hub:         hub,
		conn:        conn,
		invocations: make(map[int]invocation),
		callbacks:   make(map[string]callback),
		backlog:     make(map[string][]ClientMsg),
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

		c.process(msg)
	}
}

func (c *Client) Invoke(ctx context.Context, method string, args ...interface{}) (interface{}, error) {
	id, ch, err := c.addInvocation(ctx, method)
	if err != nil {
		return nil, err
	}

	req := ClientMsg{Hub: c.hub, Method: method, Args: args, InvocationID: id}

	if err := c.conn.WriteMessage(ctx, req); err != nil {
		return nil, err
	}

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case res := <-ch:
		return res.result, res.err
	}
}

func (c *Client) Callback(ctx context.Context, method string, callback CallbackFunc) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	ch, err := c.addCallback(ctx, method)
	if err != nil {
		return err
	}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case res := <-ch:
			if res.err != nil {
				return err
			}

			if err := callback(res.messages); err != nil {
				return err
			}
		}
	}
}

func (c *Client) process(msg Message) {
	c.mtx.Lock()
	defer c.mtx.Unlock()

	for m, messages := range c.backlog {
		cb, ok := c.callbacks[m]
		if !ok {
			continue
		}

		select {
		case <-cb.ctx.Done():
			close(cb.ch)
			delete(c.callbacks, m)
		case cb.ch <- callbackResult{messages: messages}:
			delete(c.backlog, m)
		}
	}

	for id, invocation := range c.invocations {
		if msg.InvocationID != id {
			continue
		}

		var err error
		if msg.Error != "" {
			err = &InvocationError{
				method:  invocation.method,
				id:      id,
				message: msg.Error,
			}
		}

		select {
		case <-invocation.ctx.Done():
		case invocation.ch <- invocationResult{result: msg.Result, err: err}:
		}

		close(invocation.ch)
		delete(c.invocations, id)

		return
	}

	if len(msg.Messages) == 0 {
		return
	}

	method := msg.Messages[0].Method

	for m, callback := range c.callbacks {
		if m != method {
			continue
		}

		select {
		case <-callback.ctx.Done():
			close(callback.ch)
			delete(c.callbacks, m)
		case callback.ch <- callbackResult{messages: msg.Messages}:
		}

		return
	}

	c.backlog[method] = append(c.backlog[method], msg.Messages...)
}

func (c *Client) addInvocation(ctx context.Context, method string) (id int, ch chan invocationResult, err error) {
	c.mtx.Lock()
	defer c.mtx.Unlock()

	id = c.invocationID
	c.invocationID++

	ch = make(chan invocationResult, 1)

	c.invocations[id] = invocation{
		ctx:    ctx,
		method: method,
		ch:     ch,
	}

	return id, ch, nil
}

func (c *Client) addCallback(ctx context.Context, method string) (chan callbackResult, error) {
	c.mtx.Lock()
	defer c.mtx.Unlock()

	if cb, ok := c.callbacks[method]; ok {
		select {
		case <-cb.ctx.Done():
			close(cb.ch)
		default:
			return nil, &DuplicateCallbackError{method: method}
		}
	}

	ch := make(chan callbackResult, 1)

	c.callbacks[method] = callback{
		ctx: ctx,
		ch:  ch,
	}

	return ch, nil
}

func (c *Client) cleanup() {
	c.mtx.Lock()
	defer c.mtx.Unlock()

	for _, callback := range c.callbacks {
		select {
		case <-callback.ctx.Done():
		case callback.ch <- callbackResult{err: context.Canceled}:
		}

		close(callback.ch)
	}

	c.callbacks = make(map[string]callback)

	for _, invocation := range c.invocations {
		select {
		case <-invocation.ctx.Done():
		case invocation.ch <- invocationResult{err: context.Canceled}:
		}

		close(invocation.ch)
	}

	c.invocations = make(map[int]invocation)
}

type callback struct {
	ctx context.Context
	ch  chan callbackResult
}

type invocation struct {
	ctx    context.Context
	method string
	ch     chan invocationResult
}

type invocationResult struct {
	result interface{}
	err    error
}

type callbackResult struct {
	messages []ClientMsg
	err      error
}
