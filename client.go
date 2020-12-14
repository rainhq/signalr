package signalr

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
)

type Client struct {
	mtx  sync.Mutex
	hub  string
	conn *Conn

	invocationID int
	invocations  map[int]Invocation
	callbacks    map[string]CallbackStream
	backlog      map[string][]ClientMsg
}

type Invocation struct {
	ctx    context.Context
	id     int
	method string
	ch     chan invocationResult
	err    error
}

type CallbackStream struct {
	ctx     context.Context
	cancel  context.CancelFunc
	ch      chan callbackResult
	backlog []ClientMsg
	err     error
}

func NewClient(hub string, conn *Conn) *Client {
	return &Client{
		hub:         hub,
		conn:        conn,
		invocations: make(map[int]Invocation),
		callbacks:   make(map[string]CallbackStream),
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

func (c *Client) Invoke(ctx context.Context, method string, args ...interface{}) Invocation {
	rawArgs, err := marshalArgs(args)
	if err != nil {
		return Invocation{err: fmt.Errorf("failed to marshal args: %w", err)}
	}

	c.mtx.Lock()
	id := c.invocationID
	c.invocationID++
	c.mtx.Unlock()

	req := ClientMsg{Hub: c.hub, Method: method, Args: rawArgs, InvocationID: id}

	if err := c.conn.WriteMessage(ctx, req); err != nil {
		return Invocation{err: err}
	}

	res := Invocation{
		ctx:    ctx,
		id:     id,
		method: method,
		ch:     make(chan invocationResult, 1),
	}

	c.mtx.Lock()
	c.invocations[id] = res
	c.mtx.Unlock()

	return res
}

func (c *Client) Callback(ctx context.Context, method string) CallbackStream {
	c.mtx.Lock()
	defer c.mtx.Unlock()

	if cb, ok := c.callbacks[method]; ok {
		select {
		case <-cb.ctx.Done():
		default:
			return CallbackStream{err: &DuplicateCallbackError{method: method}}
		}
	}

	ctx, cancel := context.WithCancel(ctx)

	res := CallbackStream{
		ctx:     ctx,
		cancel:  cancel,
		ch:      make(chan callbackResult, 1),
		backlog: c.backlog[method],
	}

	c.callbacks[method] = res

	return res
}

func (c *Client) process(msg Message) {
	c.mtx.Lock()
	defer c.mtx.Unlock()

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

	for _, clientMsg := range msg.Messages {
		method := clientMsg.Method
		callback, ok := c.callbacks[method]
		if !ok {
			c.backlog[method] = append(c.backlog[method], msg.Messages...)
			continue
		}

		select {
		case <-callback.ctx.Done():
			delete(c.callbacks, clientMsg.Method)
		case callback.ch <- callbackResult{message: clientMsg}:
		}
	}
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

	c.callbacks = make(map[string]CallbackStream)

	for _, invocation := range c.invocations {
		select {
		case <-invocation.ctx.Done():
		case invocation.ch <- invocationResult{err: context.Canceled}:
		}

		close(invocation.ch)
	}

	c.invocations = make(map[int]Invocation)
}

func (r Invocation) Unmarshal(dest interface{}) error {
	if r.err != nil {
		return r.err
	}

	select {
	case <-r.ctx.Done():
		return r.ctx.Err()
	case res := <-r.ch:
		if res.err != nil {
			return res.err
		}

		return json.Unmarshal(res.result, dest)
	}
}

func (r Invocation) Exec() error {
	return r.err
}

func (s CallbackStream) Read(args ...interface{}) error {
	res := s.readResult()
	if res.err != nil {
		return res.err
	}

	if err := unmarshalArgs(res.message.Args, args); err != nil {
		return fmt.Errorf("failed to unmarshal ")
	}

	return nil
}

func (s CallbackStream) readResult() callbackResult {
	// ensure non-blocking read of backlog
	select {
	case <-s.ctx.Done():
		return callbackResult{err: s.ctx.Err()}
	default:
	}

	switch {
	case len(s.backlog) == 1:
		clientMsg := s.backlog[0]
		s.backlog = nil
		return callbackResult{message: clientMsg}
	case len(s.backlog) > 1:
		clientMsg := s.backlog[0]
		s.backlog = s.backlog[1:]
		return callbackResult{message: clientMsg}
	}

	select {
	case <-s.ctx.Done():
		return callbackResult{err: s.ctx.Err()}
	case res := <-s.ch:
		return res
	}
}

func (s CallbackStream) Close() {
	s.cancel()
	close(s.ch)
}

func marshalArgs(src []interface{}) ([]json.RawMessage, error) {
	res := make([]json.RawMessage, len(src))
	for i, v := range src {
		data, err := json.Marshal(v)
		if err != nil {
			return nil, err
		}

		res[i] = json.RawMessage(data)
	}

	return res, nil
}

func unmarshalArgs(src []json.RawMessage, dest []interface{}) error {
	if len(src) != len(dest) {
		return fmt.Errorf("invalid number of arguments: expected %d, got %d", len(src), len(dest))
	}

	for i, v := range src {
		if err := json.Unmarshal(v, dest[i]); err != nil {
			return err
		}
	}

	return nil
}

type invocationResult struct {
	result json.RawMessage
	err    error
}

type callbackResult struct {
	message ClientMsg
	err     error
}
