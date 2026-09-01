package equipment

import (
	"fmt"
	"strings"
	"sync"
)

type AdapterFactory func() Adapter

var adapterRegistry = struct {
	sync.RWMutex
	factories map[string]AdapterFactory
}{factories: map[string]AdapterFactory{
	"generic-gem": func() Adapter { return GenericGemAdapter{} },
}}

// RegisterAdapter is the extension point for model- or vendor-specific adapters.
func RegisterAdapter(name string, factory AdapterFactory) error {
	name = strings.TrimSpace(name)
	if name == "" || factory == nil {
		return fmt.Errorf("adapter name and factory are required")
	}
	adapterRegistry.Lock()
	defer adapterRegistry.Unlock()
	if _, exists := adapterRegistry.factories[name]; exists {
		return fmt.Errorf("adapter %q is already registered", name)
	}
	adapterRegistry.factories[name] = factory
	return nil
}

func NewAdapter(name string) (Adapter, error) {
	if strings.TrimSpace(name) == "" {
		name = "generic-gem"
	}
	adapterRegistry.RLock()
	factory, exists := adapterRegistry.factories[name]
	adapterRegistry.RUnlock()
	if !exists {
		return nil, fmt.Errorf("unknown equipment adapter %q", name)
	}
	return factory(), nil
}
