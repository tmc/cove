package checkpoint_test

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/tmc/cove/internal/checkpoint"
)

func exampleVM() (*checkpoint.Manager, string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, "", err
	}
	parent := filepath.Join(home, "tmp")
	if err := os.MkdirAll(parent, 0700); err != nil {
		return nil, "", err
	}
	root, err := os.MkdirTemp(parent, "checkpoint-example-*")
	if err != nil {
		return nil, "", err
	}
	if err := os.WriteFile(filepath.Join(root, "disk.img"), []byte("saved"), 0600); err != nil {
		os.RemoveAll(root)
		return nil, "", err
	}
	return checkpoint.New(root), root, nil
}

func ExampleNew() {
	manager, root, err := exampleVM()
	if err != nil {
		fmt.Println(err)
		return
	}
	defer os.RemoveAll(root)
	pending, err := manager.Pending()
	fmt.Println(pending, err)
	// Output: false <nil>
}

func ExampleManager_Save() {
	manager, root, err := exampleVM()
	if err != nil {
		fmt.Println(err)
		return
	}
	defer os.RemoveAll(root)
	manifest, err := manager.Save("baseline", "guest-a", []checkpoint.Source{{Path: "disk.img", Role: "disk"}})
	fmt.Println(manifest.Name, len(manifest.Files), err)
	// Output: baseline 1 <nil>
}

func ExampleManager_Read() {
	manager, root, err := exampleVM()
	if err != nil {
		fmt.Println(err)
		return
	}
	defer os.RemoveAll(root)
	if _, err := manager.Save("baseline", "guest-a", []checkpoint.Source{{Path: "disk.img", Role: "disk"}}); err != nil {
		fmt.Println(err)
		return
	}
	manifest, err := manager.Read("baseline")
	fmt.Println(manifest.Files[0].Size, err)
	// Output: 5 <nil>
}

func ExampleManager_Restore() {
	manager, root, err := exampleVM()
	if err != nil {
		fmt.Println(err)
		return
	}
	defer os.RemoveAll(root)
	if _, err := manager.Save("baseline", "guest-a", []checkpoint.Source{{Path: "disk.img", Role: "disk"}}); err != nil {
		fmt.Println(err)
		return
	}
	if err := os.WriteFile(filepath.Join(root, "disk.img"), []byte("changed"), 0600); err != nil {
		fmt.Println(err)
		return
	}
	if err := manager.Restore("baseline", "guest-a"); err != nil {
		fmt.Println(err)
		return
	}
	data, err := os.ReadFile(filepath.Join(root, "disk.img"))
	fmt.Println(string(data), err)
	// Output: saved <nil>
}

func ExampleManager_Recover() {
	manager, root, err := exampleVM()
	if err != nil {
		fmt.Println(err)
		return
	}
	defer os.RemoveAll(root)
	fmt.Println(manager.Recover())
	// Output: <nil>
}

func ExampleManager_Pending() {
	manager, root, err := exampleVM()
	if err != nil {
		fmt.Println(err)
		return
	}
	defer os.RemoveAll(root)
	pending, err := manager.Pending()
	fmt.Println(pending, err)
	// Output: false <nil>
}

func ExampleSource() {
	source := checkpoint.Source{Path: "disk.img", Role: "disk"}
	fmt.Println(source.Path, source.Role)
	// Output: disk.img disk
}

func ExampleEntry() {
	entry := checkpoint.Entry{Source: checkpoint.Source{Path: "aux.img", Role: "firmware"}, Size: 4}
	fmt.Println(entry.Role, entry.Size)
	// Output: firmware 4
}

func ExampleManifest() {
	manifest := checkpoint.Manifest{Version: checkpoint.Version, Name: "baseline", Compatibility: "guest-a"}
	fmt.Println(manifest.Version, manifest.Name)
	// Output: 1 baseline
}

func ExampleVersion() {
	fmt.Println(checkpoint.Version)
	// Output: 1
}

func ExampleAcquireRead() {
	_, root, err := exampleVM()
	if err != nil {
		fmt.Println(err)
		return
	}
	defer os.RemoveAll(root)
	release, err := checkpoint.AcquireRead(root)
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println(release())
	// Output: <nil>
}

func ExampleManager_RecoveryPaths() {
	manager, root, err := exampleVM()
	if err != nil {
		fmt.Println(err)
		return
	}
	defer os.RemoveAll(root)
	_, err = manager.RecoveryPaths()
	fmt.Println(os.IsNotExist(err))
	// Output: true
}
