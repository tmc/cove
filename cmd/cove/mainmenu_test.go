package main

import (
	"testing"

	"github.com/tmc/apple/appkit"
)

func TestMainMenuShortcuts(t *testing.T) {
	mainMenu, _ := buildMainMenu(0)

	ctrlCmd := appkit.NSEventModifierFlagCommand | appkit.NSEventModifierFlagControl

	tests := []struct {
		menuTitle string
		itemTitle string
		wantKey   string
		wantMod   appkit.NSEventModifierFlags
	}{
		// VM menu actions: Ctrl-Cmd modifier so guest shortcuts are preserved
		{"VM", "Stop", ".", ctrlCmd},
		{"VM", "Pause", "p", ctrlCmd},
		{"VM", "Restart", "r", ctrlCmd},
		{"VM", "Capture Input", "k", ctrlCmd},
		{"VM", "Screenshot...", "s", ctrlCmd},

		// View menu actions: Toggle Toolbar uses Ctrl-Cmd
		{"View", "Toggle Toolbar", "t", ctrlCmd},
		{"View", "Enter Full Screen", "f", ctrlCmd},

		// Standard host actions: Cmd-only
		{"Edit", "Undo", "z", appkit.NSEventModifierFlagCommand},
		{"Edit", "Cut", "x", appkit.NSEventModifierFlagCommand},
		{"Edit", "Copy", "c", appkit.NSEventModifierFlagCommand},
		{"Edit", "Paste", "v", appkit.NSEventModifierFlagCommand},
		{"Edit", "Select All", "a", appkit.NSEventModifierFlagCommand},
		{"Window", "Minimize", "m", appkit.NSEventModifierFlagCommand},
	}

	for _, tt := range tests {
		t.Run(tt.menuTitle+"/"+tt.itemTitle, func(t *testing.T) {
			parentItem := mainMenu.ItemWithTitle(tt.menuTitle)
			if parentItem.GetID() == 0 {
				t.Fatalf("menu item %q not found", tt.menuTitle)
			}
			submenu := parentItem.Submenu()
			if submenu.GetID() == 0 {
				t.Fatalf("submenu for %q not found", tt.menuTitle)
			}
			item := submenu.ItemWithTitle(tt.itemTitle)
			if item.GetID() == 0 {
				t.Fatalf("item %q in menu %q not found", tt.itemTitle, tt.menuTitle)
			}

			if gotKey := item.KeyEquivalent(); gotKey != tt.wantKey {
				t.Errorf("keyEquivalent = %q, want %q", gotKey, tt.wantKey)
			}

			// Mask out device-independent modifier flag if set by Cocoa
			gotMod := item.KeyEquivalentModifierMask() & (appkit.NSEventModifierFlagCommand |
				appkit.NSEventModifierFlagControl |
				appkit.NSEventModifierFlagOption |
				appkit.NSEventModifierFlagShift)
			if gotMod != tt.wantMod {
				t.Errorf("modifierMask = %v, want %v", gotMod, tt.wantMod)
			}
		})
	}
}
