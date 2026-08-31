package domain

import "testing"

func TestValidateNameAcceptsCommandAndEventOrdering(t *testing.T) {
	for _, test := range []struct {
		kind MessageType
		name string
	}{{TypeCommand, "send.recipe"}, {TypeEvent, "material.arrived"}, {TypeEvent, "recipe.sent"}} {
		if err := ValidateName(test.kind, test.name); err != nil {
			t.Fatalf("ValidateName(%s, %s) = %v", test.kind, test.name, err)
		}
	}
}

func TestValidateNameRejectsUnstructuredName(t *testing.T) {
	if err := ValidateName(TypeCommand, "sendRecipe"); err == nil {
		t.Fatal("expected a naming validation error")
	}
}
