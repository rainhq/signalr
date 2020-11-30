package signalr_test

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/gorilla/websocket"
	"github.com/rainhq/signalr/v2"
	"github.com/rainhq/signalr/v2/hubs"
	"github.com/rainhq/signalr/v2/internal/testutil"
)

func ExampleClient_Run() {
	// Prepare a SignalR client.
	c := signalr.New(
		"fake-server.definitely-not-real",
		"1.5",
		"/signalr",
		`[{"name":"awesomehub"}]`,
		nil,
	)

	// Define handlers.
	msgHandler := func(_ context.Context, msg signalr.Message) error {
		log.Println(msg)
		return nil
	}

	ctx := context.Background()

	// Start the run loop.
	if err := c.Run(ctx, msgHandler); err != nil {
		log.Fatal(err)
	}
}

// This example shows how to manually perform each of the initialization steps.
func Example_complex() {
	// Prepare a SignalR client.
	c := signalr.New(
		"fake-server.definitely-not-real",
		"1.5",
		"/signalr",
		`[{"name":"awesomehub"}]`,
		map[string]string{"custom-key": "custom-value"},
	)

	// Perform any optional modifications to the client here. Read the docs for
	// all the available options that are exposed via public fields.

	// Define message and error handlers.
	msgHandler := func(_ context.Context, msg signalr.Message) error {
		log.Println(msg)
		return nil
	}

	ctx := context.Background()

	// Manually perform the initialization routine.
	if err := c.Negotiate(ctx); err != nil {
		log.Fatal(err)
	}

	conn, err := c.Connect(ctx)
	if err != nil {
		log.Fatal(err)
	}

	if err := c.Start(ctx, conn); err != nil {
		log.Fatal(err)
	}

	// Begin the message reading loop.
	if err := c.ReadMessages(ctx, msgHandler); err != nil {
		log.Fatal(err)
	}
}

func equals(t testing.TB, exp, act interface{}, opts ...cmp.Option) {
	t.Helper()

	if exp == nil && act == nil {
		return
	}

	if !cmp.Equal(exp, act, opts...) {
		t.Errorf("unexpected value:\n%s", cmp.Diff(exp, act, opts...))
	}
}

func ok(t testing.TB, err error) bool {
	t.Helper()

	if err != nil {
		t.Errorf("unexpected error: %+v", err)
	}

	return err == nil
}

func notNil(t testing.TB, act interface{}) {
	t.Helper()

	if act == nil {
		t.Errorf("expected non-nil value, got: %v", act)
	}
}

func notEmpty(t testing.TB, act string) {
	t.Helper()

	if act == "" {
		t.Errorf("expected non-empty vale, got: %q", act)
	}
}

func errMatches(t testing.TB, exp, act error) {
	t.Helper()

	if errors.Is(act, exp) || errors.As(act, &exp) {
		return
	}

	if !cmp.Equal(exp, act, cmpopts.EquateErrors()) {
		t.Errorf("invalid error value:\n%s", cmp.Diff(exp, act, cmpopts.EquateErrors()))
	}
}

func hostFromServerURL(urlStr string) (host string) {
	host = strings.TrimPrefix(urlStr, "https://")
	host = strings.TrimPrefix(host, "http://")
	return
}

const (
	serverResponseWriteTimeout = 500 * time.Millisecond
)

func throw503Error(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusServiceUnavailable)
	_, err := w.Write([]byte("503 error"))
	if err != nil {
		log.Panic(err)
	}
}

func throw678Error(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(678)
	_, err := w.Write([]byte("678 error"))
	if err != nil {
		log.Panic(err)
	}
}

func throw404Error(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusNotFound)
	_, err := w.Write([]byte("404 error"))
	if err != nil {
		log.Panic(err)
	}
}

func causeWriteResponseTimeout(w http.ResponseWriter, r *http.Request) {
	time.Sleep(3 * serverResponseWriteTimeout)
}

