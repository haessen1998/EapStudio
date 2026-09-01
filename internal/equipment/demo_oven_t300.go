package equipment

import (
	"context"
	"fmt"
	"math"

	driver "eapstudio/internal/driver/secs"
	"eapstudio/internal/event"
	"eapstudio/internal/profile"
)

// DemoOvenT300Adapter demonstrates unit conversion at the equipment boundary.
// The demo oven transports temperature as integer deci-degrees Celsius; the
// canonical event exposes a normal Celsius number.
type DemoOvenT300Adapter struct{ GenericGemAdapter }

func (DemoOvenT300Adapter) Name() string { return "demo-oven-t300" }

func (a DemoOvenT300Adapter) Parse(ctx context.Context, message driver.Message, compiled *profile.CompiledProfile) ([]event.Event, error) {
	values, err := a.GenericGemAdapter.Parse(ctx, message, compiled)
	if err != nil {
		return nil, err
	}
	for index := range values {
		if values[index].Name != "batch.started" && values[index].Name != "temperature.deviated" {
			continue
		}
		deciC, err := adapterNumber(values[index].Data["temperatureDeciC"])
		if err != nil {
			return nil, fmt.Errorf("oven temperature: %w", err)
		}
		delete(values[index].Data, "temperatureDeciC")
		values[index].Data["temperatureC"] = deciC / 10
		values[index].Data["temperatureUnit"] = "C"
	}
	return values, nil
}

func (a DemoOvenT300Adapter) BuildEvent(ctx context.Context, name string, data map[string]any, compiled *profile.CompiledProfile) (driver.Message, error) {
	if name != "batch.started" && name != "temperature.deviated" {
		return a.GenericGemAdapter.BuildEvent(ctx, name, data, compiled)
	}
	wire := cloneAdapterData(data)
	if _, exists := wire["temperatureDeciC"]; !exists {
		celsius, err := adapterNumber(wire["temperatureC"])
		if err != nil {
			return driver.Message{}, fmt.Errorf("oven temperature: %w", err)
		}
		wire["temperatureDeciC"] = int(math.Round(celsius * 10))
	}
	return a.GenericGemAdapter.BuildEvent(ctx, name, wire, compiled)
}

var _ Adapter = DemoOvenT300Adapter{}
