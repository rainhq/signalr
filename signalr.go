package signalr

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"log"
	"math/big"
	"net/http"
	"net/url"
	"os"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/rainhq/signalr/v2/hubs"
	"go.uber.org/atomic"
)

var ErrConnectionNotSet = errors.New("send: connection not set")

// Client represents a SignlR client. It manages connections so that the caller
// doesn't have to.
type Client struct {
	// The host providing the SignalR service.
	Host string

	// The relative path where the SignalR service is provided.
	Endpoint string

	// The websockets protocol version.
	Protocol string

	// Connection data passed to the service's websocket.
	ConnectionData string

	// User-defined custom parameters passed with each request to the server.
	Params map[string]string

	// The HTTPClient used to initialize the websocket connection.
	HTTPClient *http.Client

	// An optional setting to provide a non-default TLS configuration to use
	// when connecting to the websocket.
	TLSClientConfig *tls.Config

	// Either HTTPS or HTTP.
	Scheme Scheme

	// The maximum number of times to re-attempt a negotiation.
	MaxNegotiateRetries int

	// The maximum number of times to re-attempt a connection.
	MaxConnectRetries int

	// The maximum number of times to re-attempt a reconnection.
	MaxReconnectRetries int

	// The maximum number of times to re-attempt a start command.
	MaxStartRetries int

	// The time to wait before retrying, in the event that an error occurs
	// when contacting the SignalR service.
	RetryWaitDuration time.Duration

	// The maximum amount of time to spend retrying a reconnect attempt.
	MaxReconnectAttemptDuration time.Duration

	// This is the connection token set during the negotiate phase of the
	// protocol and used to uniquely identify the connection to the server
	// in all subsequent phases of the connection.
	ConnectionToken string

	// This is the ID of the connection. It is set during the negotiate
	// phase and then ignored by all subsequent steps.
	ConnectionID string

	// The groups token that is used during reconnect attempts.
	//
	// This is an example groups token:
	// nolint:lll
	// yUcSohHrAZGEwK62B4Ao0WYac82p5yeRvHHInBgVmSK7jX++ym3kIgDy466yW/gRPp2l3Py8G45mRLJ9FslB3sKfsDPUNWL1b54cvjaSXCUo0znzyACxrN2Y0kNLR59h7hb6PgOSfy3Z2R5CUSVm5LZg6jg=
	GroupsToken atomic.String

	// The message ID that is used during reconnect attempts.
	//
	// This is an example message ID: d-8B839DC3-C,0|aaZe,0|aaZf,2|C1,2A801
	MessageID atomic.String

	// Header values that should be applied to all HTTP requests.
	Headers map[string]string

	// This value is not part of the SignalR protocol. If this value is set,
	// it will be used in debug messages.
	CustomID string

	Sync bool

	// This field holds a struct that can read messages from and write JSON
	// objects to a websocket. In practice, this is simply a raw websocket
	// connection that results from a successful connection to the SignalR
	// server.
	conn    WebsocketConn
	connMux sync.Mutex
}

// Negotiate implements the negotiate step of the SignalR connection sequence.
func (c *Client) Negotiate(ctx context.Context) error {
	// Reset the connection token in case it has been set.
	c.ConnectionToken = ""

	// Make a "negotiate" URL.
	u := makeURL("negotiate", c)

	var (
		req  *http.Request
		resp *http.Response
		err  error
	)
	for i := 0; i < c.MaxNegotiateRetries; i++ {
		req, err = prepareRequest(ctx, u.String(), c.Headers)
		if err != nil {
			return fmt.Errorf("failed to prepare request: %w", err)
		}

		// Perform the request.
		resp, err = c.HTTPClient.Do(req)
		if err != nil {
			return fmt.Errorf("request failed: %w", err)
		}
		defer resp.Body.Close()

		switch {
		case resp.StatusCode != http.StatusOK:
			err = fmt.Errorf("request failed: %s", resp.Status)
			debugMessage("%snegotiate: retrying after %s", prefixedID(c.CustomID), resp.Status)
			time.Sleep(c.RetryWaitDuration)
		default:
			var data []byte
			data, err = ioutil.ReadAll(resp.Body)
			if err != nil {
				return fmt.Errorf("read failed: %w", err)
			}

			return c.processNegotiateResponse(data)
		}
	}

	debugMessage("%sthe negotiate retry was unsuccessful", prefixedID(c.CustomID))
	return err
}

