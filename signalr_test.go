package signalr

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cenkalti/backoff/v4"
	"github.com/gorilla/websocket"
	"golang.org/x/sync/errgroup"
)

var (
	connectionData  = "connection-data"
	connectionToken = "connection-token"
	connectionID    = "connection-id"
	protocolVersion = "1337"
	groupsToken     = "42"
	retryInterval   = 5 * time.Millisecond
)

func TestClient(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		server      func(http.Handler) *httptest.Server
		handler     testHandlerFunc
		expectedErr error
	}{
		{
			name: "http client",
		},
		{
			name:   "https client",
			server: httptest.NewTLSServer,
		},
		{
			name:        "negotiate failure",
			handler:     errorResponse(404, "/negotiate"),
			expectedErr: &NegotiateError{},
		},
		{
			name:        "connect failure",
			handler:     errorResponse(404, "/connect"),
			expectedErr: &ConnectError{},
		},
		{
			name:        "start failure",
			handler:     errorResponse(404, "/start"),
			expectedErr: &StartError{},
		},
	}

	for _, tc := range tests {
		tc := tc

		t.Run(tc.name, func(t *testing.T) {
			server := tc.server
			if server == nil {
				server = httptest.NewServer
			}

			handler := tc.handler
			if handler == nil {
				handler = newRootHandler()
			}

			ts := server(wrapHandler(t, handler))
			t.Cleanup(ts.Close)

			ctx := context.Background()

			c, err := Dial(ctx, ts.URL, connectionData, HTTPClient(ts.Client()), Protocol(protocolVersion), RetryInterval(retryInterval))

			if tc.expectedErr != nil {
				expectErrorMatch(t, tc.expectedErr, err)
				return
			}

			if !expectNoError(t, err) {
				return
			}

			t.Cleanup(func() {
				err := c.Close()
				if err != nil {
					t.Errorf("close failed: %v", err)
				}
			})

			expectState(t, State{
				ConnectionData:  connectionData,
				ConnectionToken: connectionToken,
				ConnectionID:    connectionID,
				Protocol:        protocolVersion,
			}, *c.State())

			var msg Message
			if expectNoError(t, c.ReadMessage(ctx, &msg)) {
				return
			}

			expectState(t, State{
				ConnectionData:  connectionData,
				ConnectionToken: connectionToken,
				ConnectionID:    connectionID,
				Protocol:        protocolVersion,
				GroupsToken:     groupsToken,
				MessageID:       "0",
			}, *c.State())
		})
	}
}

func TestReadMessage(t *testing.T) {
	t.Parallel()

	initMessage := readResult{msg: `{"S":1}`}

	cases := []struct {
		name        string
		dialResults []dialResult
		readResults []readResult
		retries     int
		expectedMsg Message
		expectedErr error
	}{
		{
			name:        "normal message",
			readResults: []readResult{{msg: `{"C":"test message"}`}},
			expectedMsg: Message{C: "test message"},
		},
		{
			name:        "groups token",
			readResults: []readResult{{msg: `{"C":"test message","G":"custom-groups-token"}`}},
			expectedMsg: Message{C: "test message", G: "custom-groups-token"},
		},
		{
			name: "recover after websocket closed",
			readResults: []readResult{
				{err: &CloseError{code: 1001}},
				{msg: `{"C":"test message"}`},
			},
			expectedMsg: Message{C: "test message"},
		},
		{
			name: "reconnect failed",
			readResults: []readResult{
				{err: &CloseError{code: 1001}},
				{msg: `{"C":"test message"}`},
			},
			dialResults: []dialResult{
				{err: io.EOF},
			},
			expectedErr: &ConnectError{},
		},
		{
			name:        "read failed",
			readResults: []readResult{{err: io.EOF}},
			expectedErr: &ReadError{},
		},
		{
			name:        "empty message",
			readResults: []readResult{{msg: ""}},
			expectedErr: &json.SyntaxError{},
		},
		{
			name:        "bad json",
			readResults: []readResult{{msg: "{invalid json"}},
			expectedErr: &json.SyntaxError{},
		},
	}

	for _, tc := range cases {
		tc := tc

		t.Run(tc.name, func(t *testing.T) {
			ts := httptest.NewServer(wrapHandler(t, newRootHandler()))
			t.Cleanup(ts.Close)

			readResults := append([]readResult{initMessage}, tc.readResults...)
			conn := &fakeConn{results: readResults}

			dialResults := append([]dialResult{{conn: conn}}, tc.dialResults...)
			dialer := func(*http.Client) WebsocketDialer {
				return &mockDialer{conn: conn, results: dialResults}
			}

			ctx := context.Background()
			c, err := Dial(ctx, ts.URL, connectionData, Dialer(dialer), RetryInterval(retryInterval), MaxReconnectRetries(tc.retries))

			if !expectNoError(t, err) {
				return
			}

			var msg Message
			err = c.ReadMessage(ctx, &msg)

			if tc.expectedErr != nil {
				expectErrorMatch(t, tc.expectedErr, err)
				return
			}

			expectNoError(t, err)
			expectState(t, State{
				ConnectionData:  connectionData,
				ConnectionID:    connectionID,
				ConnectionToken: connectionToken,
				Protocol:        protocolVersion,
				GroupsToken:     tc.expectedMsg.G,
				MessageID:       tc.expectedMsg.C,
			}, *c.State())
			expectMessage(t, tc.expectedMsg, msg)
		})
	}
}

