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
	// ReadMessage is modeled after the function defined at
	// https://godoc.org/github.com/gorilla/websocket#Conn.ReadMessage
	//
	// At a high level, it reads messages and returns:
	//  - the type of message read
	//  - the bytes that were read
	//  - any errors encountered during reading the message
	ReadMessage() (messageType int, p []byte, err error)

	WriteMessage(messageType int, p []byte) error

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

func (d defaultDialer) Dial(ctx context.Context, u string, headers http.Header) (conn WebsocketConn, status int, err error) {
	//nolint:bodyclose
	conn, res, err := d.DialContext(ctx, u, headers)

	if res != nil {
		status = res.StatusCode
	}

	return conn, status, err
}

type defaultConn struct {
	websocket.Conn
}

func (c *defaultConn) ReadMessage() (messageType int, p []byte, err error) {
	messageType, p, err = c.Conn.ReadMessage()

	var closeErr *websocket.CloseError
	if errors.As(err, &closeErr) {
		return messageType, p, &CloseError{code: closeErr.Code, text: closeErr.Text}
	}

	return messageType, p, err
}
