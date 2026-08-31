package equipment

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"eapstudio/internal/command"
	"eapstudio/internal/domain"
	driver "eapstudio/internal/driver/secs"
	"eapstudio/internal/event"
	"eapstudio/internal/profile"
)

type GenericGemAdapter struct{}

func (GenericGemAdapter) Name() string { return "generic-gem" }

func (GenericGemAdapter) Parse(_ context.Context, message driver.Message, compiled *profile.CompiledProfile) ([]event.Event, error) {
	if message.Stream == 5 && message.Function == 1 {
		return parseAlarm(message), nil
	}
	if message.Stream != 6 || message.Function != 11 {
		return nil, nil
	}
	definition, ok := compiled.Spec.Events[message.CEID]
	if !ok {
		return []event.Event{{
			ID: fmt.Sprintf("evt-%s", message.ID), Type: domain.TypeEvent, Name: "secs.unknown",
			EquipmentID: message.EquipmentID, CorrelationID: "flow-" + message.ID, CausationID: message.ID,
			Timestamp: message.Timestamp,
			Source:    event.Source{Protocol: "secs-gem", Stream: message.Stream, Function: message.Function, CEID: message.CEID},
			Data:      map[string]any{"ceid": message.CEID, "reports": message.Reports},
		}}, nil
	}

	data := make(map[string]any, len(definition.Mapping))
	for fieldName, mapping := range definition.Mapping {
		position := compiled.VariablePositions[mapping.Report][mapping.Variable]
		values, exists := message.Reports[mapping.Report]
		if !exists || position >= len(values) {
			return nil, fmt.Errorf("CEID %d field %q missing report %d variable %d", message.CEID, fieldName, mapping.Report, mapping.Variable)
		}
		data[fieldName] = values[position]
	}
	timestamp := message.Timestamp
	if timestamp.IsZero() {
		timestamp = time.Now()
	}
	return []event.Event{{
		ID: fmt.Sprintf("evt-%s", message.ID), Type: domain.TypeEvent, Name: definition.Name,
		EquipmentID: message.EquipmentID, CorrelationID: correlationID(message, data), CausationID: message.ID,
		Timestamp: timestamp,
		Source:    event.Source{Protocol: "secs-gem", Stream: message.Stream, Function: message.Function, CEID: message.CEID},
		Data:      data,
	}}, nil
}

func parseAlarm(message driver.Message) []event.Event {
	alarmID := fmt.Sprint(message.Fields["alarmId"])
	active, _ := message.Fields["active"].(bool)
	name := "alarm.cleared"
	if active {
		name = "alarm.raised"
	}
	timestamp := message.Timestamp
	if timestamp.IsZero() {
		timestamp = time.Now()
	}
	data := map[string]any{
		"alarmId":  alarmID,
		"code":     fmt.Sprint(message.Fields["code"]),
		"text":     fmt.Sprint(message.Fields["text"]),
		"severity": fmt.Sprint(message.Fields["severity"]),
		"active":   active,
	}
	return []event.Event{{
		ID: "evt-" + message.ID, Type: domain.TypeEvent, Name: name,
		EquipmentID: message.EquipmentID, CorrelationID: fmt.Sprintf("alarm-%s-%s", message.EquipmentID, alarmID), CausationID: message.ID,
		Timestamp: timestamp, Source: event.Source{Protocol: "secs-gem", Stream: 5, Function: 1}, Data: data,
	}}
}

func (GenericGemAdapter) BuildCommand(_ context.Context, value command.Command, compiled *profile.CompiledProfile) (driver.Message, error) {
	definition, ok := compiled.Spec.Commands[value.Name]
	if !ok {
		return driver.Message{}, fmt.Errorf("profile %q does not define command %q", compiled.Metadata.Name, value.Name)
	}
	var body strings.Builder
	fmt.Fprintf(&body, "S%dF%d", definition.Stream, definition.Function)
	if definition.Wait {
		body.WriteString(" W")
	}
	fmt.Fprintf(&body, "\n<L[%d]", len(definition.Parameters))
	for _, name := range definition.Parameters {
		parameter, exists := value.Parameters[name]
		if !exists {
			return driver.Message{}, fmt.Errorf("command %q requires parameter %q", value.Name, name)
		}
		fmt.Fprintf(&body, "\n  <A \"%s\">", escapeSML(fmt.Sprint(parameter)))
	}
	body.WriteString("\n>\n.")
	return driver.Message{EquipmentID: value.EquipmentID, Direction: driver.DirectionOut, Timestamp: time.Now(), Stream: definition.Stream, Function: definition.Function, Wait: definition.Wait, SML: body.String(), Tree: body.String(), Metadata: map[string]string{"commandId": value.ID, "commandName": value.Name}}, nil
}

