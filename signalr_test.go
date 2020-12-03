package signalr

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cenkalti/backoff/v4"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"golang.org/x/sync/errgroup"
)

var (
	connectionData  = "connection-data"
	connectionToken = "connection-token"
	connectionID    = "connection-id"
	protocolVersion = "1337"
	groupsToken     = "42"
)

func TestClient(t *testing.T) {
	t.Parallel()

	// Create a test server.
	ts := httptest.NewServer(wrapHandler(t, newRootHandler(nil)))
	t.Cleanup(ts.Close)

	ctx := context.Background()

	c, err := Dial(ctx, ts.URL, connectionData, Protocol(protocolVersion))
	if !assert.NoError(t, err) {
		return
	}

	assert.Equal(t, &State{
		ConnectionData:  connectionData,
		ConnectionToken: connectionToken,
		ConnectionID:    connectionID,
		Protocol:        protocolVersion,
	}, c.State())

	var msg Message
	if !assert.NoError(t, c.ReadMessage(ctx, &msg)) {
		return
	}

	assert.Equal(t, &State{
		ConnectionData:  connectionData,
		ConnectionToken: connectionToken,
		ConnectionID:    connectionID,
		Protocol:        protocolVersion,
		GroupsToken:     groupsToken,
		MessageID:       "0",
	}, c.State())
}

func TestNegotiate(t *testing.T) {
	t.Parallel()

	retryWaitDuration := 5 * time.Millisecond

	cases := []struct {
		name        string
		handler     testHandlerFunc
		headers     http.Header
		expectedErr error
	}{
		{
			name: "successful negotiate",
		},
		{
			name:    "recover after failure",
			handler: errorResponseOnce(503, nil),
		},
		{
			name:        "503 error",
			handler:     errorResponse(503),
			expectedErr: &url.Error{},
		},
		{
			name:        "failed get request",
			handler:     timeoutResponse(2 * retryWaitDuration),
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
				handler = newRootHandler(nil)
			}

			// Create a test server.
			ts := httptest.NewServer(wrapHandler(t, handler))
			t.Cleanup(ts.Close)

			state := State{
				ConnectionData: connectionData,
				Protocol:       protocolVersion,
			}

			bo := backoff.WithMaxRetries(backoff.NewConstantBackOff(retryWaitDuration), 5)
			err := negotiate(ctx, ts.Client(), ts.URL, tc.headers, &state, bo)

			if tc.expectedErr != nil {
				assert.True(t, errors.As(err, &tc.expectedErr))
				return
			}

			assert.NoError(t, err)
			assert.Equal(t, State{
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

	cases := []struct {
		name        string
		retries     int
		expectedErr error
	}{
		{
			name:    "successful reconnect",
			retries: 5,
		},
		{
			name:        "unsuccessful reconnect with http status",
			retries:     0,
			expectedErr: &DialError{},
		},
		{
			name:        "unsuccessful reconnect with other error",
			retries:     1,
			expectedErr: &DialError{},
		},
	}

	for _, tc := range cases {
		tc := tc

		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			bo := backoff.WithMaxRetries(backoff.NewConstantBackOff(1*time.Millisecond), uint64(tc.retries))

			for _, command := range []string{"connect", "reconnect"} {
				expectedEndpoint := fmt.Sprintf("ws://fake-endpoint/%s?clientProtocol=%s&connectionData=%s", command, protocolVersion, connectionData)

				dialer := &mockDialer{}
				dialer.On("Dial", ctx, expectedEndpoint, headers).Return(nil, http.StatusNotFound, websocket.ErrBadHandshake).Once()
				dialer.On("Dial", ctx, expectedEndpoint, headers).Return(nil, 0, io.EOF).Once()
				dialer.On("Dial", ctx, expectedEndpoint, headers).Return(&fakeConn{}, http.StatusOK, nil)

				state := State{
					ConnectionData: connectionData,
					Protocol:       protocolVersion,
				}

				conn, err := connect(ctx, dialer, endpoint, command, make(http.Header), &state, bo)

				if tc.expectedErr != nil {
					assert.True(t, errors.As(err, &tc.expectedErr))
					return
				}

				assert.NoError(t, err)
				assert.NotNil(t, conn)

				assert.NoError(t, conn.Close())
			}
		})
	}
}

func TestStart(t *testing.T) {
	t.Parallel()

	retryWaitDuration := 5 * time.Millisecond
	validConn := &fakeConn{msgType: 1, msg: `{"S":1}`}

	cases := []struct {
		name        string
		handler     testHandlerFunc
		conn        *fakeConn
		headers     http.Header
		expectedErr error
	}{
		{
			name: "successful start",
			conn: validConn,
		},
		{
			name:    "recover after failure",
			handler: errorResponseOnce(503, validConn),
			conn:    validConn,
		},
		{
			name:        "503 error",
			handler:     errorResponse(503),
			expectedErr: &url.Error{},
		},
		{
			name:        "failed get request",
			handler:     timeoutResponse(2 * retryWaitDuration),
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
			name:        "wrong message type",
			handler:     response(`{"Response":"started"}`),
			conn:        &fakeConn{msgType: 9001},
			expectedErr: &ReadError{},
		},
		{
			name:        "message json unmarshal failure",
			handler:     response(`{"Response":"started"}`),
			conn:        &fakeConn{msgType: 1, msg: "invalid json"},
			expectedErr: &json.SyntaxError{},
		},
		{
			name:        "server not initialized",
			handler:     response(`{"Response":"started"}`),
			conn:        &fakeConn{msgType: 1, msg: `{"S":9002}`},
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
				handler = newRootHandler(validConn)
			}

			// Create a test server.
			ts := httptest.NewServer(wrapHandler(t, handler))
			t.Cleanup(ts.Close)

			state := State{
				ConnectionData:  connectionData,
				ConnectionID:    connectionID,
				ConnectionToken: connectionToken,
				Protocol:        protocolVersion,
			}

			bo := backoff.WithMaxRetries(backoff.NewConstantBackOff(retryWaitDuration), 5)
			err := start(ctx, ts.Client(), tc.conn, ts.URL, tc.headers, &state, bo)

			if tc.expectedErr != nil {
				assert.True(t, errors.As(err, &tc.expectedErr))
				return
			}

			assert.NoError(t, err)
			assert.Equal(t, State{
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
				assert.True(t, errors.As(err, &tc.expectedErr))
				return
			}

			if assert.NoError(t, err) {
				assert.Equal(t, tc.url, req.URL.String())
				assert.Equal(t, tc.headers, req.Header)
			}
		})
	}
}

