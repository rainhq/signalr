package testutil

import (
	"net/http/httptest"

	"github.com/rainhq/signalr/v2"
)

func NewTestClient(protocol, endpoint, connectionData string, params map[string]string, ts *httptest.Server) *signalr.Client {
	// Prepare a SignalR client.
	c := signalr.New(ts.Listener.Addr().String(), protocol, endpoint, connectionData, params)
	c.HTTPClient = ts.Client()

	// Save the TLS config in case this is using TLS.
	if ts.TLS != nil {
		c.TLSClientConfig = ts.TLS
		c.Scheme = signalr.HTTPS
	} else {
		c.Scheme = signalr.HTTP
	}

	return c
}
