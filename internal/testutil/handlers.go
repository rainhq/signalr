package testutil

import (
	"encoding/json"
	"log"
	"net/http"
	"strings"

	"github.com/gorilla/websocket"
	"github.com/rainhq/signalr/v2/internal/model"
	"golang.org/x/sync/errgroup"
)

var (
	ConnectionToken = "hello world"
	ConnectionID    = "ConnectionId"
	ProtocolVersion = "1337"
)

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
	data, err := json.Marshal(model.NegotiateResponse{
		ConnectionToken: ConnectionToken,
		ConnectionID:    ConnectionID,
		ProtocolVersion: ProtocolVersion,
	})
	if err != nil {
		log.Fatal(err)
	}

	if _, err := w.Write(data); err != nil {
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
		w.WriteHeader(http.StatusInternalServerError)
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
	data, err := json.Marshal(model.StartResponse{
		Response: "started",
	})
	if err != nil {
		log.Fatal(err)
	}

	if _, err := w.Write(data); err != nil {
		log.Fatal(err)
	}
}
