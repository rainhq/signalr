package testutil

import (
	"crypto/x509"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/gorilla/websocket"
	"golang.org/x/sync/errgroup"
)

func NewTestServer(fn http.Handler, tls bool) *httptest.Server {
	if fn == nil {
		fn = http.HandlerFunc(DefaultHandler)
	}

	var ts *httptest.Server
	if tls {
		// Create the server.
		ts = httptest.NewTLSServer(fn)

		// Save the testing certificate to the TLS client config.
		//
		// I'm not sure why ts.TLS doesn't contain certificate information.
		// However, this seems to make the testing TLS certificate be trusted by
		// the client.
		ts.TLS.RootCAs = x509.NewCertPool()
		ts.TLS.RootCAs.AddCert(ts.Certificate())
	} else {
		// Create the server.
		ts = httptest.NewServer(fn)
	}

	return ts
}

func DefaultHandler(w http.ResponseWriter, r *http.Request) {
	switch {
	case strings.Contains(r.URL.Path, "/negotiate"):
		TestNegotiate(w, r)
	case strings.Contains(r.URL.Path, "/connect"):
		TestConnect(w, r)
	case strings.Contains(r.URL.Path, "/reconnect"):
		TestReconnect(w, r)
	case strings.Contains(r.URL.Path, "/start"):
		TestStart(w, r)
	default:
		log.Println("url:", r.URL)
	}
}

// TestCompleteHandler combines the negotiate, connect, reconnect, and start
// handlers found in this package into one complete response handler.
func TestCompleteHandler(w http.ResponseWriter, r *http.Request) {
	switch {
	case strings.Contains(r.URL.Path, "/negotiate"):
		TestNegotiate(w, r)
	case strings.Contains(r.URL.Path, "/connect"):
		TestConnect(w, r)
	case strings.Contains(r.URL.Path, "/reconnect"):
		TestReconnect(w, r)
	case strings.Contains(r.URL.Path, "/start"):
		TestStart(w, r)
	}
}

// TestNegotiate provides a sample "/negotiate" handling function.
//
// If an error occurs while writing the response data, it will panic.
func TestNegotiate(w http.ResponseWriter, r *http.Request) {
	// nolint:lll
	_, err := w.Write([]byte(`{"ConnectionToken":"hello world","ConnectionId":"1234-ABC","URL":"/signalr","ProtocolVersion":"1337"}`))
	if err != nil {
		log.Fatal(err)
	}
}

// TestConnect provides a sample "/connect" handling function.
//
// If an error occurs while upgrading the websocket, it will panic.
func TestConnect(w http.ResponseWriter, req *http.Request) {
	upgrader := websocket.Upgrader{}
	c, err := upgrader.Upgrade(w, req, nil)
	if err != nil {
		log.Fatal(err)
	}

	errg, ctx := errgroup.WithContext(req.Context())
	errg.Go(func() error {
		for {
			select {
			case <-ctx.Done():
				return nil
			default:
			}

			if _, _, err := c.ReadMessage(); err != nil {
				return err
			}
		}
	})
	errg.Go(func() error {
		for {
			select {
			case <-ctx.Done():
				return nil
			default:
			}

			if err := c.WriteMessage(websocket.TextMessage, []byte(`{"S":1}`)); err != nil {
				return err
			}
		}
	})

	if err := errg.Wait(); err != nil {
		log.Print(err)
	}
}

// TestReconnect provides a sample "/reconnect" handling function. It simply
// calls TestConnect.
func TestReconnect(w http.ResponseWriter, r *http.Request) {
	TestConnect(w, r)
}

// TestStart provides a sample "/start" handling function.
//
// If an error occurs while writing the response data, it will panic.
func TestStart(w http.ResponseWriter, r *http.Request) {
	_, err := w.Write([]byte(`{"Response":"started"}`))
	if err != nil {
		panic(err)
	}
}
