package signalr

import (
	"encoding/json"
	"fmt"

	"github.com/rainhq/signalr/v2/hubs"
)

// Message represents a message sent from the sserver to the persistent websocket
// connection.
type Message struct {
	// message id, present for all non-KeepAlive messages
	C string

	// an array containing actual data
	M []hubs.ClientMsg

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

func readMessage(conn WebsocketConn, msg *Message, state *State) error {
	t, p, err := conn.ReadMessage()
	if err != nil {
		return fmt.Errorf("message read failed: %w", err)
	}

	// Verify the correct response type was received.
	if t != 1 {
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
