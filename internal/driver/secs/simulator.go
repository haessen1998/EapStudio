package secs

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

type SimulatorDriver struct {
	equipmentID string
	mu          sync.RWMutex
	state       ConnectionState
	message     MessageHandler
	stateChange StateHandler
	sequence    atomic.Uint32
}

func NewSimulatorDriver(equipmentID string) *SimulatorDriver {
	return &SimulatorDriver{equipmentID: equipmentID, state: StateDisconnected}
}

func (d *SimulatorDriver) Open(ctx context.Context) error {
	d.mu.Lock()
	if d.state == StateSelected {
		d.mu.Unlock()
		return nil
	}
	d.state = StateSelected
	handler := d.stateChange
	d.mu.Unlock()
	if handler != nil {
		handler(StateSelected, "simulated HSMS selected")
	}
	return nil
}

func (d *SimulatorDriver) Close() error {
	d.mu.Lock()
	d.state = StateDisconnected
	handler := d.stateChange
	d.mu.Unlock()
	if handler != nil {
		handler(StateDisconnected, "simulator stopped")
	}
	return nil
}

func (d *SimulatorDriver) State() ConnectionState { d.mu.RLock(); defer d.mu.RUnlock(); return d.state }
func (d *SimulatorDriver) OnMessage(handler MessageHandler) {
	d.mu.Lock()
	d.message = handler
	d.mu.Unlock()
}
func (d *SimulatorDriver) OnState(handler StateHandler) {
	d.mu.Lock()
	d.stateChange = handler
	d.mu.Unlock()
}

func (d *SimulatorDriver) Send(_ context.Context, message Message) (*Message, error) {
	message.ID = d.nextID()
	message.Timestamp = time.Now()
	message.Direction = DirectionOut
	if message.Wait {
		reply := message
		reply.ID = d.nextID()
		reply.Direction = DirectionIn
		reply.Function++
		reply.Wait = false
		ack := uint8(0)
		reply.Ack = &ack
		reply.SML = fmt.Sprintf("S%dF%d\n<B 0>\n.", reply.Stream, reply.Function)
		reply.Tree = reply.SML
		return &reply, nil
	}
	return nil, nil
}

func (d *SimulatorDriver) Reply(_ context.Context, _ Message, _ Message) error { return nil }

// Deliver injects a Profile-built equipment message into the normal inbound path.
func (d *SimulatorDriver) Deliver(message Message) error {
	if d.State() != StateSelected {
		return fmt.Errorf("simulator %s is not connected", d.equipmentID)
	}
	sequence := d.sequence.Add(1)
	message.ID = fmt.Sprintf("msg-%s-%06d", d.equipmentID, sequence)
	message.EquipmentID = d.equipmentID
	message.Direction = DirectionIn
	message.Timestamp = time.Now()
	message.SystemBytes = sequence
	d.mu.RLock()
	handler := d.message
	d.mu.RUnlock()
	if handler != nil {
		handler(message)
	}
	return nil
}

func (d *SimulatorDriver) nextID() string {
	return fmt.Sprintf("msg-%s-%06d", d.equipmentID, d.sequence.Add(1))
}
