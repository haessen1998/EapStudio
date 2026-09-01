package equipment

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	driver "eapstudio/internal/driver/secs"
	"eapstudio/internal/event"
	"eapstudio/internal/profile"
)

// DemoAOIV200Adapter demonstrates where vendor-specific wire values are
// translated into a stable canonical contract. The AOI reports a numeric
// result code, while downstream consumers receive a readable result and a
// derived review decision.
type DemoAOIV200Adapter struct{ GenericGemAdapter }

func (DemoAOIV200Adapter) Name() string { return "demo-aoi-v200" }

func (a DemoAOIV200Adapter) Parse(ctx context.Context, message driver.Message, compiled *profile.CompiledProfile) ([]event.Event, error) {
	values, err := a.GenericGemAdapter.Parse(ctx, message, compiled)
	if err != nil {
		return nil, err
	}
	for index := range values {
		if values[index].Name != "inspection.completed" {
			continue
		}
		code, err := adapterNumber(values[index].Data["resultCode"])
		if err != nil {
			return nil, fmt.Errorf("AOI result code: %w", err)
		}
		result := map[int]string{0: "PASS", 1: "REVIEW", 2: "FAIL"}[int(code)]
		if result == "" {
			result = "UNKNOWN"
		}
		delete(values[index].Data, "resultCode")
		values[index].Data["result"] = result
		values[index].Data["requiresReview"] = result != "PASS"
		if count, convertErr := adapterNumber(values[index].Data["defectCount"]); convertErr == nil {
			values[index].Data["defectCount"] = int(count)
		}
	}
	return values, nil
}

func (a DemoAOIV200Adapter) BuildEvent(ctx context.Context, name string, data map[string]any, compiled *profile.CompiledProfile) (driver.Message, error) {
	if name != "inspection.completed" {
		return a.GenericGemAdapter.BuildEvent(ctx, name, data, compiled)
	}
	wire := cloneAdapterData(data)
	if _, exists := wire["resultCode"]; !exists {
		result := strings.ToUpper(strings.TrimSpace(fmt.Sprint(wire["result"])))
		code, ok := map[string]int{"PASS": 0, "REVIEW": 1, "FAIL": 2}[result]
		if !ok {
			return driver.Message{}, fmt.Errorf("AOI inspection result must be PASS, REVIEW, or FAIL")
		}
		wire["resultCode"] = code
	}
	return a.GenericGemAdapter.BuildEvent(ctx, name, wire, compiled)
}

func adapterNumber(value any) (float64, error) {
	number, err := strconv.ParseFloat(strings.TrimSpace(fmt.Sprint(value)), 64)
	if err != nil {
		return 0, fmt.Errorf("invalid numeric value %q", value)
	}
	return number, nil
}

func cloneAdapterData(data map[string]any) map[string]any {
	result := make(map[string]any, len(data)+2)
	for key, value := range data {
		result[key] = value
	}
	return result
}

var _ Adapter = DemoAOIV200Adapter{}
