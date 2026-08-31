package router

import (
	"context"
	"fmt"
	"io/fs"
	"strings"
	"sync"

	"eapstudio/internal/event"
	"eapstudio/internal/sink"
	"gopkg.in/yaml.v3"
)

type Config struct {
	Routes []Rule `yaml:"routes" json:"routes"`
}
type Rule struct {
	Name  string   `yaml:"name" json:"name"`
	Match Match    `yaml:"match" json:"match"`
	Sinks []string `yaml:"sinks" json:"sinks"`
}
type Match struct {
	Names []string `yaml:"names" json:"names"`
}

type Router struct {
	mu         sync.RWMutex
	rules      []Rule
	sinks      map[string]sink.Sink
	deliveries []sink.Delivery
}

func Load(source fs.FS, path string, sinks ...sink.Sink) (*Router, error) {
	data, err := fs.ReadFile(source, path)
	if err != nil {
		return nil, fmt.Errorf("read routes: %w", err)
	}
	var config Config
	decoder := yaml.NewDecoder(strings.NewReader(string(data)))
	decoder.KnownFields(true)
	if err := decoder.Decode(&config); err != nil {
		return nil, fmt.Errorf("decode routes: %w", err)
	}
	router := &Router{rules: config.Routes, sinks: map[string]sink.Sink{}}
	for _, output := range sinks {
		router.sinks[output.Name()] = output
	}
	for _, rule := range router.rules {
		for _, name := range rule.Sinks {
			if _, ok := router.sinks[name]; !ok {
				return nil, fmt.Errorf("route %q references unknown sink %q", rule.Name, name)
			}
		}
	}
	return router, nil
}

func (r *Router) Route(ctx context.Context, value event.Event) error {
	for _, rule := range r.rules {
		if !matches(rule.Match.Names, value.Name) {
			continue
		}
		for _, name := range rule.Sinks {
			status := "delivered"
			if err := r.sinks[name].Send(ctx, value); err != nil {
				status = "error: " + err.Error()
			}
			r.mu.Lock()
			r.deliveries = append(r.deliveries, sink.Delivery{Sink: name, EventID: value.ID, EventName: value.Name, Status: status, Timestamp: value.Timestamp})
			if len(r.deliveries) > 100 {
				r.deliveries = append([]sink.Delivery(nil), r.deliveries[len(r.deliveries)-100:]...)
			}
			r.mu.Unlock()
		}
	}
	return nil
}
func (r *Router) Rules() []Rule {
	result := make([]Rule, len(r.rules))
	copy(result, r.rules)
	return result
}
func (r *Router) Deliveries() []sink.Delivery {
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make([]sink.Delivery, len(r.deliveries))
	copy(result, r.deliveries)
	return result
}
func matches(patterns []string, value string) bool {
	for _, pattern := range patterns {
		if pattern == "*" || pattern == value {
			return true
		}
	}
	return false
}
