package secs

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/arloliu/go-secs/v2/gem"
	"github.com/arloliu/go-secs/v2/hsms"
	"github.com/arloliu/go-secs/v2/hsmsss"
	"github.com/arloliu/go-secs/v2/secs2"
	"github.com/arloliu/go-secs/v2/sml"
)

type GoSecsDriver struct {
	config       ConnectionConfig
	connection   hsmsss.Connection
	mu           sync.RWMutex
	state        ConnectionState
	message      MessageHandler
	stateChanged StateHandler
}

func NewGoSecsDriver(config ConnectionConfig) (*GoSecsDriver, error) {
	connectionOptions := []hsms.ConnOption{hsms.WithSessionID(config.SessionID)}
	if config.ReplyTimeout > 0 {
		connectionOptions = append(connectionOptions, hsms.WithT3(config.ReplyTimeout))
	}
	options := []hsmsss.Option{hsmsss.WithActive()}
	for _, option := range connectionOptions {
		options = append(options, hsmsss.WithConnectionOption(option))
	}
	if config.ConnectTimeout > 0 {
		options = append(options, hsmsss.WithConnectTimeout(config.ConnectTimeout))
	}
	if strings.EqualFold(config.Mode, "passive") {
		options[0] = hsmsss.WithPassive()
	}
	sdkConfig, err := hsmsss.NewConfig(config.Host, config.Port, options...)
	if err != nil {
		return nil, err
	}
	connection, err := hsmsss.New(sdkConfig)
	if err != nil {
		return nil, err
	}
	driver := &GoSecsDriver{config: config, connection: connection, state: StateDisconnected}
	connection.AddDataMessageHandler(driver.receive)
	connection.SubscribeLifecycle(func(value hsms.LifecycleEvent) {
		detail := fmt.Sprintf("%s: %s → %s", value.Cause, value.Previous, value.Current)
		state := mapSDKState(value.Current)
		if value.Current == hsms.NotConnectedState && value.Cause != hsms.CauseLocalClose {
			// OpenBackground owns the reconnect supervisor. A transport drop means the
			// driver is reconnecting, not closed and ready for another Open call.
			state = StateConnecting
		}
		driver.updateState(state, detail)
	})
	return driver, nil
}

func (d *GoSecsDriver) Open(ctx context.Context) error {
	state := d.connection.State()
	if state != hsms.NotConnectedState {
		d.updateState(mapSDKState(state), "connection already open")
		return nil
	}
	detail := fmt.Sprintf("opening HSMS %s session", strings.ToLower(d.config.Mode))
	if d.config.ConnectTimeout > 0 || d.config.ReplyTimeout > 0 {
		detail += fmt.Sprintf(" (connect %s, T3 %s)", d.config.ConnectTimeout, d.config.ReplyTimeout)
	}
	d.updateState(StateConnecting, detail)
	if err := d.connection.Open(ctx, hsms.OpenBackground); err != nil {
		if errors.Is(err, hsms.ErrAlreadyOpen) {
			// The SDK deliberately reports ErrAlreadyOpen while its supervisor is
			// reconnecting even though the instantaneous FSM state is NotConnected.
			// Treat Connect as idempotent and let that supervisor finish its work.
			d.updateState(StateConnecting, "HSMS lifecycle is already open; waiting for selection")
			return nil
		}
		d.updateState(StateError, err.Error())
		return err
	}
	return nil
}

func (d *GoSecsDriver) Close() error {
	err := d.connection.Close()
	d.updateState(StateDisconnected, "closed")
	return err
}

func (d *GoSecsDriver) State() ConnectionState {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.state
}

func (d *GoSecsDriver) ProtocolDiagnostics() ProtocolDiagnostics {
	metrics := d.connection.Metrics()
	control := d.connection.ControlMetrics()
	return ProtocolDiagnostics{
		DataSent: metrics.DataMsgSendCount(), DataReceived: metrics.DataMsgRecvCount(), DataErrors: metrics.DataMsgErrCount(),
		DecodeErrors: metrics.DecodeErrCount() + metrics.BodyDecodeErrCount(), ReplyMismatches: metrics.ReplyMismatchCount(),
		Reconnects: metrics.Reconnects(), Inflight: metrics.DataMsgInflightCount(), LinktestSent: control.LinktestSendCount(),
		LinktestReceived: control.LinktestRecvCount(), LinktestErrors: control.LinktestErrCount(), SeparateReceived: control.SeparateRecvCount(),
		RejectSent: control.RejectSentCount(), RejectReceived: control.RejectRecvCount(),
	}
}

func (d *GoSecsDriver) OnMessage(handler MessageHandler) {
	d.mu.Lock()
	d.message = handler
	d.mu.Unlock()
}
func (d *GoSecsDriver) OnState(handler StateHandler) {
	d.mu.Lock()
	d.stateChanged = handler
	d.mu.Unlock()
}

func (d *GoSecsDriver) Send(ctx context.Context, message Message) (*Message, error) {
	item := secs2.NewEmptyItem()
	if strings.TrimSpace(message.SML) != "" {
		parsed, err := sml.Parse(message.SML)
		if err != nil || len(parsed) != 1 {
			return nil, fmt.Errorf("parse outbound SML: %w", err)
		}
		item, err = parsed[0].Item()
		if err != nil {
			return nil, err
		}
	}
	reply, err := d.connection.SendDataMessage(ctx, message.Stream, message.Function, message.Wait, item)
	if err != nil {
		return nil, err
	}
	if reply == nil {
		return nil, nil
	}
	converted := fromSDK(reply)
	return &converted, nil
}