func TestClientNegotiate(t *testing.T) {
	t.Parallel()

	// Make a requestID available to test cases in the event that multiple
	// requests are sent that should have different responses based on which
	// request is being sent.
	var requestID int
	log.Println(requestID)

	cases := map[string]struct {
		fn       http.HandlerFunc
		in       *signalr.Client
		TLS      bool
		useDebug bool
		exp      *signalr.Client
		scheme   signalr.Scheme
		params   map[string]string
		wantErr  error
	}{
		"successful http": {
			fn: testutil.TestNegotiate,
			in: &signalr.Client{
				Protocol:       "1337",
				Endpoint:       "/signalr",
				ConnectionData: "all the data",
			},
			TLS: false,
			exp: &signalr.Client{
				Protocol:        "1337",
				Endpoint:        "/signalr",
				ConnectionToken: "hello world",
				ConnectionID:    "1234-ABC",
				ConnectionData:  "",
			},
		},
		"successful https": {
			fn: testutil.TestNegotiate,
			in: &signalr.Client{
				Protocol:       "1337",
				Endpoint:       "/signalr",
				ConnectionData: "all the data",
			},
			TLS: true,
			exp: &signalr.Client{
				Protocol:        "1337",
				Endpoint:        "/signalr",
				ConnectionToken: "hello world",
				ConnectionID:    "1234-ABC",
			},
		},
		"503 error": {
			fn:      throw503Error,
			in:      &signalr.Client{},
			exp:     &signalr.Client{},
			wantErr: errors.New("503 Service Unavailable"),
		},
		"default error": {
			fn:      throw678Error,
			in:      &signalr.Client{},
			exp:     &signalr.Client{},
			wantErr: errors.New("678 status code"),
		},
		"failed get request": {
			fn:      causeWriteResponseTimeout,
			in:      &signalr.Client{},
			exp:     &signalr.Client{},
			wantErr: io.EOF,
		},
		"invalid json": {
			fn: func(w http.ResponseWriter, r *http.Request) {
				_, err := w.Write([]byte("invalid json"))
				if err != nil {
					log.Panic(err)
				}
			},
			in:      &signalr.Client{},
			exp:     &signalr.Client{},
			wantErr: &json.SyntaxError{},
		},
		"request preparation failure": {
			fn:      testutil.TestNegotiate,
			in:      &signalr.Client{},
			scheme:  ":",
			exp:     &signalr.Client{},
			wantErr: nil,
		},
		"call debug messages": {
			fn:       throw503Error,
			in:       &signalr.Client{},
			exp:      &signalr.Client{},
			useDebug: true,
			wantErr:  errors.New("503 Service Unavailable"),
		},
		"recover after failure": {
			fn: func(w http.ResponseWriter, r *http.Request) {
				if requestID == 0 {
					throw503Error(w, r)
					requestID++
				} else {
					testutil.TestNegotiate(w, r)
				}
			},
			in: &signalr.Client{
				Protocol:       "1337",
				Endpoint:       "/signalr",
				ConnectionData: "all the data",
			},
			TLS: false,
			exp: &signalr.Client{
				Protocol:        "1337",
				Endpoint:        "/signalr",
				ConnectionToken: "hello world",
				ConnectionID:    "1234-ABC",
				ConnectionData:  "",
			},
		},
		"custom parameters": {
			fn: testutil.TestNegotiate,
			in: &signalr.Client{
				Protocol:       "1337",
				Endpoint:       "/signalr",
				ConnectionData: "all the data",
			},
			TLS:    false,
			params: map[string]string{"custom-key": "custom-value"},
			exp: &signalr.Client{
				Protocol:        "1337",
				Endpoint:        "/signalr",
				ConnectionToken: "hello world",
				ConnectionID:    "1234-ABC",
				ConnectionData:  "",
			},
		},
	}

	for id, tc := range cases {
		tc := tc

		t.Run(id, func(t *testing.T) {
			// Set the debug flag.
			if tc.useDebug {
				os.Setenv("DEBUG", "true")
			}

			// Reset the request ID.
			requestID = 0

			// Prepare to save parameters.
			var params map[string]string

			ctx, cancel := context.WithCancel(context.Background())
			t.Cleanup(cancel)

			// Create a test server.
			ts := testutil.NewTestServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				params = extractCustomParams(r.URL.Query())
				tc.fn(w, r)
			}), tc.TLS)
			t.Cleanup(ts.Close)

			// Create a test client.
			c := testutil.NewTestClient(tc.in.Protocol, tc.in.Endpoint, tc.in.ConnectionData, tc.params, ts)

			// Set the wait time to milliseconds.
			c.RetryWaitDuration = 1 * time.Millisecond

			// Set a custom scheme if one is specified.
			if tc.scheme != "" {
				c.Scheme = tc.scheme
			}

			// Perform the negotiation.
			err := c.Negotiate(ctx)

			// If the scheme is invalid, this will never send a request, so we move
			// on. Otherwise, we wait for the request to complete.
			if tc.scheme != ":" {
				return
			}

			// Make sure the error matches the expected error.
			errMatches(t, tc.wantErr, err)

			// Validate the things we expect.
			equals(t, tc.exp.ConnectionToken, c.ConnectionToken)
			equals(t, tc.exp.ConnectionID, c.ConnectionID)
			equals(t, tc.exp.Protocol, c.Protocol)
			equals(t, tc.exp.Endpoint, c.Endpoint)
			equals(t, tc.params, params)

			// Unset the debug flag.
			if tc.useDebug {
				os.Unsetenv("DEBUG")
			}
		})
	}
}

