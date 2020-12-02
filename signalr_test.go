package signalr

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/cenkalti/backoff/v4"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/rainhq/signalr/v2/internal/testutil"
)

func TestNegotiate(t *testing.T) {
	t.Parallel()

	cdata := "connection data"
	retryWaitDuration := 5 * time.Millisecond

	cases := []struct {
		name        string
		handler     http.HandlerFunc
		headers     http.Header
		expectedErr error
	}{
		{
			name:        "successful negotiate",
			expectedErr: nil,
		},
		{
			name:        "recover after failure",
			handler:     errorResponseOnce(t, 503, testutil.TestNegotiate),
			expectedErr: nil,
		},
		{
			name:    "custom headers",
			headers: http.Header{"X-Custom": []string{"custom"}},
		},
		{
			name:        "503 error",
			handler:     errorResponse(t, 503),
			expectedErr: &url.Error{},
		},
		{
			name:        "failed get request",
			handler:     timeoutResponse(2 * retryWaitDuration),
			expectedErr: io.EOF,
		},
		{
			name:        "invalid json",
			handler:     invalidJSONResponse(t),
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
				handler = func(w http.ResponseWriter, req *http.Request) {
					headers := req.Header
					for k, v := range tc.headers {
						equals(t, v, headers[k])
					}

					testutil.TestNegotiate(w, req)
				}
			}

			// Create a test server.
			ts := httptest.NewServer(handler)
			t.Cleanup(ts.Close)

			state := State{
				ConnectionData: cdata,
				Protocol:       testutil.ProtocolVersion,
			}

			bo := backoff.WithMaxRetries(backoff.NewConstantBackOff(retryWaitDuration), 5)
			err := negotiate(ctx, ts.Client(), ts.URL, tc.headers, &state, bo)

			if tc.expectedErr != nil {
				errMatches(t, tc.expectedErr, err)
				return
			}

			equals(t, State{
				ConnectionData:  cdata,
				ConnectionID:    testutil.ConnectionID,
				ConnectionToken: testutil.ConnectionToken,
				Protocol:        testutil.ProtocolVersion,
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

			errMatches(t, tc.expectedErr, err)

			if err == nil {
				equals(t, tc.url, req.URL.String())
				equals(t, tc.headers, req.Header)
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

			errMatches(t, tc.expectedErr, err)
			equals(t, tc.expectedMsg, msg)
			equals(t, tc.expectedState, state)
		})
	}
}

func TestProcessStartResponse(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name        string
		data        string
		conn        WebsocketConn
		expectedErr error
	}{
		{
			name:        "invalid json in response",
			data:        "invalid json",
			conn:        &fakeConn{},
			expectedErr: &json.SyntaxError{},
		},
		{
			name:        "non-started response 1",
			data:        `{"hello":"world"}`,
			conn:        &fakeConn{},
			expectedErr: errors.New(`start response is not 'started'`),
		},
		{
			name:        "non-stared response 2",
			data:        `{"Response":"blabla"}`,
			conn:        &fakeConn{},
			expectedErr: errors.New(`start response is not 'started'`),
		},
		{
			name:        "wrong message type",
			data:        `{"Response":"started"}`,
			conn:        &fakeConn{msgType: 9001},
			expectedErr: errors.New("unexpected websocket control type: 9001"),
		},
		{
			name:        "message json unmarshal failure",
			data:        `{"Response":"started"}`,
			conn:        &fakeConn{msgType: 1, msg: "invalid json"},
			expectedErr: &json.SyntaxError{},
		},
		{
			name:        "server not initialized",
			data:        `{"Response":"started"}`,
			conn:        &fakeConn{msgType: 1, msg: `{"S":9002}`},
			expectedErr: errors.New(`unexpected S value received from server: 9002 | message: {"S":9002}`),
		},
		{
			name: "successful call",
			data: `{"Response":"started"}`,
			conn: &fakeConn{msgType: 1, msg: `{"S":1}`},
		},
	}

	for _, tc := range cases {
		tc := tc

		t.Run(tc.name, func(t *testing.T) {
			var state State
			err := processStartResponse([]byte(tc.data), tc.conn, &state)
			errMatches(t, tc.expectedErr, err)
		})
	}
}

func TestProcessNegotiateResponse(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name          string
		data          string
		expectedState State
		wantErr       error
	}{
		{
			name:    "empty json",
			data:    "",
			wantErr: &json.SyntaxError{},
		},
		{
			name:    "invalid json",
			data:    "invalid json",
			wantErr: &json.SyntaxError{},
		},
		{
			name: "valid data",
			// nolint:lll
			data: `{"ConnectionToken":"123abc","ConnectionID":"456def","ProtocolVersion":"my-custom-protocol","Url":"super-awesome-signalr"}`,
			expectedState: State{
				ConnectionToken: "123abc",
				ConnectionID:    "456def",
				Protocol:        "my-custom-protocol",
			},
			wantErr: nil,
		},
	}

	for _, tc := range cases {
		tc := tc

		t.Run(tc.name, func(t *testing.T) {
			var state State
			err := processNegotiateResponse([]byte(tc.data), &state)

			errMatches(t, tc.wantErr, err)
			equals(t, tc.expectedState, state)
		})
	}
}

func TestReconnect(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name        string
		maxRetries  int
		expectedErr error
	}{
		{
			name:       "successful reconnect",
			maxRetries: 5,
		},
		{
			name:        "unsuccessful reconnect",
			maxRetries:  0,
			expectedErr: ConnectError{},
		},
	}

	for _, tc := range cases {
		tc := tc

		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			bo := backoff.WithMaxRetries(backoff.NewConstantBackOff(1*time.Millisecond), uint64(tc.maxRetries))

			dialer := &fakeDialer{conn: &fakeConn{}}

			var state State
			_, err := reconnect(ctx, dialer, "fake-endpoint", make(http.Header), &state, bo)
			errMatches(t, tc.expectedErr, err)
		})
	}
}

type fakeDialer struct {
	conn *fakeConn
}

func (d *fakeDialer) Dial(_ context.Context, _ string, _ http.Header) (conn WebsocketConn, status int, err error) {
	return d.conn, http.StatusOK, nil
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

func (c *fakeConn) WriteJSON(v interface{}) (err error) {
	return
}

func equals(t testing.TB, exp, act interface{}) {
	t.Helper()

	if !cmp.Equal(exp, act) {
		t.Errorf("unexpected value:\n%s", cmp.Diff(exp, act))
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

func errorResponse(t testing.TB, status int) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
		_, err := w.Write([]byte(http.StatusText(status)))
		if err != nil {
			t.Fatal(err)
		}
	}
}

func errorResponseOnce(t testing.TB, status int, handler http.HandlerFunc) http.HandlerFunc {
	var done bool
	return func(w http.ResponseWriter, req *http.Request) {
		if !done {
			errorResponse(t, status)(w, req)
			done = true
			return
		}

		handler(w, req)
	}
}

func invalidJSONResponse(t testing.TB) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		_, err := w.Write([]byte("invalid json"))
		if err != nil {
			t.Fatal(err)
		}
	}
}

func timeoutResponse(timeout time.Duration) http.HandlerFunc {
	return func(rw http.ResponseWriter, r *http.Request) {
		time.Sleep(timeout)
	}
}