func TestNegotiate(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name        string
		endpoint    string
		retries     int
		handler     testHandlerFunc
		headers     http.Header
		expectedErr error
	}{
		{
			name: "successful negotiate",
		},
		{
			name:    "recover after failure",
			handler: errorResponseOnce(503),
			retries: 1,
		},
		{
			name:        "invalid scheme",
			endpoint:    "ssh://invalid-endpoint",
			expectedErr: &url.Error{},
		},
		{
			name:        "503 error",
			handler:     errorResponse(503),
			expectedErr: &url.Error{},
		},
		{
			name:        "failed get request",
			handler:     timeout(2 * retryInterval),
			expectedErr: &ReadError{},
		},
		{
			name:        "invalid json",
			handler:     response("invalid json"),
			expectedErr: &json.SyntaxError{},
		},
	}

	for _, tc := range cases {
		tc := tc

		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			t.Cleanup(cancel)

			handler := tc.handler
			if handler == nil {
				handler = newRootHandler()
			}

			// Create a test server.
			ts := httptest.NewServer(wrapHandler(t, handler))
			t.Cleanup(ts.Close)

			endpoint := tc.endpoint
			if endpoint == "" {
				endpoint = ts.URL
			}

			state := State{
				ConnectionData: connectionData,
				Protocol:       protocolVersion,
			}

			bo := backoff.WithMaxRetries(backoff.NewConstantBackOff(retryInterval), uint64(tc.retries))
			err := negotiate(ctx, ts.Client(), endpoint, tc.headers, &state, bo)

			if tc.expectedErr != nil {
				expectErrorMatch(t, tc.expectedErr, err)
				return
			}

			expectNoError(t, err)
			expectState(t, State{
				ConnectionData:  connectionData,
				ConnectionID:    connectionID,
				ConnectionToken: connectionToken,
				Protocol:        protocolVersion,
			}, state)
		})
	}
}