func extractCustomParams(values url.Values) map[string]string {
	// Remove the parameters that we know will be there.
	values.Del("transport")
	values.Del("clientProtocol")
	values.Del("connectionData")
	values.Del("tid")

	// Return nil if nothing remains.
	if len(values) == 0 {
		return nil
	}

	// Save the custom parameters.
	params := make(map[string]string)
	for k, v := range values {
		params[k] = v[0]
	}

	// Return the custom parameters.
	return params
}

func TestClient_Connect(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		fn      http.HandlerFunc
		tls     bool
		cookies []*http.Cookie
		params  map[string]string
		wantErr error
	}{
		{
			name: "successful https connect",
			fn:   testutil.TestConnect,
			tls:  true,
		},
		{
			name: "successful http connect",
			fn:   testutil.TestConnect,
		},
		{
			name:    "service not available",
			fn:      throw503Error,
			tls:     true,
			wantErr: websocket.ErrBadHandshake,
		},
		{
			name:    "generic error",
			fn:      throw404Error,
			tls:     true,
			wantErr: errors.New("xconnect failed: 404 Not Found, retry 0: websocket: bad handshake"),
		},
		{
			name: "custom cookie jar",
			fn:   testutil.TestConnect,
			cookies: []*http.Cookie{{
				Name:  "hello",
				Value: "world",
			}},
		},
		{
			name:   "custom parameters",
			fn:     testutil.TestConnect,
			tls:    true,
			params: map[string]string{"custom-key": "custom-value"},
		},
	}

	for _, tc := range cases {
		tc := tc

		t.Run(tc.name, func(t *testing.T) {
			recordResponse := func(w http.ResponseWriter, r *http.Request) {
				if tc.wantErr == nil {
					equals(t, tc.cookies, r.Cookies(), cmpopts.EquateEmpty())
					equals(t, tc.params, extractCustomParams(r.URL.Query()))
					notEmpty(t, r.URL.Query().Get("tid"))
				}

				tc.fn(w, r)
			}

			// Set up the test server.
			ts := testutil.NewTestServer(http.HandlerFunc(recordResponse), tc.tls)
			t.Cleanup(ts.Close)

			// Prepare a new client.
			c := testutil.NewTestClient("", "", "", tc.params, ts)

			// Set cookies if they have been configured.
			if tc.cookies != nil {
				u, err := url.Parse(ts.URL)
				if err != nil {
					log.Panic(err)
				}
				c.HTTPClient.Jar, err = cookiejar.New(nil)
				if err != nil {
					log.Panic(err)
				}
				c.HTTPClient.Jar.SetCookies(u, tc.cookies)
			}

			// Set the wait time to milliseconds.
			c.RetryWaitDuration = 1 * time.Millisecond

			// Perform the connection.
			conn, err := c.Connect(context.Background())
			errMatches(t, tc.wantErr, err)
			if tc.wantErr == nil {
				notNil(t, conn)
			}
		})
	}
}

