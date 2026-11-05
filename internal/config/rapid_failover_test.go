package config

import "testing"

func TestExplicitRapidFailureWindowIsPreserved(t *testing.T) {
	state := DefaultState()
	state.Automation.FailoverFailureSeconds = 6
	if got := normalize(state).Automation.FailoverFailureSeconds; got != 6 {
		t.Fatalf("rapid failure window was clamped to %ds", got)
	}
	state.Automation.FailoverFailureSeconds = 1
	if got := normalize(state).Automation.FailoverFailureSeconds; got != 5 {
		t.Fatalf("minimum failure window is %ds, want5", got)
	}
}