func TestConnect(t *testing.T) {
	t.Parallel()

	endpoint := "http://fake-endpoint"
	headers := make(http.Header)

	notFoundResult := dialResult{status: http.StatusNotFound, err: websocket.ErrBadHandshake}
	eofResult := dialResult{err: io.EOF}
	validResult := dialResult{conn: &fakeConn{}}

	cases := []struct {
		name        string
		dialResults []dialResult
		retries     int
		expectedErr error
	}{
		{
			name:        "successful reconnect",
			dialResults: []dialResult{notFoundResult, eofResult, validResult},
			retries:     5,
		},
		{
			name:        "unsuccessful reconnect with http status",
			dialResults: []dialResult{notFoundResult},
			expectedErr: &DialError{},
		},
		{
			name:        "unsuccessful reconnect with other error",
			dialResults: []dialResult{eofResult},
			expectedErr: &DialError{},
		},
	}

	for _, tc := range cases {
		tc := tc

		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			bo := backoff.WithMaxRetries(backoff.NewConstantBackOff(1*time.Millisecond), uint64(tc.retries))

			for _, command := range []string{"connect", "reconnect"} {
				dialer := &mockDialer{results: tc.dialResults}

				state := State{
					ConnectionData: connectionData,
					Protocol:       protocolVersion,
				}

				conn, err := connect(ctx, dialer, endpoint, command, headers, &state, bo)

				if tc.expectedErr != nil {
					expectErrorMatch(t, tc.expectedErr, err)
					return
				}

				if conn == nil {
					t.Error("expected non-nil conn")
				}
			}
		})
	}
}

func TestStart(t *testing.T) {
	t.Parallel()

	headers := make(http.Header)
	initMessage := readResult{msgType: 1, msg: `{"S":1}`}

	cases := []struct {
		name        string
		handler     testHandlerFunc
		readResult  readResult
		retries     int
		expectedErr error
	}{
		{
			name:       "successful start",
			readResult: initMessage,
		},
		{
			name:       "recover after failure",
			handler:    errorResponseOnce(503),
			retries:    1,
			readResult: initMessage,
		},
		{
			name:        "request failed with http status",
			handler:     errorResponse(503),
			expectedErr: &url.Error{},
		},
		{
			name:        "request timed out",
			handler:     timeout(2 * retryInterval),
			expectedErr: &ReadError{},
		},
		{
			name:        "invalid json",
			handler:     response("invalid json"),
			expectedErr: &json.SyntaxError{},
		},
		{
			name:        "non-started response 1",
			handler:     response(`{"hello":"world"}`),
			expectedErr: &InvalidStartResponseError{},
		},
		{
			name:        "non-stared response 2",
			handler:     response(`{"Response":"blabla"}`),
			expectedErr: &InvalidStartResponseError{},
		},
		{
			name:        "read error",
			readResult:  readResult{err: io.EOF},
			expectedErr: &ReadError{},
		},
		{
			name:        "wrong message type",
			handler:     response(`{"Response":"started"}`),
			readResult:  readResult{msgType: 9001},
			expectedErr: &ReadError{},
		},
		{
			name:        "message json unmarshal failure",
			handler:     response(`{"Response":"started"}`),
			readResult:  readResult{msg: "invalid json"},
			expectedErr: &json.SyntaxError{},
		},
		{
			name:        "server not initialized",
			handler:     response(`{"Response":"started"}`),
			readResult:  readResult{msg: `{"S":9002}`},
			expectedErr: &InvalidInitMessageError{},
		},
	}

	for _, tc := range cases {
		tc := tc

		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			t.Cleanup(cancel)

			handler := tc.handler
			if handler == nil {
				handler = newRootHandler()
			}

			// Create a test server.
			ts := httptest.NewServer(wrapHandler(t, handler))
			t.Cleanup(ts.Close)

			conn := &fakeConn{results: []readResult{tc.readResult}}

			state := State{
				ConnectionData:  connectionData,
				ConnectionID:    connectionID,
				ConnectionToken: connectionToken,
				Protocol:        protocolVersion,
			}

			bo := backoff.WithMaxRetries(backoff.NewConstantBackOff(retryInterval), uint64(tc.retries))
			err := start(ctx, ts.Client(), conn, ts.URL, headers, &state, bo)

			if tc.expectedErr != nil {
				expectErrorMatch(t, tc.expectedErr, err)
				return
			}

			expectNoError(t, err)
			expectState(t, State{
				ConnectionData:  connectionData,
				ConnectionID:    connectionID,
				ConnectionToken: connectionToken,
				Protocol:        protocolVersion,
			}, state)
		})
	}
}