func TestClient_Reconnect(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name        string
		groupsToken string
		messageID   string
	}{
		{
			name: "successful reconnect",
		},
		{
			name:        "groups token",
			groupsToken: "my-custom-token",
		},
		{
			name:      "message id",
			messageID: "unique-message-id",
		},
	}

	for _, tc := range cases {
		tc := tc

		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			t.Cleanup(cancel)

			recordResponse := func(w http.ResponseWriter, req *http.Request) {
				equals(t, tc.groupsToken, req.URL.Query().Get("groupsToken"))
				equals(t, tc.messageID, req.URL.Query().Get("messageId"))
				testutil.TestReconnect(w, req)
			}

			// Set up the test server.
			ts := testutil.NewTestServer(http.HandlerFunc(recordResponse), true)
			t.Cleanup(ts.Close)

			// Prepare a new client.
			c := testutil.NewTestClient("", "", "", nil, ts)

			// Set the wait time to milliseconds.
			c.RetryWaitDuration = 1 * time.Millisecond

			// Set the group token.
			c.GroupsToken.Store(tc.groupsToken)
			c.MessageID.Store(tc.messageID)

			// Perform the connection.
			conn, err := c.Reconnect(ctx)
			if ok(t, err) {
				notNil(t, conn)
			}
		})
	}
}

func handleWebsocketWithCustomMsg(w http.ResponseWriter, r *http.Request, msg string) {
	upgrader := websocket.Upgrader{}
	c, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Panic(err)
	}

	go func() {
		for {
			_, _, rerr := c.ReadMessage()
			if rerr != nil {
				return
			}
		}
	}()

	go func() {
		for {
			werr := c.WriteMessage(websocket.TextMessage, []byte(msg))
			if werr != nil {
				return
			}
		}
	}()
}

