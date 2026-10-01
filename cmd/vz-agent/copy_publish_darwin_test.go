package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPublishAgentCopy(t *testing.T) {
	for _, overwrite := range []bool{false, true} {
		t.Run(map[bool]string{false: "no overwrite", true: "overwrite"}[overwrite], func(t *testing.T) {
			dir := t.TempDir()
			stage := filepath.Join(dir, "stage")
			dest := filepath.Join(dir, "dest")
			if err := os.Mkdir(stage, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(stage, "new"), []byte("new"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(dest, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dest, "old"), []byte("old"), 0600); err != nil {
				t.Fatal(err)
			}
			err := publishAgentCopy(stage, dest, overwrite)
			if !overwrite {
				if err == nil {
					t.Fatal("overwrote existing directory")
				}
				if _, err := os.Stat(filepath.Join(dest, "old")); err != nil {
					t.Fatal(err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(filepath.Join(dest, "new")); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(filepath.Join(stage, "old")); err != nil {
				t.Fatal("old directory lost", err)
			}
		})
	}
}
