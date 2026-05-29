package main

import (
	"errors"
	"testing"
)

func TestLoadVZScriptDataEmptyRecipeName(t *testing.T) {
	for _, name := range []string{"", " ", "\t", "  \n"} {
		data, err := loadVZScriptData(name)
		if !errors.Is(err, ErrEmptyRecipeName) {
			t.Errorf("loadVZScriptData(%q) err = %v, want ErrEmptyRecipeName", name, err)
		}
		if data != nil {
			t.Errorf("loadVZScriptData(%q) data = %q, want nil", name, data)
		}
	}
}

func TestLoadVZScriptDataMissingNotEmpty(t *testing.T) {
	_, err := loadVZScriptData("definitely-not-a-real-recipe-name-xyz")
	if err == nil {
		t.Fatal("expected error for missing recipe")
	}
	if errors.Is(err, ErrEmptyRecipeName) {
		t.Errorf("missing recipe matched ErrEmptyRecipeName: %v", err)
	}
}