func TestClient_Start(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		skipConnect bool
		skipRetries bool
		startFn     http.HandlerFunc
		connectFn   http.HandlerFunc
		scheme      signalr.Scheme
		params      map[string]string
		groupsToken string
		messageID   string
		wantErr     error
	}{
		"successful start": {
			startFn:   testutil.TestStart,
			connectFn: testutil.TestConnect,
		},
		"nil connection": {
			skipConnect: true,
			wantErr:     errors.New("connection is nil"),
		},
		"failed get request": {
			startFn:   causeWriteResponseTimeout,
			connectFn: testutil.TestConnect,
			wantErr:   io.EOF,
		},
		"invalid json sent in response to get request": {
			startFn: func(w http.ResponseWriter, r *http.Request) {
				_, err := w.Write([]byte("invalid json"))
				if err != nil {
					log.Panic(err)
				}
			},
			connectFn: testutil.TestConnect,
			wantErr:   &json.SyntaxError{},
		},
		"non-'started' response": {
			startFn: func(w http.ResponseWriter, r *http.Request) {
				_, err := w.Write([]byte(`{"Response":"not expecting this"}`))
				if err != nil {
					log.Panic(err)
				}
			},
			connectFn: testutil.TestConnect,
			wantErr:   errors.New("start response is not 'started': not expecting this"),
		},
		"non-text message from websocket": {
			startFn: testutil.TestStart,
			connectFn: func(w http.ResponseWriter, r *http.Request) {
				upgrader := websocket.Upgrader{}
				c, err := upgrader.Upgrade(w, r, nil)
				if err != nil {
					log.Panic(err)
				}
				err = c.WriteMessage(websocket.BinaryMessage, []byte("non-text message"))
				if err != nil {
					log.Panic(err)
				}
			},
			wantErr: errors.New("unexpected websocket control type"),
		},
		"invalid json sent in init message": {
			startFn: testutil.TestStart,
			connectFn: func(w http.ResponseWriter, r *http.Request) {
				upgrader := websocket.Upgrader{}
				c, err := upgrader.Upgrade(w, r, nil)
				if err != nil {
					log.Panic(err)
				}
				err = c.WriteMessage(websocket.TextMessage, []byte("invalid json"))
				if err != nil {
					log.Panic(err)
				}
			},
			wantErr: &json.SyntaxError{},
		},
		"wrong S value from server": {
			startFn: testutil.TestStart,
			connectFn: func(w http.ResponseWriter, r *http.Request) {
				upgrader := websocket.Upgrader{}
				c, err := upgrader.Upgrade(w, r, nil)
				if err != nil {
					log.Panic(err)
				}
				err = c.WriteMessage(websocket.TextMessage, []byte(`{"S":3}`))
				if err != nil {
					log.Panic(err)
				}
			},
			wantErr: errors.New("unexpected S value received from server"),
		},
		"request preparation failure": {
			startFn:   testutil.TestStart,
			connectFn: testutil.TestConnect,
			scheme:    ":",
			wantErr:   errors.New("failed to prepare request: get request creation failed"),
		},
		"custom parameters": {
			startFn:   testutil.TestStart,
			connectFn: testutil.TestConnect,
			params:    map[string]string{"custom-key": "custom-value"},
		},
		"groups token": {
			startFn:     testutil.TestStart,
			groupsToken: "my-custom-groups-token",
			connectFn: func(w http.ResponseWriter, r *http.Request) {
				handleWebsocketWithCustomMsg(w, r, `{"S":1,"G":"my-custom-groups-token"}`)
			},
		},
		"message id": {
			startFn:   testutil.TestStart,
			messageID: "my-custom-message-id",
			connectFn: func(w http.ResponseWriter, r *http.Request) {
				handleWebsocketWithCustomMsg(w, r, `{"S":1,"C":"my-custom-message-id"}`)
			},
		},
	}

	for id, tc := range cases {
		tc := tc

		t.Run(id, func(t *testing.T) {
			var params map[string]string

			// Create a test server that is initialized with this test
			// case's "start handler".
			ts := testutil.NewTestServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.Contains(r.URL.Path, "/start") {
					params = extractCustomParams(r.URL.Query())
					tc.startFn(w, r)
				} else if strings.Contains(r.URL.Path, "/connect") {
					tc.connectFn(w, r)
				}
			}), true)
			t.Cleanup(ts.Close)

			// Create a test client and establish the initial connection.
			c := testutil.NewTestClient("", "", "", tc.params, ts)

			// Set the wait time to milliseconds.
			c.RetryWaitDuration = 1 * time.Millisecond

			// Don't perform any retries.
			if tc.skipRetries {
				c.MaxStartRetries = 0
			}

			ctx := context.Background()

			// Perform the connection.
			var (
				conn signalr.WebsocketConn
				err  error
			)
			if !tc.skipConnect {
				conn, err = c.Connect(ctx)
				if err != nil {
					// If this fails, it is not part of the test, so we
					// panic here.
					log.Fatal(err)
				}
			}

			// Set a custom scheme if one is specified.
			if tc.scheme != "" {
				c.Scheme = tc.scheme
			}

			// Execute the start function.
			err = c.Start(ctx, conn)
			switch {
			case tc.wantErr != nil:
				errMatches(t, err, tc.wantErr)
			case ok(t, err):
				// Verify that the connection was properly set.
				equals(t, conn, c.Conn(), cmpopts.IgnoreUnexported(websocket.Conn{}, tls.Conn{}, net.TCPConn{}))

				// Verify parameters were properly set.
				equals(t, tc.params, params)

				// Verify the groups token was properly set.
				equals(t, tc.groupsToken, c.GroupsToken.Load())

				// Verify the message ID was properly set.
				equals(t, tc.messageID, c.MessageID.Load())
			}
		})
	}
}

