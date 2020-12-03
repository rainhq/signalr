package signalr

import (
	"encoding/json"
	"errors"
	"fmt"
)

var (
	textMessage   = 1
	statusStarted = 1
)

// Message represents a message sent from the sserver to the persistent websocket
// connection.
type Message struct {
	// message id, present for all non-KeepAlive messages
	C string

	// an array containing actual data
	M []ClientMsg

	// indicates that the transport was initialized (a.k.a. init message)
	S int

	// groups token – an encrypted string representing group membership
	G string

	// other miscellaneous variables that sometimes are sent by the server
	I string
	E string
	R json.RawMessage
	H json.RawMessage // could be bool or string depending on a message type
	D json.RawMessage
	T json.RawMessage
}

// ClientMsg represents a message sent to the Hubs API from the client.
type ClientMsg struct {
	// invocation identifier – allows to match up responses with requests
	I int

	// the name of the hub
	H string

	// the name of the method
	M string

	// arguments (an array, can be empty if the method does not have any
	// parameters)
	A []interface{}

	// state – a dictionary containing additional custom data (optional)
	S *json.RawMessage `json:",omitempty"`
}

// MarshalJSON converts the current message into a JSON-formatted byte array. It
// will perform different types of conversion based on the Golang type of the
// "A" field. For instance, an array will be converted into a JSON object
// looking like [...], whereas a byte array would look like "...".
func (cm *ClientMsg) MarshalJSON() (buf []byte, err error) {
	var args []byte
	for _, a := range cm.A {
		switch a := a.(type) {
		case []byte:
			args = append(args, a...)
		case string:
			args = append(args, a...)
		default:
			err = errors.New("unsupported argument type")
			return
		}
	}

	return json.Marshal(&struct {
		I int
		H string
		M string
		A []byte
		S *json.RawMessage `json:"omitempty"`
	}{
		I: cm.I,
		H: cm.H,
		M: cm.M,
		A: args,
		S: cm.S,
	})
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

func readMessage(conn WebsocketConn, msg *Message, state *State) error {
	t, p, err := conn.ReadMessage()
	if err != nil {
		return fmt.Errorf("message read failed: %w", err)
	}

	// Verify the correct response type was received.
	if t != textMessage {
		return fmt.Errorf("unexpected websocket control type: %d", t)
	}

	if err := json.Unmarshal(p, msg); err != nil {
		return err
	}

	// Update the groups token.
	if msg.G != "" {
		state.GroupsToken = msg.G
	}

	// Update the current message ID.
	if msg.C != "" {
		state.MessageID = msg.C
	}

	return nil
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
