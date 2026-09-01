package automation

import (
	"fmt"
	"io/fs"
	pathpkg "path"
	"strings"
	"sync"
	"time"

	"eapstudio/internal/command"
	"eapstudio/internal/domain"
	"eapstudio/internal/event"
	"gopkg.in/yaml.v3"
)

type Config struct {
	Automations []Rule `yaml:"automations" json:"automations"`
}

type Rule struct {
	Name       string            `yaml:"name" json:"name"`
	Trigger    string            `yaml:"trigger" json:"trigger"`
	Equipment  []string          `yaml:"equipment,omitempty" json:"equipment,omitempty"`
	Command    string            `yaml:"command" json:"command"`
	Parameters map[string]string `yaml:"parameters" json:"parameters"`
}

type Engine struct {
	mu    sync.RWMutex
	rules []Rule
}

func Load(source fs.FS, path string) (*Engine, error) {
	rules, err := ReadRules(source, path)
	if err != nil {
		return nil, err
	}
	return &Engine{rules: rules}, nil
}

func ReadRules(source fs.FS, filePath string) ([]Rule, error) {
	data, err := fs.ReadFile(source, filePath)
	if err != nil {
		return nil, fmt.Errorf("read automations: %w", err)
	}
	var config Config
	decoder := yaml.NewDecoder(strings.NewReader(string(data)))
	decoder.KnownFields(true)
	if err := decoder.Decode(&config); err != nil {
		return nil, fmt.Errorf("decode automations: %w", err)
	}
	for _, rule := range config.Automations {
		if rule.Name == "" {
			return nil, fmt.Errorf("automation name is required")
		}
		if hasGlob(rule.Trigger) {
			if _, err := pathpkg.Match(rule.Trigger, ""); err != nil {
				return nil, fmt.Errorf("automation %q trigger pattern: %w", rule.Name, err)
			}
		} else if err := domain.ValidateName(domain.TypeEvent, rule.Trigger); err != nil {
			return nil, fmt.Errorf("automation %q trigger: %w", rule.Name, err)
		}
		for _, pattern := range rule.Equipment {
			if _, err := pathpkg.Match(pattern, ""); err != nil {
				return nil, fmt.Errorf("automation %q equipment pattern %q: %w", rule.Name, pattern, err)
			}
		}
		if err := domain.ValidateName(domain.TypeCommand, rule.Command); err != nil {
			return nil, fmt.Errorf("automation %q command: %w", rule.Name, err)
		}
	}
	return config.Automations, nil
}

func (e *Engine) Reload(source fs.FS, filePath string) error {
	rules, err := ReadRules(source, filePath)
	if err != nil {
		return err
	}
	e.ReplaceRules(rules)
	return nil
}

func (e *Engine) ReplaceRules(rules []Rule) {
	e.mu.Lock()
	e.rules = append([]Rule(nil), rules...)
	e.mu.Unlock()
}

func (e *Engine) Handle(value event.Event) []command.Command {
	if value.Type != domain.TypeEvent {
		return nil
	}
	var commands []command.Command
	e.mu.RLock()
	rules := append([]Rule(nil), e.rules...)
	e.mu.RUnlock()
	for _, rule := range rules {
		triggered, _ := pathpkg.Match(rule.Trigger, value.Name)
		if !triggered || !matchesEquipment(rule.Equipment, value.EquipmentID) {
			continue
		}
		parameters := make(map[string]any, len(rule.Parameters))
		for target, source := range rule.Parameters {
			parameters[target] = value.Data[source]
		}
		correlationID := value.CorrelationID
		if correlationID == "" {
			correlationID = "flow-" + value.ID
		}
		commands = append(commands, command.Command{
			ID:   "cmd-" + value.ID + "-" + strings.ReplaceAll(rule.Name, "_", "-"),
			Type: domain.TypeCommand, Name: rule.Command, EquipmentID: value.EquipmentID,
			CorrelationID: correlationID, CausationID: value.ID, Status: command.StatusPending,
			CreatedAt: time.Now(), Parameters: parameters,
		})
	}
	return commands
}

func hasGlob(value string) bool { return strings.ContainsAny(value, "*?[") }

func matchesEquipment(patterns []string, equipmentID string) bool {
	if len(patterns) == 0 {
		return true
	}
	for _, pattern := range patterns {
		matched, _ := pathpkg.Match(pattern, equipmentID)
		if matched {
			return true
		}
	}
	return false
}

func (e *Engine) Rules() []Rule {
	e.mu.RLock()
	defer e.mu.RUnlock()
	result := make([]Rule, len(e.rules))
	copy(result, e.rules)
	return result
}