func (c *Client) processNegotiateResponse(data []byte) error {
	// Create a struct to allow parsing of the response object.
	parsed := struct {
		URL                     string `json:"Url"`
		ConnectionToken         string
		ConnectionID            string `json:"ConnectionId"`
		KeepAliveTimeout        float64
		DisconnectTimeout       float64
		ConnectionTimeout       float64
		TryWebSockets           bool
		ProtocolVersion         string
		TransportConnectTimeout float64
		LongPollDelay           float64
	}{}
	if err := json.Unmarshal(data, &parsed); err != nil {
		return err
	}

	// Set the connection token and ID.
	c.ConnectionToken = parsed.ConnectionToken
	c.ConnectionID = parsed.ConnectionID

	// Update the protocol version.
	c.Protocol = parsed.ProtocolVersion

	// Set the SignalR endpoint.
	c.Endpoint = parsed.URL

	return nil
}

// Connect implements the connect step of the SignalR connection sequence.
func (c *Client) Connect(ctx context.Context) (*websocket.Conn, error) {
	// Example connect URL:
	// https://socket.bittrex.com/signalr/connect?
	//   transport=webSockets&
	//   clientProtocol=1.5&
	//   connectionToken=<token>&
	//   connectionData=%5B%7B%22name%22%3A%22corehub%22%7D%5D&
	//   tid=5
	// -> returns connection ID. (e.g.: d-F2577E41-B,0|If60z,0|If600,1)

	// Create the URL.
	u := makeURL("connect", c)

	// Perform the connection.
	conn, err := c.xconnect(ctx, u.String(), false)
	if err != nil {
		return nil, fmt.Errorf("xconnect failed: %w", err)
	}

	return conn, nil
}

// Reconnect implements the reconnect step of the SignalR connection sequence.
func (c *Client) Reconnect(ctx context.Context) (*websocket.Conn, error) {
	// Note from
	// https://blog.3d-logic.com/2015/03/29/signalr-on-the-wire-an-informal-description-of-the-signalr-protocol/
	// Once the channel is set up there are no further HTTP requests until
	// the client is stopped (the abort request) or the connection was lost
	// and the client tries to re-establish the connection (the reconnect
	// request).

	// Example reconnect URL:
	// https://socket.bittrex.com/signalr/reconnect?
	//   transport=webSockets&
	//   messageId=d-F2577E41-B%2C0%7CIf60z%2C0%7CIf600%2C1&
	//   clientProtocol=1.5&
	//   connectionToken=<same-token-as-above>&
	//   connectionData=%5B%7B%22name%22%3A%22corehub%22%7D%5D&
	//   tid=7
	// Note: messageId matches connection ID returned from the connect request

	// Create the URL.
	u := makeURL("reconnect", c)

	// Perform the reconnection.
	conn, err := c.xconnect(ctx, u.String(), true)
	if err != nil {
		return nil, fmt.Errorf("xconnect failed: %w", err)
	}

	// Once complete, set the new connection for this client.
	c.SetConn(conn)

	return conn, nil
}

func (c *Client) xconnect(ctx context.Context, urlStr string, isReconnect bool) (*websocket.Conn, error) {
	// Prepare to use the existing HTTP client's cookie jar, if an HTTP client
	// has been set.
	var jar http.CookieJar
	if c.HTTPClient != nil {
		jar = c.HTTPClient.Jar
	}

	// Set the proxy to use the value from the environment by default.
	proxy := http.ProxyFromEnvironment

	// Check to see if the HTTP client transport is defined.
	if t, ok := c.HTTPClient.Transport.(*http.Transport); ok {
		// If the client is an HTTP client, then it will have a proxy defined.
		// By default, this is set to http.ProxyFromEnvironment. If it is not
		// that function, then it will be some other valid function; otherwise
		// the code won't compile. Therefore, we choose to use that function as
		// our proxy.
		//
		// For details about the default value of the proxy, see here:
		// https://github.com/golang/go/blob/cf4691650c3c556f19844a881a32792a919ee8d1/src/net/http/transport.go#L43
		proxy = t.Proxy
	}

	// Create a dialer that uses the supplied TLS client configuration.
	dialer := &websocket.Dialer{
		Proxy:           proxy,
		TLSClientConfig: c.TLSClientConfig,
		Jar:             jar,
	}

	// Prepare a header to be used when dialing to the service.
	header := makeHeader(c)

	retryCount := c.MaxConnectRetries
	if isReconnect {
		retryCount = c.MaxReconnectRetries
	}

	// Perform the connection in a retry loop.
	var conn *websocket.Conn
	var err error
	for i := 0; i < retryCount; i++ {
		var resp *http.Response
		//nolint:bodyclose
		conn, resp, err = dialer.DialContext(ctx, urlStr, header)
		// Handle any specific errors.
		switch {
		case err == nil:
			break
		case errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded):
			return nil, err
		case resp == nil:
			err = fmt.Errorf("empty response, retry %d: %w", i, err)
			time.Sleep(c.RetryWaitDuration)
			continue
		default:
			// Return in the event that no specific error was
			// encountered.
			return nil, fmt.Errorf("%v, retry %d: %w", resp.Status, i, err)
		}
	}

	return conn, err
}

