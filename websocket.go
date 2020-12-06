package signalr

import (
	"context"
	"crypto/tls"
	"errors"
	"net/http"
	"net/url"

	"github.com/gorilla/websocket"
)

type ProxyFunc func(req *http.Request) (*url.URL, error)

type WebsocketDialerFunc func(client *http.Client) WebsocketDialer

type WebsocketDialer interface {
	Dial(ctx context.Context, u string, headers http.Header) (conn WebsocketConn, status int, err error)
}

// WebsocketConn is a combination of MessageReader and JSONWriter. It is used to
// provide an interface to objects that can read from and write to a websocket
// connection.
type WebsocketConn interface {
	ReadMessage(ctx context.Context) (messageType int, p []byte, err error)
	WriteMessage(ctx context.Context, messageType int, p []byte) error
	Close() error
}

var (
	_ WebsocketDialerFunc = NewDefaultDialer
	_ WebsocketDialer     = &defaultDialer{}
	_ WebsocketConn       = &defaultConn{}
)

type defaultDialer struct {
	websocket.Dialer
}

func NewDefaultDialer(client *http.Client) WebsocketDialer {
	proxy := http.ProxyFromEnvironment
	var tlsConfig *tls.Config

	if t, ok := client.Transport.(*http.Transport); ok {
		proxy = t.Proxy
		tlsConfig = t.TLSClientConfig
	}

	return defaultDialer{
		Dialer: websocket.Dialer{
			TLSClientConfig: tlsConfig,
			Proxy:           proxy,
			Jar:             client.Jar,
		},
	}
}

func (d defaultDialer) Dial(ctx context.Context, u string, headers http.Header) (WebsocketConn, int, error) {
	//nolint:bodyclose
	conn, res, err := d.DialContext(ctx, u, headers)

	var status int
	if res != nil {
		status = res.StatusCode
	}

	if err != nil {
		return nil, status, err
	}

	return &defaultConn{Conn: conn}, status, err
}

type defaultConn struct {
	*websocket.Conn
}

func (c *defaultConn) ReadMessage(ctx context.Context) (messageType int, p []byte, err error) {
	select {
	case <-ctx.Done():
		return 0, nil, ctx.Err()
	default:
	}

	deadline, _ := ctx.Deadline()
	c.Conn.SetReadDeadline(deadline)

	messageType, p, err = c.Conn.ReadMessage()

	var closeErr *websocket.CloseError
	if errors.As(err, &closeErr) {
		return 0, nil, &CloseError{code: closeErr.Code, text: closeErr.Text}
	}

	return messageType, p, err
}

func (c *defaultConn) WriteMessage(ctx context.Context, messageType int, p []byte) error {
	deadline, _ := ctx.Deadline()
	c.Conn.SetWriteDeadline(deadline)

	return c.Conn.WriteMessage(messageType, p)
}
