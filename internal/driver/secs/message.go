package secs

import (
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/arloliu/go-secs/v2/hsms"
	"github.com/arloliu/go-secs/v2/sml"
)

type Direction string

const (
	DirectionIn  Direction = "IN"
	DirectionOut Direction = "OUT"
)

// Message is the SDK-neutral representation used outside this package.
type Message struct {
	ID          string            `json:"id"`
	EquipmentID string            `json:"equipmentId"`
	Direction   Direction         `json:"direction"`
	Timestamp   time.Time         `json:"timestamp"`
	Stream      uint8             `json:"stream"`
	Function    uint8             `json:"function"`
	Wait        bool              `json:"wait"`
	SystemBytes uint32            `json:"systemBytes"`
	SML         string            `json:"sml"`
	Tree        string            `json:"tree"`
	RawHex      string            `json:"rawHex,omitempty"`
	CEID        uint64            `json:"ceid,omitempty"`
	Fields      map[string]any    `json:"fields,omitempty"`
	Reports     map[uint64][]any  `json:"reports,omitempty"`
	Ack         *uint8            `json:"ack,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty"`
	raw         any
}

func (m Message) Name() string { return "S" + itoa(m.Stream) + "F" + itoa(m.Function) }

// ParseOutboundSML validates one complete SML message and converts its header
// into the SDK-neutral representation used by DeviceRuntime. The go-secs
// driver parses the item again at send time; keeping validation here prevents
// malformed operator input from reaching a live connection.
func ParseOutboundSML(text string) (Message, error) {
	parsed, err := sml.Parse(text)
	if err != nil {
		return Message{}, fmt.Errorf("parse outbound SML: %w", err)
	}
	if len(parsed) != 1 {
		return Message{}, fmt.Errorf("outbound SML must contain exactly one message")
	}
	message := parsed[0]
	return Message{
		Direction: DirectionOut,
		Timestamp: time.Now(),
		Stream:    message.Stream(),
		Function:  message.Function(),
		Wait:      message.WaitBit(),
		SML:       text,
		Tree:      text,
	}, nil
}

// PopulateRawHex renders SDK-neutral simulator messages into the same complete
// HSMS wire-frame representation captured by the real go-secs driver.
func PopulateRawHex(message *Message) error {
	if message == nil || message.RawHex != "" || message.SML == "" {
		return nil
	}
	parsed, err := sml.NewParser().ParseMessage(message.SML)
	if err != nil {
		return fmt.Errorf("parse SML for raw frame: %w", err)
	}
	item, err := parsed.Item()
	if err != nil {
		return fmt.Errorf("decode SML item for raw frame: %w", err)
	}
	var systemBytes [4]byte
	binary.BigEndian.PutUint32(systemBytes[:], message.SystemBytes)
	wire, err := hsms.NewDataMessage(message.Stream, message.Function, message.Wait, 0, systemBytes, item)
	if err != nil {
		return fmt.Errorf("build raw HSMS frame: %w", err)
	}
	message.RawHex = hex.EncodeToString(wire.ToBytes())
	return nil
}

func (m *Message) setRaw(value any) { m.raw = value }
func (m Message) rawValue() any     { return m.raw }

func itoa(value uint8) string {
	if value < 10 {
		return string(rune('0' + value))
	}
	return string(rune('0'+value/10)) + string(rune('0'+value%10))
}
