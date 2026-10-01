package main

import (
	"testing"

	"github.com/tmc/apple/appkit"
)

func TestSharedFolderMenuAccessModes(t *testing.T) {
	dir := t.TempDir()
	folders := []SharedFolderEntry{
		{Path: "/one/work", Tag: "one", ReadOnly: true},
		{Path: "/two/work", Tag: "two"},
	}
	if err := saveSharedFolders(dir, folders); err != nil {
		t.Fatal(err)
	}
	tb := &VMToolbar{vmDirectory: dir}
	menu := appkit.NewMenuWithTitle("Shared Folders")
	tb.populateSharedFolderMenu(menu)
	for i, folder := range folders {
		item := menu.ItemAtIndex(i)
		if !item.HasSubmenu() {
			t.Fatalf("folder %q has no submenu", folder.Tag)
		}
		submenu := item.Submenu()
		for j, title := range []string{"Read Only", "Read & Write"} {
			choice := submenu.ItemAtIndex(j)
			if choice.Title() != title || sharedFolderMenuSelector(choice.GetID()) != folder.Tag {
				t.Fatalf("choice %d for %q = %q, selector %q", j, folder.Tag, choice.Title(), sharedFolderMenuSelector(choice.GetID()))
			}
			selected := choice.State() == appkit.NSControlStateValue(1)
			if selected != (folder.ReadOnly == (j == 0)) {
				t.Fatalf("incorrect checkmark for %q, choice %q", folder.Tag, title)
			}
			if got := choice.Tag() == 1; got != (j == 0) {
				t.Fatalf("wrong access mode on %q", title)
			}
		}
		remove := submenu.ItemAtIndex(3)
		if remove.Title() != "Remove" || sharedFolderMenuSelector(remove.GetID()) != folder.Tag {
			t.Fatalf("invalid removal choice for %q", folder.Tag)
		}
	}
}
