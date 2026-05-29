package main

import (
	"errors"
	"fmt"
	"testing"
)

func TestErrVZScriptDependencyCycleWraps(t *testing.T) {
	err := fmt.Errorf("%w at homebrew", ErrVZScriptDependencyCycle)
	if !errors.Is(err, ErrVZScriptDependencyCycle) {
		t.Fatalf("err = %v, want errors.Is(err, ErrVZScriptDependencyCycle)", err)
	}
}

func TestErrVZScriptDependencyCycleDistinctFromEmptyRecipe(t *testing.T) {
	if errors.Is(ErrVZScriptDependencyCycle, ErrEmptyRecipeName) {
		t.Fatal("DependencyCycle should not match EmptyRecipeName")
	}
	if errors.Is(ErrEmptyRecipeName, ErrVZScriptDependencyCycle) {
		t.Fatal("EmptyRecipeName should not match DependencyCycle")
	}
}
