package sink

import (
	"context"
	"sync"
	"time"

	"eapstudio/internal/event"
)

type Sink interface {
	Name() string
	Send(context.Context, event.Event) error
}

type Delivery struct {
	Sink      string    `json:"sink"`
	EventID   string    `json:"eventId"`
	EventName string    `json:"eventName"`
	Status    string    `json:"status"`
	Timestamp time.Time `json:"timestamp"`
}

type MemorySink struct {
	name       string
	mu         sync.RWMutex
	deliveries []Delivery
}

func NewMemory(name string) *MemorySink { return &MemorySink{name: name} }
func (s *MemorySink) Name() string      { return s.name }
func (s *MemorySink) Send(_ context.Context, value event.Event) error {
	s.mu.Lock()
	s.deliveries = append(s.deliveries, Delivery{Sink: s.name, EventID: value.ID, EventName: value.Name, Status: "delivered", Timestamp: time.Now()})
	if len(s.deliveries) > 100 {
		s.deliveries = append([]Delivery(nil), s.deliveries[len(s.deliveries)-100:]...)
	}
	s.mu.Unlock()
	return nil
}
func (s *MemorySink) Deliveries() []Delivery {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]Delivery, len(s.deliveries))
	copy(result, s.deliveries)
	return result
}
