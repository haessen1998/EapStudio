package router

import (
	"context"
	"fmt"
	"io/fs"
	pathpkg "path"
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
	Names     []string `yaml:"names" json:"names"`
	Equipment []string `yaml:"equipment,omitempty" json:"equipment,omitempty"`
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
		if rule.Name == "" {
			return nil, fmt.Errorf("route name is required")
		}
		if len(rule.Match.Names) == 0 {
			return nil, fmt.Errorf("route %q requires at least one event pattern", rule.Name)
		}
		for _, pattern := range append(append([]string{}, rule.Match.Names...), rule.Match.Equipment...) {
			if _, err := pathpkg.Match(pattern, ""); err != nil {
				return nil, fmt.Errorf("route %q has invalid pattern %q: %w", rule.Name, pattern, err)
			}
		}
		for _, name := range rule.Sinks {
			if _, ok := router.sinks[name]; !ok {
				return nil, fmt.Errorf("route %q references unknown sink %q", rule.Name, name)
			}
		}
	}
	return router, nil
}

func (r *Router) Route(ctx context.Context, value event.Event) error {
	delivered := map[string]struct{}{}
	for _, rule := range r.rules {
		if !matches(rule.Match.Names, value.Name) || !matchesOptional(rule.Match.Equipment, value.EquipmentID) {
			continue
		}
		for _, name := range rule.Sinks {
			if _, exists := delivered[name]; exists {
				continue
			}
			delivered[name] = struct{}{}
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
		matched, err := pathpkg.Match(pattern, value)
		if err == nil && matched {
			return true
		}
	}
	return false
}

func matchesOptional(patterns []string, value string) bool {
	return len(patterns) == 0 || matches(patterns, value)
}
