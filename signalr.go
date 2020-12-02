package signalr

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"math/big"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"sync"

	"github.com/cenkalti/backoff/v4"
	"github.com/gorilla/websocket"
	"github.com/rainhq/signalr/v2/hubs"
	"github.com/rainhq/signalr/v2/internal/model"
)

// Client represents a SignlR client. It manages connections so that the caller
// doesn't have to.
type Client struct {
	mtx      sync.Mutex
	client   *http.Client
	dialer   WebsocketDialer
	conn     WebsocketConn
	endpoint string
	config   *config
	state    *State
}

// Dial connects to Signalr endpoint
func Dial(ctx context.Context, endpoint, cdata string, opts ...Opt) (*Client, error) {
	cfg := defaultConfig
	for _, opt := range opts {
		opt(&cfg)
	}

	client := cfg.Client

	if client.Jar == nil {
		jar, err := cookiejar.New(nil)
		if err != nil {
			return nil, err
		}

		client.Jar = jar
	}

	state := State{
		ConnectionData: cdata,
		Protocol:       cfg.Protocol,
	}

	if err := negotiate(ctx, client, endpoint, cfg.Headers, &state, cfg.NegotiateBackoff()); err != nil {
		return nil, &NegotiateError{cause: err}
	}

	dialer := cfg.Dialer(client)

	conn, err := connect(ctx, dialer, endpoint, cfg.Headers, &state, cfg.ConnectBackoff())
	if err != nil {
		return nil, &ConnectError{cause: err}
	}

	err = start(ctx, client, conn, endpoint, cfg.Headers, &state, cfg.StartBackoff())
	if err != nil {
		return nil, &StartError{cause: err}
	}

	return &Client{
		client:   client,
		dialer:   dialer,
		conn:     conn,
		endpoint: endpoint,
		config:   &cfg,
		state:    &state,
	}, nil
}

func (c *Client) State() *State {
	c.mtx.Lock()
	state := *c.state
	c.mtx.Unlock()

	return &state
}

func (c *Client) ReadMessage(ctx context.Context, msg *Message) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	c.mtx.Lock()
	defer c.mtx.Unlock()

	err := readMessage(c.conn, msg, c.state)

	if websocket.IsCloseError(err, 1000, 1001, 1006) {
		ctx, cancel := context.WithTimeout(ctx, c.config.MaxReconnectDuration)
		defer cancel()

		conn, err := reconnect(ctx, c.dialer, c.endpoint, c.config.Headers, c.state, c.config.ReconnectBackoff())
		if err != nil {
			return ConnectError{cause: err}
		}

		c.conn = conn
	}

	return nil
}

// Send sends a message to the websocket connection.
func (c *Client) WriteMessage(m hubs.ClientMsg) error {
	c.mtx.Lock()
	defer c.mtx.Unlock()

	// Write the message.
	err := c.conn.WriteJSON(m)
	if err != nil {
		return err
	}

	return nil
}

// negotiate implements the negotiate step of the SignalR connection sequence.
func negotiate(ctx context.Context, client *http.Client, endpoint string, headers http.Header, state *State, bo backoff.BackOff) error {
	// Reset Token
	state.ConnectionToken = ""

	// Make a "negotiate" URL.
	endpoint, err := makeURL(endpoint, "negotiate", state)
	if err != nil {
		return err
	}

	return backoff.Retry(func() error {
		req, err := prepareRequest(ctx, endpoint, headers)
		if err != nil {
			return fmt.Errorf("failed to prepare request: %w", err)
		}

		// Perform the request.
		httpRes, err := client.Do(req)
		if err != nil {
			return fmt.Errorf("request failed: %w", err)
		}
		defer httpRes.Body.Close()

		if httpRes.StatusCode != http.StatusOK {
			return fmt.Errorf("request failed: %s", httpRes.Status)
		}

		data, err := ioutil.ReadAll(httpRes.Body)
		if err != nil {
			return fmt.Errorf("read failed: %w", err)
		}

		return processNegotiateResponse(data, state)
	}, backoff.WithContext(bo, ctx))
}

func processNegotiateResponse(data []byte, state *State) error {
	var res model.NegotiateResponse
	if err := json.Unmarshal(data, &res); err != nil {
		return err
	}

	// Set the connection token and ID.
	state.ConnectionToken = res.ConnectionToken
	state.ConnectionID = res.ConnectionID

	// Update the protocol version.
	state.Protocol = res.ProtocolVersion

	return nil
}

// connect implements the connect step of the SignalR connection sequence.
func connect(ctx context.Context, dialer WebsocketDialer, endpoint string, headers http.Header, state *State, bo backoff.BackOff) (WebsocketConn, error) {
	// Example connect URL:
	// https://socket.bittrex.com/signalr/connect?
	//   transport=webSockets&
	//   clientProtocol=1.5&
	//   connectionToken=<token>&
	//   connectionData=%5B%7B%22name%22%3A%22corehub%22%7D%5D&
	//   tid=5
	// -> returns connection ID. (e.g.: d-F2577E41-B,0|If60z,0|If600,1)
	endpoint, err := makeURL(endpoint, "connect", state)
	if err != nil {
		return nil, err
	}

	bo = backoff.WithContext(bo, ctx)

	// Perform the connection.
	conn, err := xconnect(ctx, dialer, endpoint, headers, bo)
	if err != nil {
		return nil, err
	}

	return conn, nil
}

