package secs

import "context"

type ConnectionState string

const (
	StateDisconnected ConnectionState = "disconnected"
	StateConnecting   ConnectionState = "connecting"
	StateSelected     ConnectionState = "selected"
	StateError        ConnectionState = "error"
)

type MessageHandler func(Message)
type StateHandler func(ConnectionState, string)

// Driver is the anti-corruption boundary around go-secs and the demo simulator.
type Driver interface {
	Open(context.Context) error
	Close() error
	State() ConnectionState
	Send(context.Context, Message) (*Message, error)
	Reply(context.Context, Message, Message) error
	OnMessage(MessageHandler)
	OnState(StateHandler)
}

type ConnectionConfig struct {
	Host      string
	Port      int
	Mode      string
	SessionID uint16
}
