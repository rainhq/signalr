package signalr

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
)

type fakeConn struct {
	err     error
	errs    chan error
	msgType int
	msg     string
}

func (c *fakeConn) ReadMessage() (n int, data []byte, err error) {
	// Set the message type.
	msgType := c.msgType

	// Set the message.
	p := []byte(c.msg)

	// Default to using the errs channel.
	if c.errs != nil {
		return 0, nil, <-c.errs
	}

	// Otherwise use a static error.
	err = c.err

	return msgType, p, err
}

func (c *fakeConn) WriteJSON(v interface{}) (err error) {
	return
}

func TestPrefixedID(t *testing.T) {
	t.Parallel()

	cases := []struct {
		in  string
		exp string
	}{
		{"", ""},
		{"123", "[123] "},
		{"abc", "[abc] "},
	}

	for _, tc := range cases {
		act := prefixedID(tc.in)
		equals(t, tc.exp, act)
	}
}

func TestPrepareRequest(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	cases := map[string]struct {
		url     string
		headers map[string]string
		req     *http.Request
		wantErr error
	}{
		"simple request with no headers": {
			url:     "http://example.org",
			headers: map[string]string{},
			req: &http.Request{
				Host:       "example.org",
				Method:     "GET",
				Proto:      "HTTP/1.1",
				ProtoMajor: 1,
				ProtoMinor: 1,
				URL: &url.URL{
					Scheme: "http",
					Host:   "example.org",
				},
				Header: http.Header{},
			},
			wantErr: nil,
		},
		"complex request with headers": {
			url: "https://example.org/custom/path?param=123",
			headers: map[string]string{
				"header1": "value1",
				"header2": "value2a,value2b",
			},
			req: &http.Request{
				Host:       "example.org",
				Method:     "GET",
				Proto:      "HTTP/1.1",
				ProtoMajor: 1,
				ProtoMinor: 1,
				URL: &url.URL{
					Scheme:   "https",
					Host:     "example.org",
					Path:     "/custom/path",
					RawQuery: "param=123",
				},
				Header: http.Header{
					"Header1": []string{"value1"},
					"Header2": []string{"value2a,value2b"},
				},
			},
			wantErr: nil,
		},
		"invalid URL": {
			url:     ":",
			headers: map[string]string{},
			req:     nil,
			wantErr: errors.New("missing protocol scheme"),
		},
	}

	for id, tc := range cases {
		tc := tc

		t.Run(id, func(t *testing.T) {
			req, err := prepareRequest(ctx, tc.url, tc.headers)
			if tc.req != nil {
				equals(t, tc.req.WithContext(ctx), req, cmp.AllowUnexported(http.Request{}))
			}
			errMatches(t, tc.wantErr, err)
		})
	}
}

func TestProcessReadMessagesMessage(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		p       []byte
		expMsg  *Message
		wantErr error
	}{
		"empty message": {
			p:       []byte(""),
			expMsg:  nil,
			wantErr: &json.SyntaxError{},
		},
		"bad json": {
			p:       []byte("{invalid json"),
			expMsg:  nil,
			wantErr: &json.SyntaxError{},
		},
		"keepalive": {
			p:       []byte("{}"),
			expMsg:  nil,
			wantErr: nil,
		},
		"normal message": {
			p:       []byte(`{"C":"test message"}`),
			expMsg:  &Message{C: "test message"},
			wantErr: nil,
		},
		"groups token": {
			p:       []byte(`{"C":"test message","G":"custom-groups-token"}`),
			expMsg:  &Message{C: "test message", G: "custom-groups-token"},
			wantErr: nil,
		},
	}

	for id, tc := range cases {
		tc := tc

		t.Run(id, func(t *testing.T) {
			c := new(Client)

			// Parse the message.
			var msg Message
			err := c.parseMessage(tc.p, &msg)

			switch {
			case errors.Is(err, context.DeadlineExceeded):
				t.Error("test timed out")
			case err != nil:
				errMatches(t, err, tc.wantErr)
			case tc.expMsg != nil:
				equals(t, *tc.expMsg, msg)
				equals(t, tc.expMsg.C, c.MessageID.Load())
			}
		})
	}
}

type EmptyCookieJar struct{}

func (j EmptyCookieJar) SetCookies(u *url.URL, cookies []*http.Cookie) {}

func (j EmptyCookieJar) Cookies(u *url.URL) []*http.Cookie {
	return make([]*http.Cookie, 0)
}

type FakeCookieJar struct {
	cookies map[string]string
}

func (j FakeCookieJar) SetCookies(u *url.URL, cookies []*http.Cookie) {}

func (j FakeCookieJar) Cookies(u *url.URL) []*http.Cookie {
	cookies := make([]*http.Cookie, len(j.cookies))
	i := 0
	for k, v := range j.cookies {
		cookies[i] = &http.Cookie{
			Name:  k,
			Value: v,
		}
		i++
	}

	// Sort it so the results are consistent.
	sort.Slice(cookies, func(i, j int) bool {
		return strings.Compare(cookies[i].Name, cookies[j].Name) < 0
	})

	return cookies
}

