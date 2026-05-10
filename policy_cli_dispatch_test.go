package main

import (
	"strings"
	"testing"
)

func TestHandlePolicyCommandDispatchBranches(t *testing.T) {
	withTempHome(t)

	t.Run("emptyArgs", func(t *testing.T) {
		if err := handlePolicyCommand(nil); err != nil {
			t.Fatalf("err = %v, want nil (help)", err)
		}
	})

	t.Run("helpAlias", func(t *testing.T) {
		if err := handlePolicyCommand([]string{"-h"}); err != nil {
			t.Fatalf("err = %v, want nil", err)
		}
	})

	t.Run("missingCommand", func(t *testing.T) {
		err := handlePolicyCommand([]string{"vm"})
		if err == nil || !strings.Contains(err.Error(), "policy requires a command") {
			t.Fatalf("err = %v, want requires a command", err)
		}
	})

	t.Run("unknownCommand", func(t *testing.T) {
		err := handlePolicyCommand([]string{"vm", "frobnicate"})
		if err == nil || !strings.Contains(err.Error(), "unknown policy command") {
			t.Fatalf("err = %v, want unknown policy command", err)
		}
	})

	t.Run("showWithExtraArg", func(t *testing.T) {
		err := handlePolicyCommand([]string{"vm", "show", "extra"})
		if err == nil || !strings.Contains(err.Error(), "usage:") {
			t.Fatalf("err = %v, want usage:", err)
		}
	})

	t.Run("clearWithExtraArg", func(t *testing.T) {
		err := handlePolicyCommand([]string{"vm", "clear", "extra"})
		if err == nil || !strings.Contains(err.Error(), "usage:") {
			t.Fatalf("err = %v, want usage:", err)
		}
	})
}