// Start implements the start step of the SignalR connection sequence.
func (c *Client) Start(ctx context.Context, conn WebsocketConn) (err error) {
	if conn == nil {
		return errors.New("connection is nil")
	}

	u := makeURL("start", c)

	var req *http.Request
	req, err = prepareRequest(ctx, u.String(), c.Headers)
	if err != nil {
		return fmt.Errorf("failed to prepare request: %w", err)
	}

	// Perform the request in a retry loop.
	var resp *http.Response
	for i := 0; i < c.MaxStartRetries; i++ {
		resp, err = c.HTTPClient.Do(req)

		// Exit the retry loop if the request was successful.
		if err == nil {
			break
		}

		// If the request was unsuccessful, wrap the error, sleep, and
		// then retry.
		err = fmt.Errorf("request failed (%d): %w", i, err)

		// Wait and retry.
		time.Sleep(c.RetryWaitDuration)
	}

	// If an error occurred on the last retry, then return.
	if err != nil {
		return fmt.Errorf("all request retries failed: %w", err)
	}

	defer func() {
		if cerr := resp.Body.Close(); cerr != nil {
			err = cerr
		}
	}()

	var data []byte
	data, err = ioutil.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read failed: %w", err)
	}

	return c.processStartResponse(data, conn)
}

func (c *Client) processStartResponse(data []byte, conn WebsocketConn) error {
	// Create an anonymous struct to parse the response.
	parsed := struct{ Response string }{}
	if err := json.Unmarshal(data, &parsed); err != nil {
		return err
	}

	// Confirm the server response is what we expect.
	if parsed.Response != "started" {
		return fmt.Errorf("start response is not \"started\": %q", parsed.Response)
	}

	// Wait for the init message.
	t, p, err := conn.ReadMessage()
	if err != nil {
		return fmt.Errorf("message read failed: %w", err)
	}

	// Verify the correct response type was received.
	if t != websocket.TextMessage {
		return fmt.Errorf("unexpected websocket control type: %d", t)
	}

	// Extract the server message.
	var msg Message
	err = json.Unmarshal(p, &msg)
	if err != nil {
		return fmt.Errorf("json unmarshal failed: %w", err)
	}

	serverInitialized := 1
	if msg.S != serverInitialized {
		return fmt.Errorf("unexpected S value received from server: %d | message: %s", msg.S, string(p))
	}

	if msg.G != "" {
		c.GroupsToken.Store(msg.G)
	}

	if msg.C != "" {
		c.MessageID.Store(msg.C)
	}

	// Since we got to this point, the connection is successful. So we set
	// the connection for the client.
	c.conn = conn
	return nil
}

// Run connects to the host and performs the websocket initialization routines
// that are part of the SignalR specification.
//
// This call will terminate when context is canceled or unrecoverable error occurs.
func (c *Client) Run(ctx context.Context, msgHandler MsgHandler) error {
	if err := c.Negotiate(ctx); err != nil {
		return fmt.Errorf("negotiate failed: %w", err)
	}

	conn, err := c.Connect(ctx)
	if err != nil {
		return fmt.Errorf("connect failed: %w", err)
	}

	err = c.Start(ctx, conn)
	if err != nil {
		return fmt.Errorf("start failed: %w", err)
	}

	return c.ReadMessages(ctx, msgHandler)
}

func (c *Client) ReadMessages(ctx context.Context, msgHandler MsgHandler) error {
	for {
		err := c.readMessage(ctx, msgHandler)
		switch {
		case errors.Is(err, context.Canceled):
			return nil
		case err != nil:
			return err
		}
	}
}

