package domain

import (
	"fmt"
	"regexp"
	"strings"
)

type MessageType string

const (
	TypeCommand MessageType = "command"
	TypeEvent   MessageType = "event"
)

var namePattern = regexp.MustCompile(`^[a-z][a-z0-9_-]*\.[a-z][a-z0-9_-]*$`)

// ValidateName enforces the public message contract:
// commands are verb.noun and events are noun.verb/state.
func ValidateName(kind MessageType, name string) error {
	if kind != TypeCommand && kind != TypeEvent {
		return fmt.Errorf("unsupported message type %q", kind)
	}
	if !namePattern.MatchString(name) {
		return fmt.Errorf("%s name %q must contain two lower-case segments separated by a dot", kind, name)
	}
	parts := strings.SplitN(name, ".", 2)
	if parts[0] == parts[1] {
		return fmt.Errorf("%s name %q must describe two distinct concepts", kind, name)
	}
	return nil
}
