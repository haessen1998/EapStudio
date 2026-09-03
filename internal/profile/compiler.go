package profile

import (
	"fmt"
	"io/fs"
	"strings"

	"eapstudio/internal/domain"
	"gopkg.in/yaml.v3"
)

func Load(source fs.FS, path string) (*CompiledProfile, error) {
	data, err := fs.ReadFile(source, path)
	if err != nil {
		return nil, fmt.Errorf("read profile %q: %w", path, err)
	}

	return Decode(data)
}

func Decode(data []byte) (*CompiledProfile, error) {
	var document EquipmentProfile
	decoder := yaml.NewDecoder(strings.NewReader(string(data)))
	decoder.KnownFields(true)
	if err := decoder.Decode(&document); err != nil {
		return nil, fmt.Errorf("decode profile: %w", err)
	}
	return Compile(document)
}

func Compile(document EquipmentProfile) (*CompiledProfile, error) {
	if document.APIVersion != "eapstudio/v1alpha1" {
		return nil, fmt.Errorf("unsupported profile apiVersion %q", document.APIVersion)
	}
	if document.Kind != "EquipmentProfile" {
		return nil, fmt.Errorf("unsupported profile kind %q", document.Kind)
	}
	if document.Metadata.Name == "" {
		return nil, fmt.Errorf("profile metadata.name is required")
	}
	if document.Metadata.Adapter == "" {
		document.Metadata.Adapter = "generic-gem"
	}
	if document.Spec.Scenarios == nil {
		document.Spec.Scenarios = map[string]SimulatorScenario{}
	}
	if document.Spec.Commands == nil {
		document.Spec.Commands = map[string]CommandDefinition{}
	}
	if document.Spec.Simulator != nil {
		for name, scenario := range document.Spec.Simulator.Scenarios {
			if strings.EqualFold(scenario.Direction, "outbound") {
				commandName := legacyScenarioCommandName(name)
				if _, exists := document.Spec.Commands[commandName]; !exists {
					document.Spec.Commands[commandName] = CommandDefinition{
						DisplayName:  scenario.DisplayName,
						Stream:       scenario.Message.Stream,
						Function:     scenario.Message.Function,
						Wait:         scenario.Message.Wait,
						SML:          scenario.Message.SML,
						SuccessEvent: "message.accepted",
						FailureEvent: "message.failed",
					}
				}
				continue
			}
			scenario.Direction = ""
			if _, exists := document.Spec.Scenarios[name]; !exists {
				document.Spec.Scenarios[name] = scenario
			}
		}
	}
	document.Spec.Simulator = nil
	for name, scenario := range document.Spec.Scenarios {
		scenario.Direction = ""
		document.Spec.Scenarios[name] = scenario
	}

	compiled := &CompiledProfile{
		EquipmentProfile:  document,
		VariablePositions: make(map[uint64]map[uint64]int, len(document.Spec.Reports)),
		EventsByName:      make(map[string]uint64, len(document.Spec.Events)),
	}
	for reportID, report := range document.Spec.Reports {
		positions := make(map[uint64]int, len(report.Variables))
		for index, variableID := range report.Variables {
			if _, ok := document.Spec.Variables[variableID]; !ok {
				return nil, fmt.Errorf("report %d references unknown variable %d", reportID, variableID)
			}
			positions[variableID] = index
		}
		compiled.VariablePositions[reportID] = positions
	}
	for eventID, definition := range document.Spec.Events {
		if err := domain.ValidateName(domain.TypeEvent, definition.Name); err != nil {
			return nil, fmt.Errorf("event %d: %w", eventID, err)
		}
		if _, exists := compiled.EventsByName[definition.Name]; exists {
			return nil, fmt.Errorf("duplicate event name %q", definition.Name)
		}
		compiled.EventsByName[definition.Name] = eventID
		for name, mapping := range definition.Mapping {
			positions, ok := compiled.VariablePositions[mapping.Report]
			if !ok {
				return nil, fmt.Errorf("event %d field %q references unknown report %d", eventID, name, mapping.Report)
			}
			if _, ok := positions[mapping.Variable]; !ok {
				return nil, fmt.Errorf("event %d field %q references variable %d outside report %d", eventID, name, mapping.Variable, mapping.Report)
			}
		}
	}
	for name, definition := range document.Spec.Commands {
		if err := domain.ValidateName(domain.TypeCommand, name); err != nil {
			return nil, err
		}
		if definition.Function%2 == 0 {
			return nil, fmt.Errorf("command %q must use an odd primary function", name)
		}
		if err := domain.ValidateName(domain.TypeEvent, definition.SuccessEvent); err != nil {
			return nil, fmt.Errorf("command %q successEvent: %w", name, err)
		}
		if err := domain.ValidateName(domain.TypeEvent, definition.FailureEvent); err != nil {
			return nil, fmt.Errorf("command %q failureEvent: %w", name, err)
		}
	}
	for scenarioName, scenario := range document.Spec.Scenarios {
		if scenario.Event != "" {
			if _, ok := compiled.EventsByName[scenario.Event]; !ok {
				return nil, fmt.Errorf("simulator scenario %q references unknown event %q", scenarioName, scenario.Event)
			}
			continue
		}
		if scenario.Message.Stream == 0 || scenario.Message.Function == 0 {
			return nil, fmt.Errorf("simulator scenario %q requires event or message stream/function", scenarioName)
		}
	}
	return compiled, nil
}

func legacyScenarioCommandName(name string) string {
	parts := strings.FieldsFunc(strings.ToLower(strings.TrimSpace(name)), func(value rune) bool {
		return value == '-' || value == '_' || value == '.' || value == ' '
	})
	if len(parts) >= 2 {
		return parts[len(parts)-1] + "." + strings.Join(parts[:len(parts)-1], "-")
	}
	if len(parts) == 1 {
		return "send." + parts[0]
	}
	return "send.message"
}