func TestClient_ReadMessages(t *testing.T) {
	t.Parallel()

	genericError := errors.New("generic error")

	repeat := func(count int, errs ...error) []error {
		res := make([]error, len(errs)*count)
		for i := 0; i < count; i++ {
			copy(res[i*len(errs):], errs)
		}

		return res
	}

	cases := []struct {
		name     string
		errors   []error
		expected error
	}{
		{
			name:     "1000 error",
			errors:   []error{&websocket.CloseError{Code: 1000}},
			expected: nil,
		},
		{
			name:     "1001 error",
			errors:   []error{&websocket.CloseError{Code: 1001}},
			expected: nil,
		},
		{
			name:     "1006 error",
			errors:   []error{&websocket.CloseError{Code: 1006}},
			expected: nil,
		},
		{
			name:     "generic error",
			errors:   []error{genericError},
			expected: genericError,
		},
		{
			name:     "many generic errors",
			errors:   repeat(20, genericError),
			expected: genericError,
		},
		{
			name:     "1001, then 1006 error",
			errors:   []error{&websocket.CloseError{Code: 1001}, &websocket.CloseError{Code: 1006}},
			expected: nil,
		},
		{
			name:     "1006, then 1001 error",
			errors:   []error{&websocket.CloseError{Code: 1006}, &websocket.CloseError{Code: 1001}},
			expected: nil,
		},
		{
			name:     "all the recoverable errors",
			errors:   []error{&websocket.CloseError{Code: 1000}, &websocket.CloseError{Code: 1001}, &websocket.CloseError{Code: 1006}},
			expected: nil,
		},
		{
			name:     "multiple recoverable errors",
			errors:   repeat(5, &websocket.CloseError{Code: 1000}, &websocket.CloseError{Code: 1001}, &websocket.CloseError{Code: 1006}),
			expected: nil,
		},
		{
			name:     "multiple recoverable errors, followed by one unrecoverable error",
			errors:   append(repeat(5, &websocket.CloseError{Code: 1000}, &websocket.CloseError{Code: 1001}, &websocket.CloseError{Code: 1006}), genericError),
			expected: genericError,
		},
	}

	for _, tc := range cases {
		tc := tc

		t.Run(tc.name, func(t *testing.T) {
			ts := testutil.NewTestServer(nil, true)
			t.Cleanup(ts.Close)

			// Make a new client.
			c := testutil.NewTestClient("1.5", "/signalr", "all the data", nil, ts)
			c.RetryWaitDuration = 1 * time.Millisecond

			ctx, cancel := context.WithCancel(context.Background())
			t.Cleanup(cancel)

			// Perform the first part of the initialization routine.

			if err := c.Negotiate(ctx); err != nil {
				t.Fatal(err)
			}

			conn, err := c.Connect(ctx)
			if err != nil {
				t.Fatal(err)
			}

			if err := c.Start(ctx, conn); err != nil {
				t.Fatal(err)
			}

			// Attach a test connection.
			c.SetConn(NewFakeConn(tc.errors...))

			err = c.ReadMessages(ctx, func(context.Context, signalr.Message) error {
				cancel()
				return nil
			})
			errMatches(t, tc.expected, err)
		})
	}
}

func TestClient_ReadMessages_earlyClose(t *testing.T) {
	t.Parallel()

	c := signalr.New("", "", "", "", map[string]string{})
	c.SetConn(NewFakeConn())

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	msgHandler := func(context.Context, signalr.Message) error { return nil }
	err := c.ReadMessages(ctx, msgHandler)
	if err != nil {
		t.Errorf("failed to read messages: %v", err)
	}
}

func TestClient_ReadMessages_longReconnectAttempt(t *testing.T) {
	t.Parallel()

	ts := testutil.NewTestServer(http.HandlerFunc(
		func(w http.ResponseWriter, req *http.Request) {
			if strings.Contains(req.URL.Path, "/reconnect") {
				// Intentionally wait indefinitely on reconnect.
				<-req.Context().Done()
				return
			}

			testutil.DefaultHandler(w, req)
		}), true)
	t.Cleanup(ts.Close)

	c := signalr.New("", "", "", "", map[string]string{})
	// Define a maximum reconnect attempt time for the client.
	c.MaxReconnectAttemptDuration = 50 * time.Millisecond

	closeErr := &websocket.CloseError{Code: 10006}
	c.SetConn(NewFakeConn(closeErr))

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	t.Cleanup(cancel)

	err := c.ReadMessages(ctx, func(context.Context, signalr.Message) error { return nil })
	errMatches(t, closeErr, err)
}

