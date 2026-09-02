package secs

import (
	"context"
	"net"
	"testing"
	"time"
)

func TestGoSecsPassiveTwinExchangesPrimaryAndSecondary(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()

	config := func(mode string) ConnectionConfig {
		return ConnectionConfig{Host: "127.0.0.1", Port: port, Mode: mode, SessionID: 0, ConnectTimeout: time.Second, ReplyTimeout: 2 * time.Second}
	}
	passive, err := NewGoSecsDriver(config("passive"))
	if err != nil {
		t.Fatal(err)
	}
	active, err := NewGoSecsDriver(config("active"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = active.Close()
		_ = passive.Close()
	})
	passive.OnMessage(func(request Message) {
		sml := "S1F2\n."
		if request.Stream == 2 && request.Function == 41 {
			sml = "S2F42\n<L[2]\n  <B 0>\n  <L[0]>\n>\n."
		}
		reply := Message{Stream: request.Stream, Function: request.Function + 1, SML: sml}
		_ = passive.Reply(context.Background(), request, reply)
	})
	if err := passive.Open(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := active.Open(context.Background()); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for (passive.State() != StateSelected || active.State() != StateSelected) && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if passive.State() != StateSelected || active.State() != StateSelected {
		t.Fatalf("connections were not selected: passive=%s active=%s", passive.State(), active.State())
	}
	request, err := ParseOutboundSML("S1F1 W\n.")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	reply, err := active.Send(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if reply == nil || reply.Stream != 1 || reply.Function != 2 {
		t.Fatalf("reply = %#v", reply)
	}
	command, err := ParseOutboundSML("S2F41 W\n<L[2]\n  <A \"START\">\n  <L[0]>\n>\n.")
	if err != nil {
		t.Fatal(err)
	}
	commandReply, err := active.Send(ctx, command)
	if err != nil {
		t.Fatal(err)
	}
	if commandReply == nil || commandReply.Ack == nil || *commandReply.Ack != 0 {
		t.Fatalf("S2F42 reply = %#v", commandReply)
	}
	// Connect is idempotent even if the UI state update races a second click.
	if err := active.Open(context.Background()); err != nil {
		t.Fatalf("second Open() = %v", err)
	}

	// A lost peer leaves the SDK supervisor open while the FSM temporarily says
	// NotConnected. The UI may still receive a Connect click in this window; it
	// must remain an idempotent request instead of surfacing ErrAlreadyOpen.
	if err := passive.Close(); err != nil {
		t.Fatal(err)
	}
	reconnectDeadline := time.Now().Add(3 * time.Second)
	for active.State() != StateConnecting && time.Now().Before(reconnectDeadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if active.State() != StateConnecting {
		t.Fatalf("active state after peer loss = %s", active.State())
	}
	if err := active.Open(context.Background()); err != nil {
		t.Fatalf("Open() while reconnecting = %v", err)
	}
}