func reconnect(ctx context.Context, dialer WebsocketDialer, endpoint string, headers http.Header, state *State, bo backoff.BackOff) (WebsocketConn, error) {
	endpoint, err := makeURL(endpoint, "reconnect", state)
	if err != nil {
		return nil, err
	}

	// Perform the reconnection.
	conn, err := xconnect(ctx, dialer, endpoint, headers, bo)
	if err != nil {
		return nil, err
	}

	return conn, nil
}

func xconnect(ctx context.Context, dialer WebsocketDialer, endpoint string, headers http.Header, bo backoff.BackOff) (WebsocketConn, error) {
	var conn WebsocketConn
	err := backoff.Retry(func() error {
		var (
			status int
			err    error
		)
		conn, status, err = dialer.Dial(ctx, endpoint, headers)
		switch {
		case status >= http.StatusOK:
			return fmt.Errorf("dial failed (%d): %w", status, err)
		case !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded):
			return fmt.Errorf("dial failed: %w", err)
		default:
			return err
		}
	}, backoff.WithContext(bo, ctx))

	return conn, err
}

// Start implements the start step of the SignalR connection sequence.
func start(ctx context.Context, client *http.Client, conn WebsocketConn, endpoint string, headers http.Header, state *State, bo backoff.BackOff) error {
	endpoint, err := makeURL(endpoint, "start", state)
	if err != nil {
		return err
	}

	req, err := prepareRequest(ctx, endpoint, headers)
	if err != nil {
		return fmt.Errorf("failed to prepare request: %w", err)
	}

	// Perform the request in a retry loop.
	return backoff.Retry(func() error {
		httpRes, err := client.Do(req)
		if err != nil {
			return fmt.Errorf("request failed: %w", err)
		}
		defer httpRes.Body.Close()

		data, err := ioutil.ReadAll(httpRes.Body)
		if err != nil {
			return fmt.Errorf("read failed: %w", err)
		}

		return processStartResponse(data, conn, state)
	}, backoff.WithContext(bo, ctx))
}

func processStartResponse(data []byte, conn WebsocketConn, state *State) error {
	var res model.StartResponse
	if err := json.Unmarshal(data, &res); err != nil {
		return err
	}

	if res.Response != "started" {
		return fmt.Errorf("start response is not \"started\": %q", res.Response)
	}

	var msg Message
	if err := readMessage(conn, &msg, state); err != nil {
		return fmt.Errorf("failed to read message: %w", err)
	}

	if msg.S != 1 {
		return fmt.Errorf("unexpected S value received from server: %d", msg.S)
	}

	return nil
}

type State struct {
	ConnectionData  string
	ConnectionID    string
	ConnectionToken string
	GroupsToken     string
	MessageID       string
	Protocol        string
}

func makeURL(endpoint, command string, state *State) (string, error) {
	u, err := url.Parse(endpoint)
	if err != nil {
		return "", err
	}

	query := u.Query()

	query.Set("connectionData", state.ConnectionData)
	query.Set("clientProtocol", state.Protocol)

	// Set the connectionToken.
	if state.ConnectionToken != "" {
		query.Set("connectionToken", state.ConnectionToken)
	}

	switch command {
	case "negotiate":
		u.Path += "/negotiate"
	case "connect":
		if err := connectURL(u); err != nil {
			return "", err
		}
		u.Path += "/connect"
	case "reconnect":
		if err := connectURL(u); err != nil {
			return "", err
		}
		if groupsToken := state.GroupsToken; groupsToken != "" {
			query.Set("groupsToken", groupsToken)
		}
		if messageID := state.MessageID; messageID != "" {
			query.Set("messageId", messageID)
		}
		u.Path += "/reconnect"
	case "start":
		query.Set("transport", "webSockets")
		u.Path += "/start"
	}

	// Set the parameters.
	u.RawQuery = query.Encode()

	return u.String(), nil
}

func connectURL(u *url.URL) error {
	switch {
	case u.Scheme == "https":
		u.Scheme = "wss"
	case u.Scheme == "http":
		u.Scheme = "http"
	default:
		return fmt.Errorf("invalid scheme %s", u.Scheme)
	}

	query := u.Query()

	query.Set("transport", "webSockets")
	tid, _ := rand.Int(rand.Reader, big.NewInt(1000000))
	query.Set("tid", tid.String())

	return nil
}

func prepareRequest(ctx context.Context, u string, headers http.Header) (*http.Request, error) {
	// Make the GET request object.
	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		err = fmt.Errorf("get request creation failed")
		return nil, err
	}

	// Add all header values.
	req.Header = headers

	return req, nil
}