func TestClient_Init(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		negotiateFn func(http.ResponseWriter, *http.Request)
		connectFn   func(http.ResponseWriter, *http.Request)
		startFn     func(http.ResponseWriter, *http.Request)
		wantErr     error
	}{
		"successful init": {
			negotiateFn: testutil.TestNegotiate,
			connectFn:   testutil.TestConnect,
			startFn:     testutil.TestStart,
			wantErr:     nil,
		},
		"failed negotiate": {
			negotiateFn: func(w http.ResponseWriter, r *http.Request) {
				_, err := w.Write([]byte("invalid json"))
				if err != nil {
					log.Panic(err)
				}
			},
			wantErr: &json.SyntaxError{},
		},
		"failed connect": {
			negotiateFn: testutil.TestNegotiate,
			connectFn:   throw678Error,
			wantErr:     errors.New("connect failed: xconnect failed: 678 status code 678"),
		},
		"failed start": {
			negotiateFn: testutil.TestNegotiate,
			connectFn:   testutil.TestConnect,
			startFn:     causeWriteResponseTimeout,
			wantErr:     io.EOF,
		},
	}

	for id, tc := range cases {
		tc := tc

		t.Run(id, func(t *testing.T) {
			ts := testutil.NewTestServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case strings.Contains(r.URL.Path, "/negotiate"):
					tc.negotiateFn(w, r)
				case strings.Contains(r.URL.Path, "/connect"):
					tc.connectFn(w, r)
				case strings.Contains(r.URL.Path, "/start"):
					tc.startFn(w, r)
				default:
					log.Println("url:", r.URL)
				}
			}), true)
			t.Cleanup(ts.Close)

			c := testutil.NewTestClient("1.5", "/signalr", "all the data", nil, ts)
			c.RetryWaitDuration = 1 * time.Millisecond

			ctx, cancel := context.WithCancel(context.Background())
			t.Cleanup(cancel)

			// Define handlers.
			msgHandler := func(context.Context, signalr.Message) error {
				cancel()
				return nil
			}

			// Run the client.
			err := c.Run(ctx, msgHandler)
			errMatches(t, tc.wantErr, err)
		})
	}
}

type FakeConn struct {
	errors []error
	data   interface{}
}

func NewFakeConn(errs ...error) *FakeConn {
	return &FakeConn{
		errors: errs,
	}
}

func (c *FakeConn) ReadMessage() (messageType int, p []byte, err error) {
	if len(c.errors) == 0 {
		return 0, nil, nil
	}

	err = c.errors[0]
	c.errors = c.errors[1:]
	return 0, nil, err
}

func (c *FakeConn) WriteJSON(v interface{}) error {
	// Save the data that is supposedly being written, so it can be
	// inspected later.
	c.data = v

	if len(c.errors) == 0 {
		return nil
	}

	err := c.errors[0]
	c.errors = c.errors[1:]
	return err
}

func TestClient_Send(t *testing.T) {
	t.Parallel()

	writeError := errors.New("write error")

	cases := map[string]struct {
		conn    *FakeConn
		wantErr error
	}{
		"successful write": {
			conn:    NewFakeConn(),
			wantErr: nil,
		},
		"connection not set": {
			conn:    nil,
			wantErr: signalr.ErrConnectionNotSet,
		},
		"write error": {
			conn:    NewFakeConn(writeError),
			wantErr: writeError,
		},
	}

	for id, tc := range cases {
		tc := tc

		t.Run(id, func(t *testing.T) {
			// Set up a new test client.
			c := signalr.New("", "", "", "", nil)

			// Set up a fake connection, if one has been created.
			if tc.conn != nil {
				c.SetConn(tc.conn)
			}

			// Send the message.
			data := hubs.ClientMsg{H: "test data 123"}
			err := c.Send(data)

			// Check the results.
			if tc.wantErr != nil {
				errMatches(t, tc.wantErr, err)
			} else {
				equals(t, data, tc.conn.data)
				ok(t, err)
			}
		})
	}
}

func TestNew(t *testing.T) {
	t.Parallel()

	// Define parameter values.
	host := "test-host"
	protocol := "test-protocol"
	endpoint := "test-endpoint"
	connectionData := "test-connection-data"
	params := map[string]string{
		"test-key": "test-value",
	}

	// Create the client.
	c := signalr.New(host, protocol, endpoint, connectionData, params)

	// Validate values were set up properly.
	equals(t, host, c.Host)
	equals(t, protocol, c.Protocol)
	equals(t, endpoint, c.Endpoint)
	equals(t, connectionData, c.ConnectionData)
	notNil(t, c.HTTPClient)
	notNil(t, c.HTTPClient.Transport)
	equals(t, signalr.HTTPS, c.Scheme)
	equals(t, 5, c.MaxNegotiateRetries)
	equals(t, 5, c.MaxConnectRetries)
	equals(t, 5, c.MaxReconnectRetries)
	equals(t, 5, c.MaxStartRetries)
	equals(t, 1*time.Minute, c.RetryWaitDuration)
}
