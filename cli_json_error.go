package main

import (
	"encoding/json"
	"fmt"
	"io"
)

type cliJSONError struct {
	OK      bool   `json:"ok"`
	Command string `json:"command"`
	Error   string `json:"error"`
	Hint    string `json:"hint,omitempty"`
}

func writeCLIErrorJSON(w io.Writer, command string, err error, hint string) error {
	if err == nil {
		return nil
	}
	out := cliJSONError{
		OK:      false,
		Command: command,
		Error:   err.Error(),
		Hint:    hint,
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if encErr := enc.Encode(out); encErr != nil {
		return fmt.Errorf("encode json error: %w", encErr)
	}
	return nil
}