func TestPrepareRequest(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	cases := []struct {
		name        string
		url         string
		headers     http.Header
		expectedErr error
	}{
		{
			name: "simple request with no headers",
			url:  "http://example.org",
		},
		{
			name: "complex request with headers",
			url:  "https://example.org/custom/path?param=123",
			headers: http.Header{
				"header1": []string{"value1"},
				"header2": []string{"value1", "value2"},
			},
		},
		{
			name:        "invalid URL",
			url:         ":",
			expectedErr: &url.Error{},
		},
	}

	for _, tc := range cases {
		tc := tc

		t.Run(tc.name, func(t *testing.T) {
			req, err := prepareRequest(ctx, tc.url, tc.headers)

			if tc.expectedErr != nil {
				expectErrorMatch(t, tc.expectedErr, err)
				return
			}

			if !expectNoError(t, err) {
				return
			}

			if tc.url != req.URL.String() {
				t.Errorf("expected request url %q, got %q", tc.url, req.URL)
			}

			if !reflect.DeepEqual(tc.headers, req.Header) {
				t.Errorf("expected request headers %+v, got %+v", tc.headers, req.Header)
			}
		})
	}
}

type mockDialer struct {
	conn    WebsocketConn
	results []dialResult
}

type dialResult struct {
	conn   WebsocketConn
	status int
	err    error
}

func (d *mockDialer) Dial(ctx context.Context, endpoint string, headers http.Header) (conn WebsocketConn, status int, err error) {
	switch {
	case len(d.results) == 0 && d.conn != nil:
		return d.conn, 0, nil
	case len(d.results) == 0:
		return &fakeConn{}, 0, nil
	}

	r := d.results[0]
	d.results = d.results[1:]

	return r.conn, r.status, r.err
}

type fakeConn struct {
	msg     string
	results []readResult
}

type readResult struct {
	msgType int
	msg     string
	err     error
}

func (c *fakeConn) ReadMessage() (msgType int, p []byte, err error) {
	if len(c.results) == 0 {
		return 0, []byte(c.msg), nil
	}

	r := c.results[0]
	c.results = c.results[1:]

	msgType = textMessage
	if r.msgType != 0 {
		msgType = r.msgType
	}

	if r.msg != "" {
		p = []byte(r.msg)
	}

	return msgType, p, r.err
}

func (c *fakeConn) WriteMessage(int, []byte) (err error) {
	return
}

func (c *fakeConn) Close() error {
	return nil
}

type testHandlerFunc func(testing.TB, http.ResponseWriter, *http.Request)

func wrapHandler(t testing.TB, handler testHandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		handler(t, w, r)
	}
}

type rootHandler struct {
	mtx     sync.Mutex
	conn    WebsocketConn
	started bool
}

func newRootHandler() testHandlerFunc {
	handler := rootHandler{}
	return handler.ServeHTTP
}