func (GenericGemAdapter) ValidateCommandReply(_ context.Context, value command.Command, reply *driver.Message, compiled *profile.CompiledProfile) error {
	definition := compiled.Spec.Commands[value.Name]
	if definition.Wait && reply == nil {
		return fmt.Errorf("command %q expected a secondary reply", value.Name)
	}
	if definition.SuccessAck == nil {
		return nil
	}
	if reply == nil || reply.Ack == nil {
		return fmt.Errorf("command %q reply has no acknowledgement", value.Name)
	}
	if *reply.Ack != *definition.SuccessAck {
		return fmt.Errorf("equipment rejected command %q with ACK %d", value.Name, *reply.Ack)
	}
	return nil
}

// BuildEvent performs the inverse Profile mapping used by the simulator:
// canonical event data becomes CEID/RPTID/ordered report values and then SML.
func (GenericGemAdapter) BuildEvent(_ context.Context, name string, data map[string]any, compiled *profile.CompiledProfile) (driver.Message, error) {
	ceid, ok := compiled.EventsByName[name]
	if !ok {
		return driver.Message{}, fmt.Errorf("profile %q does not define event %q", compiled.Metadata.Name, name)
	}
	definition := compiled.Spec.Events[ceid]
	reports := make(map[uint64][]any, len(definition.Reports))
	for _, reportID := range definition.Reports {
		report, exists := compiled.Spec.Reports[reportID]
		if !exists {
			return driver.Message{}, fmt.Errorf("event %q references unknown report %d", name, reportID)
		}
		reports[reportID] = make([]any, len(report.Variables))
	}
	for field, mapping := range definition.Mapping {
		value, exists := data[field]
		if !exists {
			return driver.Message{}, fmt.Errorf("simulated event %q requires data field %q", name, field)
		}
		position := compiled.VariablePositions[mapping.Report][mapping.Variable]
		reports[mapping.Report][position] = value
	}

	reportIDs := append([]uint64(nil), definition.Reports...)
	sort.Slice(reportIDs, func(i, j int) bool { return reportIDs[i] < reportIDs[j] })
	var body strings.Builder
	fmt.Fprintf(&body, "S6F11 W\n<L[3]\n  <U4 0>\n  <U4 %d>\n  <L[%d]", ceid, len(reportIDs))
	for _, reportID := range reportIDs {
		values := reports[reportID]
		fmt.Fprintf(&body, "\n    <L[2]\n      <U4 %d>\n      <L[%d]", reportID, len(values))
		for _, value := range values {
			fmt.Fprintf(&body, "\n        <A \"%s\">", escapeSML(fmt.Sprint(value)))
		}
		body.WriteString("\n      >\n    >")
	}
	body.WriteString("\n  >\n>\n.")
	return driver.Message{Direction: driver.DirectionIn, Timestamp: time.Now(), Stream: 6, Function: 11, Wait: true, CEID: ceid, Reports: reports, SML: body.String(), Tree: body.String(), Metadata: map[string]string{"eventName": name}}, nil
}

func correlationID(message driver.Message, data map[string]any) string {
	if materialID, ok := data["materialId"]; ok && fmt.Sprint(materialID) != "" {
		return fmt.Sprintf("flow-%s-%v", message.EquipmentID, materialID)
	}
	return "flow-" + message.ID
}

func escapeSML(value string) string {
	return strings.ReplaceAll(strings.ReplaceAll(value, `\`, `\\`), `"`, `\"`)
}
