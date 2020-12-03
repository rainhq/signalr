package signalr

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"math/big"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"sync"

	"github.com/cenkalti/backoff/v4"
	"github.com/gorilla/websocket"
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

	conn, err := connect(ctx, dialer, endpoint, "connect", cfg.Headers, &state, cfg.ConnectBackoff())
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
	switch {
	case websocket.IsCloseError(err, 1000, 1001, 1006):
		dctx, cancel := context.WithTimeout(ctx, c.config.MaxReconnectDuration)
		defer cancel()

		conn, err := connect(dctx, c.dialer, c.endpoint, "reconnect", c.config.Headers, c.state, c.config.ReconnectBackoff())
		if err != nil {
			return &ConnectError{cause: err}
		}

		c.conn = conn
	case err != nil:
		return &ReadError{cause: err}
	}

	return nil
}

// Send sends a message to the websocket connection.
func (c *Client) WriteMessage(m ClientMsg) error {
	c.mtx.Lock()
	defer c.mtx.Unlock()

	data, err := json.Marshal(m)
	if err != nil {
		return &WriteError{cause: err}
	}

	if err := c.conn.WriteMessage(textMessage, data); err != nil {
		return &WriteError{cause: err}
	}

	return nil
}

func (c *Client) Close() error {
	return c.conn.Close()
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

		var res negotiateResponse
		if err := json.Unmarshal(data, &res); err != nil {
			return err
		}

		// Set the connection token and ID.
		state.ConnectionToken = res.ConnectionToken
		state.ConnectionID = res.ConnectionID

		// Update the protocol version.
		state.Protocol = res.ProtocolVersion

		return nil
	}, backoff.WithContext(bo, ctx))
}

// connect implements the connect step of the SignalR connection sequence.
func connect(ctx context.Context, dialer WebsocketDialer, endpoint, command string, headers http.Header, state *State, bo backoff.BackOff) (WebsocketConn, error) {
	// Example connect URL:
	// https://socket.bittrex.com/signalr/connect?
	//   transport=webSockets&
	//   clientProtocol=1.5&
	//   connectionToken=<token>&
	//   connectionData=%5B%7B%22name%22%3A%22corehub%22%7D%5D&
	//   tid=5
	// -> returns connection ID. (e.g.: d-F2577E41-B,0|If60z,0|If600,1)
	endpoint, err := makeURL(endpoint, command, state)
	if err != nil {
		return nil, err
	}

	var conn WebsocketConn
	err = backoff.Retry(func() error {
		var (
			status int
			err    error
		)
		conn, status, err = dialer.Dial(ctx, endpoint, headers)
		if err != nil {
			log.Printf("%+v", err)
			return &DialError{status: status, cause: err}
		}

		return nil
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

		var res startResponse
		if err := json.Unmarshal(data, &res); err != nil {
			return err
		}

		if res.Response != "started" {
			return &InvalidStartResponseError{actual: res.Response}
		}

		var msg Message
		if err := readMessage(conn, &msg, state); err != nil {
			return &ReadError{cause: err}
		}

		if msg.S != statusStarted {
			return &InvalidInitMessageError{actual: msg.S}
		}

		return nil
	}, backoff.WithContext(bo, ctx))
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
		u.Scheme = "ws"
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