func (h *rootHandler) ServeHTTP(t testing.TB, w http.ResponseWriter, r *http.Request) {
	switch {
	case strings.Contains(r.URL.Path, "/negotiate"):
		h.negotiate(t, w, r)
	case strings.Contains(r.URL.Path, "/connect"):
		h.connect(t, w, r)
	case strings.Contains(r.URL.Path, "/reconnect"):
		h.connect(t, w, r)
	case strings.Contains(r.URL.Path, "/start"):
		h.start(t, w, r)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func (h *rootHandler) negotiate(t testing.TB, w http.ResponseWriter, _ *http.Request) {
	data, err := json.Marshal(negotiateResponse{
		ConnectionToken: connectionToken,
		ConnectionID:    connectionID,
		ProtocolVersion: protocolVersion,
	})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := w.Write(data); err != nil {
		t.Fatal(err)
	}
}

func (h *rootHandler) connect(t testing.TB, w http.ResponseWriter, req *http.Request) {
	upgrader := websocket.Upgrader{}

	var err error
	conn, err := upgrader.Upgrade(w, req, nil)
	if err != nil {
		t.Fatal(err)
	}

	h.mtx.Lock()
	h.conn = conn
	h.mtx.Unlock()

	errg, ctx := errgroup.WithContext(req.Context())
	errg.Go(func() error {
		for {
			select {
			case <-ctx.Done():
				return nil
			default:
			}

			if _, _, err := conn.ReadMessage(); err != nil {
				return err
			}
		}
	})
	errg.Go(func() error {
		var messageID int
		for {
			select {
			case <-ctx.Done():
				return nil
			default:
			}

			h.mtx.Lock()
			started := h.started
			h.mtx.Unlock()

			if !started {
				continue
			}

			err := h.writeMessage(Message{
				G: groupsToken,
				C: strconv.FormatInt(int64(messageID), 10),
			})
			if err != nil {
				return err
			}

			messageID++
		}
	})

	_ = errg.Wait()
}

func (h *rootHandler) start(t testing.TB, w http.ResponseWriter, _ *http.Request) {
	h.mtx.Lock()
	h.started = false
	conn := h.conn
	h.mtx.Unlock()

	data, err := json.Marshal(startResponse{
		Response: "started",
	})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := w.Write(data); err != nil {
		t.Fatal(err)
	}

	if conn == nil {
		return
	}

	if err := h.writeMessage(Message{S: statusStarted}); err != nil {
		t.Fatal(err)
	}

	h.mtx.Lock()
	h.started = true
	h.mtx.Unlock()
}

func (h *rootHandler) writeMessage(msg Message) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}

	h.mtx.Lock()
	defer h.mtx.Unlock()

	return h.conn.WriteMessage(textMessage, data)
}

func errorResponse(status int, paths ...string) testHandlerFunc {
	handler := newRootHandler()

	return func(t testing.TB, w http.ResponseWriter, req *http.Request) {
		if !pathMatches(req, paths) {
			handler(t, w, req)
			return
		}

		w.WriteHeader(status)
	}
}

func errorResponseOnce(status int, paths ...string) testHandlerFunc {
	var done int64

	errorHandler := errorResponse(status, paths...)
	handler := newRootHandler()

	return func(t testing.TB, w http.ResponseWriter, req *http.Request) {
		if !pathMatches(req, paths) || !atomic.CompareAndSwapInt64(&done, 0, 1) {
			handler(t, w, req)
			return
		}

		errorHandler(t, w, req)
	}
}

//nolint:unparam
func response(response string, paths ...string) testHandlerFunc {
	handler := newRootHandler()

	return func(t testing.TB, w http.ResponseWriter, req *http.Request) {
		if !pathMatches(req, paths) {
			handler(t, w, req)
			return
		}

		_, err := w.Write([]byte(response))
		if err != nil {
			t.Fatal(err)
		}
	}
}

func timeout(timeout time.Duration, paths ...string) testHandlerFunc {
	handler := newRootHandler()

	return func(t testing.TB, w http.ResponseWriter, req *http.Request) {
		if !pathMatches(req, paths) {
			handler(t, w, req)
			return
		}

		time.Sleep(timeout)
	}
}

func pathMatches(req *http.Request, paths []string) bool {
	if len(paths) == 0 {
		return true
	}

	for _, path := range paths {
		if strings.HasPrefix(req.URL.Path, path) {
			return true
		}
	}

	return false
}

func expectErrorMatch(t testing.TB, expected, actual error) {
	t.Helper()

	if !errors.As(actual, &expected) {
		t.Errorf("expected error %+v, got: %+v", expected, actual)
	}
}

func expectNoError(t testing.TB, err error) bool {
	t.Helper()

	if err != nil {
		t.Errorf("unexpected error: %+v", err)
		return false
	}

	return true
}

func expectMessage(t testing.TB, expected, actual Message) {
	t.Helper()

	if !reflect.DeepEqual(expected, actual) {
		t.Errorf("expected message %+v, got: %+v", expected, actual)
	}
}

func expectState(t testing.TB, expected, actual State) {
	t.Helper()

	if !reflect.DeepEqual(expected, actual) {
		t.Errorf("expected state %+v, got: %+v", expected, actual)
	}
}