func (c *Client) readMessage(ctx context.Context, msgHandler MsgHandler) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	c.connMux.Lock()
	_, data, err := c.conn.ReadMessage()
	c.connMux.Unlock()

	switch {
	case websocket.IsCloseError(err, 1000, 1001, 1006):
		if err := c.attemptReconnect(ctx); err != nil {
			return err
		}
	case err != nil:
		return err
	case bytes.Equal(data, []byte("{}")):
		return nil
	}

	var msg Message
	if err := c.parseMessage(data, &msg); err != nil {
		return err
	}

	return msgHandler(ctx, msg)
}

func (c *Client) parseMessage(data []byte, msg *Message) error {
	if err := json.Unmarshal(data, &msg); err != nil {
		return err
	}

	// Update the groups token.
	if msg.G != "" {
		c.GroupsToken.Store(msg.G)
	}

	// Update the current message ID.
	if msg.C != "" {
		c.MessageID.Store(msg.C)
	}

	return nil
}

func (c *Client) attemptReconnect(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, c.MaxReconnectAttemptDuration)
	defer cancel()

	// Attempt to reconnect in a retry loop.
	var err error
	for i := 0; i < c.MaxReconnectRetries; i++ {
		debugMessage("%sattempting to reconnect...", prefixedID(c.CustomID))

		_, err = c.Reconnect(ctx)
		switch {
		case errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded):
			return err
		case err == nil:
			debugMessage("%sreconnected successfully", prefixedID(c.CustomID))
			return nil
		}
	}

	return err
}

// Send sends a message to the websocket connection.
func (c *Client) Send(m hubs.ClientMsg) error {
	c.connMux.Lock()
	defer c.connMux.Unlock()

	// Verify a connection has been created.
	if c.conn == nil {
		return ErrConnectionNotSet
	}

	// Write the message.
	err := c.conn.WriteJSON(m)
	if err != nil {
		return err
	}

	return nil
}

// SetConn changes the underlying websocket connection to the specified
// connection. This is done using a mutex to wait until existing read operations
// have completed.
func (c *Client) SetConn(conn WebsocketConn) {
	c.connMux.Lock()
	defer c.connMux.Unlock()
	c.conn = conn
}

// Conn returns the underlying websocket connection.
func (c *Client) Conn() WebsocketConn {
	c.connMux.Lock()
	defer c.connMux.Unlock()
	return c.conn
}

// New creates and initializes a SignalR client.
func New(host, protocol, endpoint, connectionData string, params map[string]string) *Client {
	httpClient := &http.Client{
		Transport: http.DefaultTransport,
	}

	return &Client{
		Host:                        host,
		Protocol:                    protocol,
		Endpoint:                    endpoint,
		ConnectionData:              connectionData,
		HTTPClient:                  httpClient,
		Headers:                     make(map[string]string),
		Params:                      params,
		Scheme:                      HTTPS,
		MaxNegotiateRetries:         5,
		MaxConnectRetries:           5,
		MaxReconnectRetries:         5,
		MaxStartRetries:             5,
		RetryWaitDuration:           1 * time.Minute,
		MaxReconnectAttemptDuration: 5 * time.Minute,
	}
}

// Scheme represents a type of transport scheme. For the purposes of this
// project, we only provide constants for schemes relevant to HTTP and
// websockets.
type Scheme string

const (
	// HTTPS is the literal string, "https".
	HTTPS Scheme = "https"

	// HTTP is the literal string, "http".
	HTTP Scheme = "http"

	// WSS is the literal string, "wss".
	WSS Scheme = "wss"

	// WS is the literal string, "ws".
	WS Scheme = "ws"
)

// WebsocketConn is a combination of MessageReader and JSONWriter. It is used to
// provide an interface to objects that can read from and write to a websocket
// connection.
type WebsocketConn interface {
	// ReadMessage is modeled after the function defined at
	// https://godoc.org/github.com/gorilla/websocket#Conn.ReadMessage
	//
	// At a high level, it reads messages and returns:
	//  - the type of message read
	//  - the bytes that were read
	//  - any errors encountered during reading the message
	ReadMessage() (messageType int, p []byte, err error)

	// WriteJSON is modeled after the function defined at
	// https://godoc.org/github.com/gorilla/websocket#Conn.WriteJSON
	//
	// At a high level, it writes a structure to the underlying websocket and
	// returns any error that was encountered during the write operation.
	WriteJSON(v interface{}) error
}

