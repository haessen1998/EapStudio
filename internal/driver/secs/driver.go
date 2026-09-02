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

type ProtocolDiagnostics struct {
	DataSent         uint64 `json:"dataSent"`
	DataReceived     uint64 `json:"dataReceived"`
	DataErrors       uint64 `json:"dataErrors"`
	DecodeErrors     uint64 `json:"decodeErrors"`
	ReplyMismatches  uint64 `json:"replyMismatches"`
	Reconnects       uint64 `json:"reconnects"`
	Inflight         int64  `json:"inflight"`
	LinktestSent     uint64 `json:"linktestSent"`
	LinktestReceived uint64 `json:"linktestReceived"`
	LinktestErrors   uint64 `json:"linktestErrors"`
	SeparateReceived uint64 `json:"separateReceived"`
	RejectSent       uint64 `json:"rejectSent"`
	RejectReceived   uint64 `json:"rejectReceived"`
}

type DiagnosticProvider interface {
	ProtocolDiagnostics() ProtocolDiagnostics
}
