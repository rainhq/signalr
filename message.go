package signalr

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
)

var (
	textMessage   = 1
	statusStarted = 1
)

// Message represents a message sent from the server to the persistent websocket
// connection.
type Message struct {
	// message id, present for all non-KeepAlive messages
	MessageID string `json:"C"`

	// groups token – an encrypted string representing group membership
	GroupsToken string `json:"G"`

	InvocationID int `json:"I,string"`

	// an array containing actual data
	Messages []ClientMsg `json:"M"`

	// indicates that the transport was initialized (a.k.a. init message)
	Status int `json:"S"`

	// error
	Error       string                  `json:"E"`
	ErrorDetail *map[string]interface{} `json:"D"`
	HubError    bool                    `json:"H"`

	// result
	Result interface{} `json:"R"`
}

// ClientMsg represents a message sent to the Hubs API from the client.
type ClientMsg struct {
	// invocation identifier – allows to match up responses with requests
	InvocationID int `json:"I"`

	// the name of the hub
	Hub string `json:"H"`

	// the name of the method
	Method string `json:"M"`

	// arguments (an array, can be empty if the method does not have any
	// parameters)
	Args []interface{} `json:"A"`

	// state – a dictionary containing additional custom data (optional)
	State *json.RawMessage `json:"S,omitempty"`
}

// ServerMsg represents a message sent to the Hubs API from the server.
type ServerMsg struct {
	// invocation Id (always present)
	I int

	// the value returned by the server method (present if the method is not
	// void)
	R *json.RawMessage `json:",omitempty"`

	// error message
	E *string `json:",omitempty"`

	// true if this is a hub error
	H *bool `json:",omitempty"`

	// an object containing additional error data (can only be present for
	// hub errors)
	D *json.RawMessage `json:",omitempty"`

	// stack trace (if detailed error reporting (i.e. the
	// HubConfiguration.EnableDetailedErrors property) is turned on on the
	// server)
	T *json.RawMessage `json:",omitempty"`

	// state – a dictionary containing additional custom data (optional)
	S *json.RawMessage `json:",omitempty"`
}

func readMessage(ctx context.Context, conn WebsocketConn, msg *Message, state *State) error {
	for {
		t, p, err := conn.ReadMessage(ctx)
		if err != nil {
			return fmt.Errorf("message read failed: %w", err)
		}

		if t != textMessage {
			return fmt.Errorf("unexpected websocket control type: %d", t)
		}

		// skip empty messages
		if bytes.Equal(p, []byte("{}")) {
			continue
		}

		if err := json.Unmarshal(p, msg); err != nil {
			return err
		}

		// Update the groups token.
		if msg.GroupsToken != "" {
			state.GroupsToken = msg.GroupsToken
		}

		// Update the current message ID.
		if msg.MessageID != "" {
			state.MessageID = msg.MessageID
		}

		return nil
	}
}

type negotiateResponse struct {
	URL                     string  `json:"Url"`
	ConnectionToken         string  `json:"ConnectionToken"`
	ConnectionID            string  `json:"ConnectionId"`
	KeepAliveTimeout        float64 `json:"KeepAliveTimeout"`
	DisconnectTimeout       float64 `json:"DisconnectTimeout"`
	ConnectionTimeout       float64 `json:"ConnectionTimeout"`
	TryWebSockets           bool    `json:"TryWebSockets"`
	ProtocolVersion         string  `json:"ProtocolVersion"`
	TransportConnectTimeout float64 `json:"TransportConnectTimeout"`
	LongPollDelay           float64 `json:"LongPollDelay"`
}

type startResponse struct {
	Response string `json:"Response"`
}