func TestReadMessage(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name          string
		conn          *fakeConn
		expectedMsg   Message
		expectedState State
		expectedErr   error
	}{
		{
			name:          "normal message",
			conn:          &fakeConn{msg: `{"C":"test message"}`},
			expectedMsg:   Message{C: "test message"},
			expectedState: State{MessageID: "test message"},
		},
		{
			name:          "groups token",
			conn:          &fakeConn{msg: `{"C":"test message","G":"custom-groups-token"}`},
			expectedMsg:   Message{C: "test message", G: "custom-groups-token"},
			expectedState: State{MessageID: "test message", GroupsToken: "custom-groups-token"},
		},
		{
			name:        "empty message",
			conn:        &fakeConn{msg: ""},
			expectedErr: &json.SyntaxError{},
		},
		{
			name:        "bad json",
			conn:        &fakeConn{msg: "{invalid json"},
			expectedErr: &json.SyntaxError{},
		},
	}

	for _, tc := range cases {
		tc := tc

		t.Run(tc.name, func(t *testing.T) {
			var (
				msg   Message
				state State
			)
			err := readMessage(tc.conn, &msg, &state)

			if tc.expectedErr != nil {
				assert.True(t, errors.As(err, &tc.expectedErr))
				return
			}

			assert.NoError(t, err)
			assert.Equal(t, tc.expectedMsg, msg)
			assert.Equal(t, tc.expectedState, state)
		})
	}
}

type mockDialer struct {
	mock.Mock
}

func (d *mockDialer) Dial(ctx context.Context, endpoint string, headers http.Header) (conn WebsocketConn, status int, err error) {
	args := d.Called(ctx, endpoint, headers)

	conn, _ = args.Get(0).(WebsocketConn)
	return conn, args.Int(1), args.Error(2)
}

type fakeConn struct {
	errs    []error
	msgType int
	msg     string
}

func (c *fakeConn) ReadMessage() (n int, data []byte, err error) {
	if len(c.errs) != 0 {
		err = c.errs[0]
		c.errs = c.errs[1:]
		return 0, nil, err
	}

	// default to text message type
	msgType := c.msgType
	if msgType == 0 {
		msgType = 1
	}

	return msgType, []byte(c.msg), nil
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

func newRootHandler(conn WebsocketConn) testHandlerFunc {
	handler := rootHandler{conn: conn}
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

	if err := errg.Wait(); err != nil {
		t.Log(err)
		w.WriteHeader(http.StatusInternalServerError)
	}
}

func (h *rootHandler) start(t testing.TB, w http.ResponseWriter, _ *http.Request) {
	h.mtx.Lock()
	h.started = false
	conn := h.conn
	h.mtx.Unlock()

	if conn == nil {
		t.Fatal("connection not set")
	}

	data, err := json.Marshal(startResponse{
		Response: "started",
	})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := w.Write(data); err != nil {
		t.Fatal(err)
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

func errorResponse(status int) testHandlerFunc {
	return func(_ testing.TB, w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
	}
}

func errorResponseOnce(status int, conn WebsocketConn) testHandlerFunc {
	var done int64

	return func(t testing.TB, w http.ResponseWriter, req *http.Request) {
		errorHandler := errorResponse(status)
		handler := newRootHandler(conn)

		if atomic.CompareAndSwapInt64(&done, 0, 1) {
			errorHandler(t, w, req)
			return
		}

		handler(t, w, req)
	}
}

func response(response string) testHandlerFunc {
	return func(t testing.TB, w http.ResponseWriter, _ *http.Request) {
		_, err := w.Write([]byte(response))
		if err != nil {
			t.Fatal(err)
		}
	}
}

func timeoutResponse(timeout time.Duration) testHandlerFunc {
	return func(testing.TB, http.ResponseWriter, *http.Request) {
		time.Sleep(timeout)
	}
}
