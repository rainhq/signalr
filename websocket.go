package signalr

import (
	"context"
	"crypto/tls"
	"net/http"
	"net/url"

	gorilla_websocket "github.com/gorilla/websocket"
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

	// WriteJSON is modeled after the function defined at
	// https://godoc.org/github.com/gorilla/websocket#Conn.WriteJSON
	//
	// At a high level, it writes a structure to the underlying websocket and
	// returns any error that was encountered during the write operation.
	WriteJSON(v interface{}) error
}

var (
	_ WebsocketDialer     = &gorillaDialer{}
	_ WebsocketDialerFunc = NewGorillaDialer
)

type gorillaDialer struct {
	delegate gorilla_websocket.Dialer
}

func NewGorillaDialer(client *http.Client) WebsocketDialer {
	proxy := http.ProxyFromEnvironment
	var tlsConfig *tls.Config

	if t, ok := client.Transport.(*http.Transport); ok {
		proxy = t.Proxy
		tlsConfig = t.TLSClientConfig
	}

	return gorillaDialer{
		delegate: gorilla_websocket.Dialer{
			TLSClientConfig: tlsConfig,
			Proxy:           proxy,
			Jar:             client.Jar,
		},
	}
}

func (d gorillaDialer) Dial(ctx context.Context, u string, headers http.Header) (conn WebsocketConn, status int, err error) {
	//nolint:bodyclose
	conn, res, err := d.delegate.DialContext(ctx, u, headers)

	return conn, res.StatusCode, err
}