func TestMakeHeader(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		in  *Client
		exp http.Header
	}{
		"nil client": {
			in:  nil,
			exp: http.Header{},
		},
		"nil http client": {
			in:  &Client{HTTPClient: nil},
			exp: http.Header{},
		},
		"empty cookie jar": {
			in: &Client{HTTPClient: &http.Client{
				Jar: EmptyCookieJar{},
			}},
			exp: http.Header{},
		},
		"one cookie": {
			in: &Client{HTTPClient: &http.Client{
				Jar: FakeCookieJar{
					cookies: map[string]string{"key1": "value1"},
				},
			}},
			exp: http.Header{
				"Cookie": []string{"key1=value1"},
			},
		},
		"three cookies": {
			in: &Client{HTTPClient: &http.Client{
				Jar: FakeCookieJar{
					cookies: map[string]string{
						"key1": "value1",
						"key2": "value2",
						"key3": "value3",
					},
				},
			}},
			exp: http.Header{
				"Cookie": []string{
					"key1=value1; key2=value2; key3=value3",
				},
			},
		},
		"one custom header": {
			in: &Client{Headers: map[string]string{
				"custom1": "value1",
			}},
			exp: http.Header{
				"Custom1": []string{"value1"},
			},
		},
		"three custom headers": {
			in: &Client{Headers: map[string]string{
				"custom1": "value1",
				"custom2": "value2",
				"custom3": "value3",
			}},
			exp: http.Header{
				"Custom1": []string{"value1"},
				"Custom2": []string{"value2"},
				"Custom3": []string{"value3"},
			},
		},
	}

	for id, tc := range cases {
		tc := tc

		t.Run(id, func(t *testing.T) {
			equals(t, tc.exp, makeHeader(tc.in))
		})
	}
}

func TestProcessStartResponse(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		data    string
		conn    WebsocketConn
		wantErr error
	}{
		{
			name:    "invalid json in response",
			data:    "invalid json",
			conn:    &fakeConn{},
			wantErr: &json.SyntaxError{},
		},
		{
			name:    "non-started response 1",
			data:    `{"hello":"world"}`,
			conn:    &fakeConn{},
			wantErr: errors.New(`start response is not 'started'`),
		},
		{
			name:    "non-stared response 2",
			data:    `{"Response":"blabla"}`,
			conn:    &fakeConn{},
			wantErr: errors.New(`start response is not 'started'`),
		},
		{
			name:    "wrong message type",
			data:    `{"Response":"started"}`,
			conn:    &fakeConn{msgType: 9001},
			wantErr: errors.New("unexpected websocket control type: 9001"),
		},
		{
			name:    "message json unmarshal failure",
			data:    `{"Response":"started"}`,
			conn:    &fakeConn{msgType: 1, msg: "invalid json"},
			wantErr: &json.SyntaxError{},
		},
		{
			name:    "server not initialized",
			data:    `{"Response":"started"}`,
			conn:    &fakeConn{msgType: 1, msg: `{"S":9002}`},
			wantErr: errors.New(`unexpected S value received from server: 9002 | message: {"S":9002}`),
		},
		{
			name:    "successful call",
			data:    `{"Response":"started"}`,
			conn:    &fakeConn{msgType: 1, msg: `{"S":1}`},
			wantErr: nil,
		},
	}

	for _, tc := range cases {
		tc := tc

		t.Run(tc.name, func(t *testing.T) {
			// Make a new client.
			c := New("", "", "", "", nil)

			err := c.processStartResponse([]byte(tc.data), tc.conn)

			if tc.wantErr != nil {
				errMatches(t, tc.wantErr, err)
			} else {
				equals(t, tc.conn, c.conn, cmp.AllowUnexported(fakeConn{}))
			}
		})
	}
}

func TestProcessNegotiateResponse(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		data            string
		connectionToken string
		connectionID    string
		protocol        string
		endpoint        string
		wantErr         error
	}{
		"empty json": {
			data:    "",
			wantErr: &json.SyntaxError{},
		},
		"invalid json": {
			data:    "invalid json",
			wantErr: &json.SyntaxError{},
		},
		"valid data": {
			// nolint:lll
			data:            `{"ConnectionToken":"123abc","ConnectionID":"456def","ProtocolVersion":"my-custom-protocol","Url":"super-awesome-signalr"}`,
			connectionToken: "123abc",
			connectionID:    "456def",
			protocol:        "my-custom-protocol",
			endpoint:        "super-awesome-signalr",
			wantErr:         nil,
		},
	}

	for id, tc := range cases {
		tc := tc

		t.Run(id, func(t *testing.T) {
			// Create a test client.
			c := New("", "", "", "", nil)

			// Get the result.
			err := c.processNegotiateResponse([]byte(tc.data))

			// Evaluate the result.
			if tc.wantErr != nil {
				errMatches(t, err, tc.wantErr)
			} else {
				equals(t, tc.connectionToken, c.ConnectionToken)
				equals(t, tc.connectionID, c.ConnectionID)
				equals(t, tc.protocol, c.Protocol)
				equals(t, tc.endpoint, c.Endpoint)
			}
		})
	}
}

func TestClient_attemptReconnect(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		maxRetries int
	}{
		{
			name:       "successful reconnect",
			maxRetries: 5,
		},
		{
			name:       "unsuccessful reconnect",
			maxRetries: 0,
		},
	}

	for _, tc := range cases {
		tc := tc

		t.Run(tc.name, func(t *testing.T) {
			// Create a test client.
			c := New("", "", "", "", nil)

			// Set the maximum number of retries.
			c.MaxReconnectRetries = tc.maxRetries
			c.RetryWaitDuration = 1 * time.Millisecond

			ctx := context.Background()

			// Attempt to reconnect.
			_ = c.attemptReconnect(ctx)
		})
	}
}

func equals(t testing.TB, exp, act interface{}, opts ...cmp.Option) {
	t.Helper()

	if !cmp.Equal(exp, act, opts...) {
		t.Errorf("unexpected value:\n%s", cmp.Diff(exp, act, opts...))
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