func (d *GoSecsDriver) Reply(ctx context.Context, request Message, response Message) error {
	raw, ok := request.rawValue().(*hsms.DataMessage)
	if !ok {
		return fmt.Errorf("request is not backed by an HSMS message")
	}
	item := secs2.NewEmptyItem()
	if strings.TrimSpace(response.SML) != "" {
		parsed, err := sml.Parse(response.SML)
		if err != nil {
			return fmt.Errorf("parse reply SML: %w", err)
		}
		if len(parsed) != 1 {
			return fmt.Errorf("reply SML must contain exactly one message")
		}
		var itemErr error
		item, itemErr = parsed[0].Item()
		if itemErr != nil {
			return itemErr
		}
	}
	return d.connection.ReplyDataMessage(ctx, raw, item)
}

func (d *GoSecsDriver) receive(message *hsms.DataMessage, _ hsms.SECS2Endpoint) {
	converted := fromSDK(message)
	d.mu.RLock()
	handler := d.message
	d.mu.RUnlock()
	if handler != nil {
		handler(converted)
	}
}

func (d *GoSecsDriver) updateState(state ConnectionState, detail string) {
	d.mu.Lock()
	d.state = state
	handler := d.stateChanged
	d.mu.Unlock()
	if handler != nil {
		handler(state, detail)
	}
}

func mapSDKState(state hsms.ConnState) ConnectionState {
	switch state {
	case hsms.SelectedState:
		return StateSelected
	case hsms.NotSelectedState:
		return StateConnecting
	default:
		return StateDisconnected
	}
}

func fromSDK(message *hsms.DataMessage) Message {
	converted := Message{
		ID:          fmt.Sprintf("msg-%08x", message.ID()),
		Timestamp:   time.Now(),
		Direction:   DirectionIn,
		Stream:      message.Stream(),
		Function:    message.Function(),
		Wait:        message.WaitBit(),
		SystemBytes: message.ID(),
		Reports:     map[uint64][]any{},
		Fields:      map[string]any{},
		RawHex:      hex.EncodeToString(message.ToBytes()),
	}
	converted.setRaw(message)
	if text, err := sml.EncodeMessage(message); err == nil {
		converted.SML = text
		converted.Tree = text
	}
	item, err := message.Item()
	if err == nil && message.Function()%2 == 0 {
		if ack, ackErr := messageAck(message.Stream(), message.Function(), item); ackErr == nil {
			converted.Ack = &ack
		}
	}
	if err == nil && message.Stream() == 6 && message.Function() == 11 {
		if body, decodeErr := gem.DecodeS6F11(item); decodeErr == nil {
			converted.CEID, _ = scalarUint(body.CEID)
			for _, report := range body.REPORTS {
				parts, listErr := report.ToList()
				if listErr != nil || len(parts) != 2 {
					continue
				}
				reportID, idErr := scalarUint(parts[0])
				values, valuesErr := parts[1].ToList()
				if idErr != nil || valuesErr != nil {
					continue
				}
				for _, value := range values {
					converted.Reports[reportID] = append(converted.Reports[reportID], itemValue(value))
				}
			}
		}
	}
	if err == nil && message.Stream() == 5 && message.Function() == 1 {
		if body, decodeErr := gem.DecodeS5F1(item); decodeErr == nil {
			alarmID, _ := scalarUint(body.ALID)
			converted.Fields["alarmId"] = fmt.Sprint(alarmID)
			converted.Fields["code"] = fmt.Sprint(body.ALCD)
			converted.Fields["text"] = body.ALTX
			converted.Fields["active"] = body.ALCD&0x80 != 0
			converted.Fields["severity"] = alarmSeverity(body.ALCD)
		}
	}
	return converted
}

func messageAck(stream, function uint8, item secs2.Item) (uint8, error) {
	if stream == 2 && function == 42 {
		body, err := gem.DecodeS2F42(item)
		if err != nil {
			return 0, err
		}
		return uint8(body.HCACK), nil
	}
	return scalarByte(item)
}

func alarmSeverity(code byte) string {
	switch code & 0x7f {
	case 1, 2:
		return "critical"
	case 3, 4:
		return "warning"
	default:
		return "info"
	}
}

func scalarByte(item secs2.Item) (uint8, error) {
	if values, err := item.ToBinary(); err == nil && len(values) > 0 {
		return values[0], nil
	}
	value, err := scalarUint(item)
	if err != nil || value > 255 {
		return 0, fmt.Errorf("expected byte acknowledgement")
	}
	return uint8(value), nil
}

func scalarUint(item secs2.Item) (uint64, error) {
	if values, err := item.ToUint(); err == nil && len(values) > 0 {
		return values[0], nil
	}
	if values, err := item.ToInt(); err == nil && len(values) > 0 && values[0] >= 0 {
		return uint64(values[0]), nil
	}
	return 0, fmt.Errorf("expected numeric scalar, got %s", item.Type())
}

func itemValue(item secs2.Item) any {
	if value, err := item.ToASCII(); err == nil {
		return value
	}
	if value, err := item.ToUint(); err == nil {
		if len(value) == 1 {
			return value[0]
		}
		return value
	}
	if value, err := item.ToInt(); err == nil {
		if len(value) == 1 {
			return value[0]
		}
		return value
	}
	if value, err := item.ToFloat(); err == nil {
		if len(value) == 1 {
			return value[0]
		}
		return value
	}
	if value, err := item.ToBoolean(); err == nil {
		if len(value) == 1 {
			return value[0]
		}
		return value
	}
	return fmt.Sprint(item)
}