// Message represents a message sent from the server to the persistent websocket
// connection.
type Message struct {
	// message id, present for all non-KeepAlive messages
	C string

	// an array containing actual data
	M []hubs.ClientMsg

	// indicates that the transport was initialized (a.k.a. init message)
	S int

	// groups token – an encrypted string representing group membership
	G string

	// other miscellaneous variables that sometimes are sent by the server
	I string
	E string
	R json.RawMessage
	H json.RawMessage // could be bool or string depending on a message type
	D json.RawMessage
	T json.RawMessage
}

// MsgHandler processes a Message.
type MsgHandler func(ctx context.Context, msg Message) error

// Conditionally encrypt the traffic depending on the initial
// connection's encryption.
func setURLScheme(u *url.URL, httpScheme Scheme) {
	if httpScheme == HTTPS {
		u.Scheme = string(WSS)
	} else {
		u.Scheme = string(WS)
	}
}

func makeURL(command string, c *Client) url.URL {
	var u url.URL

	// Set the host.
	u.Host = c.Host

	// Set the first part of the path.
	u.Path = c.Endpoint

	// Create parameters.
	params := url.Values{}

	// Add shared parameters.
	params.Set("connectionData", c.ConnectionData)
	params.Set("clientProtocol", c.Protocol)

	// Add custom user-supplied parameters.
	for k, v := range c.Params {
		params.Set(k, v)
	}

	// Set the connectionToken.
	if c.ConnectionToken != "" {
		params.Set("connectionToken", c.ConnectionToken)
	}

	connectAdjustments := func() {
		setURLScheme(&u, c.Scheme)
		params.Set("transport", "webSockets")
		tid, _ := rand.Int(rand.Reader, big.NewInt(1000000))
		params.Set("tid", tid.String())
	}

	switch command {
	case "negotiate":
		u.Scheme = string(c.Scheme)
		u.Path += "/negotiate"
	case "connect":
		connectAdjustments()
		u.Path += "/connect"
	case "reconnect":
		connectAdjustments()

		if groupsToken := c.GroupsToken.Load(); groupsToken != "" {
			params.Set("groupsToken", groupsToken)
		}
		if messageID := c.MessageID.Load(); messageID != "" {
			params.Set("messageId", messageID)
		}
		u.Path += "/reconnect"
	case "start":
		u.Scheme = string(c.Scheme)
		params.Set("transport", "webSockets")
		u.Path += "/start"
	}

	// Set the parameters.
	u.RawQuery = params.Encode()

	return u
}

func prepareRequest(ctx context.Context, urlStr string, headers map[string]string) (*http.Request, error) {
	// Make the GET request object.
	req, err := http.NewRequestWithContext(ctx, "GET", urlStr, nil)
	if err != nil {
		err = fmt.Errorf("get request creation failed")
		return nil, err
	}

	// Add all header values.
	for k, v := range headers {
		req.Header.Add(k, v)
	}

	return req, nil
}

func makeHeader(c *Client) http.Header {
	// Create a header object that contains any cookies that have been set
	// in prior requests.
	header := make(http.Header)

	// If no client is specified, return an empty header.
	if c == nil {
		return http.Header{}
	}

	// Add cookies if they are set.
	if c.HTTPClient != nil && c.HTTPClient.Jar != nil {
		// Make a negotiate URL so we can look up the cookie that was
		// set on the negotiate request.
		nu := makeURL("negotiate", c)
		cookies := ""
		for _, v := range c.HTTPClient.Jar.Cookies(&nu) {
			if cookies == "" {
				cookies += v.Name + "=" + v.Value
			} else {
				cookies += "; " + v.Name + "=" + v.Value
			}
		}

		if cookies != "" {
			header.Add("Cookie", cookies)
		}
	}

	// Add all the other header values specified by the user.
	for k, v := range c.Headers {
		header.Add(k, v)
	}

	return header
}

func debugEnabled() bool {
	v := os.Getenv("DEBUG")
	return v != ""
}

func debugMessage(msg string, v ...interface{}) {
	if debugEnabled() {
		log.Printf(msg, v...)
	}
}

func prefixedID(id string) string {
	if id == "" {
		return ""
	}

	return "[" + id + "] "
}
