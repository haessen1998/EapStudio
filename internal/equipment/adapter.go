package equipment

import (
	"context"

	"eapstudio/internal/command"
	driver "eapstudio/internal/driver/secs"
	"eapstudio/internal/event"
	"eapstudio/internal/profile"
)

type Adapter interface {
	Name() string
	Parse(context.Context, driver.Message, *profile.CompiledProfile) ([]event.Event, error)
	BuildCommand(context.Context, command.Command, *profile.CompiledProfile) (driver.Message, error)
	ValidateCommandReply(context.Context, command.Command, *driver.Message, *profile.CompiledProfile) error
	BuildEvent(context.Context, string, map[string]any, *profile.CompiledProfile) (driver.Message, error)
}
