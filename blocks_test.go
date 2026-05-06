package main

import "testing"

func TestIsVZAlreadyStoppedStopError(t *testing.T) {
	err := nsErrorSnapshot{
		domain:      "VZErrorDomain",
		code:        4,
		description: `Invalid virtual machine state transition. Transition from state "stopped" to state "stopping" is invalid.`,
	}
	if !isVZAlreadyStoppedStopError(err) {
		t.Fatal("isVZAlreadyStoppedStopError returned false")
	}
}

func TestIsVZAlreadyStoppedStopErrorRejectsOtherErrors(t *testing.T) {
	tests := []struct {
		name string
		err  error
	}{
		{"wrong domain", nsErrorSnapshot{domain: "Other", code: 4, description: `Transition from state "stopped" to state "stopping" is invalid.`}},
		{"wrong code", nsErrorSnapshot{domain: "VZErrorDomain", code: 6, description: `Transition from state "stopped" to state "stopping" is invalid.`}},
		{"wrong transition", nsErrorSnapshot{domain: "VZErrorDomain", code: 4, description: `Transition from state "running" to state "stopping" is invalid.`}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if isVZAlreadyStoppedStopError(tt.err) {
				t.Fatal("isVZAlreadyStoppedStopError returned true")
			}
		})
	}
}
