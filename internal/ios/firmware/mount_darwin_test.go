package firmware

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/tmc/apple/x/plist"
)

type mountModel struct {
	t                        *testing.T
	images                   []mountedImage
	attaches, detaches       int
	attachError, detachError bool
	journal                  string
}

func (m *mountModel) run(ctx context.Context, args ...string) ([]byte, error) {
	switch args[0] {
	case "info":
		return plist.Marshal(map[string]any{"images": m.images}, plist.FormatXML)
	case "attach":
		data, err := os.ReadFile(filepath.Join(m.journal, "mounts.json"))
		if err != nil {
			m.t.Fatal(err)
		}
		var state mountState
		if err := json.Unmarshal(data, &state); err != nil {
			m.t.Fatal(err)
		}
		if len(state.Records) == 0 || state.Records[len(state.Records)-1].State != "attaching" {
			m.t.Fatal("missing durable attach intent")
		}
		m.attaches++
		if m.attachError {
			return nil, errors.New("attach interrupted")
		}
		uid := uint32(os.Geteuid())
		image := mountedImage{Path: args[len(args)-1], UID: &uid, PID: 123, Entities: []mountEntity{{Device: "/dev/disk91"}, {Device: "/dev/disk91s1", MountPoint: "/private/mount"}}}
		m.images = append(m.images, image)
		return plist.Marshal(map[string]any{"system-entities": image.Entities}, plist.FormatXML)
	case "detach":
		m.detaches++
		if m.detachError {
			return nil, errors.New("busy")
		}
		for i, image := range m.images {
			if image.Entities[0].Device == args[1] {
				m.images = append(m.images[:i], m.images[i+1:]...)
				return nil, nil
			}
		}
		m.t.Fatal("detached an unrelated device", args)
	}
	return nil, fmt.Errorf("unexpected hdiutil command")
}
func testMountJournal(t *testing.T) (*MountJournal, *mountModel, string) {
	t.Helper()
	dir := t.TempDir()
	image := filepath.Join(dir, "source.dmg")
	if err := os.WriteFile(image, []byte("image"), 0600); err != nil {
		t.Fatal(err)
	}
	j, err := OpenMountJournal(filepath.Join(dir, "journal"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { j.Close() })
	model := &mountModel{t: t, images: []mountedImage{}, journal: j.directory}
	j.run = model.run
	return j, model, image
}
func TestMountJournalRecover(t *testing.T) {
	j, m, image := testMountJournal(t)
	if _, err := j.Attach(context.Background(), image, true); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenMountJournal(j.directory); err == nil {
		t.Fatal("opened locked journal")
	}
	directory := j.directory
	j.Close()
	j, err := OpenMountJournal(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer j.Close()
	j.run = m.run
	if err := j.Detach(context.Background(), "/dev/disk91s1"); err != nil {
		t.Fatal(err)
	}
	if err := j.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	if m.detaches != 1 || len(m.images) != 0 || j.state.Records[0].State != "detached" {
		t.Fatal(m, j.state)
	}
}
func TestMountJournalRejectsPreexisting(t *testing.T) {
	j, m, image := testMountJournal(t)
	uid := uint32(os.Geteuid())
	m.images = append(m.images, mountedImage{Path: image, UID: &uid})
	if _, err := j.Attach(context.Background(), image, true); err == nil {
		t.Fatal("adopted existing mount")
	}
	if m.attaches != 0 || m.detaches != 0 {
		t.Fatal(m)
	}
}
func TestMountJournalRejectsChangedOwnership(t *testing.T) {
	for _, kind := range []string{"file", "devices", "owner", "process"} {
		t.Run(kind, func(t *testing.T) {
			j, m, image := testMountJournal(t)
			if _, err := j.Attach(context.Background(), image, true); err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "file":
				other := filepath.Join(t.TempDir(), "different")
				os.WriteFile(other, []byte("other"), 0600)
				alias := j.state.Records[0].Alias
				os.Remove(alias)
				if err := os.Link(other, alias); err != nil {
					t.Fatal(err)
				}
			case "devices":
				m.images[0].Entities = []mountEntity{{Device: "/dev/disk92"}}
			case "owner":
				m.images[0].UID = nil
			case "process":
				m.images[0].PID++
			}
			if err := j.Recover(context.Background()); err == nil {
				t.Fatal("accepted changed ownership")
			}
			if m.detaches != 0 {
				t.Fatal("detached changed identity")
			}
		})
	}
}
func TestMountJournalRetainsUncertainIntent(t *testing.T) {
	j, m, image := testMountJournal(t)
	m.attachError = true
	if _, err := j.Attach(context.Background(), image, true); err == nil {
		t.Fatal("lost attach error")
	}
	if err := j.Recover(context.Background()); err == nil {
		t.Fatal("declared uncertain intent clean")
	}
	if _, err := j.Attach(context.Background(), image, true); err == nil {
		t.Fatal("started another attach with pending intent")
	}
	if m.attaches != 1 || m.detaches != 0 {
		t.Fatal(m)
	}
}
func TestMountJournalBusyDetach(t *testing.T) {
	j, m, image := testMountJournal(t)
	if _, err := j.Attach(context.Background(), image, true); err != nil {
		t.Fatal(err)
	}
	m.detachError = true
	if err := j.Recover(context.Background()); err == nil {
		t.Fatal("lost detach failure")
	}
	if j.state.Records[0].State != "attached" {
		t.Fatal(j.state)
	}
	m.detachError = false
	if err := j.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
}
func TestMountJournalMissingInventory(t *testing.T) {
	j, _, image := testMountJournal(t)
	j.run = func(context.Context, ...string) ([]byte, error) {
		return plist.Marshal(map[string]any{}, plist.FormatXML)
	}
	if _, err := j.Attach(context.Background(), image, true); err == nil {
		t.Fatal("accepted malformed inventory")
	}
}

// This opt-in test mounts only its own temporary image. The child is killed
// after hdiutil succeeds but before Attach can record the returned device nodes.
func TestMountJournalKilledHelper(t *testing.T) {
	if dir := os.Getenv("COVE_MOUNT_HELPER_DIR"); dir != "" {
		j, err := OpenMountJournal(filepath.Join(dir, "journal"))
		if err != nil {
			t.Fatal(err)
		}
		defer j.Close()
		j.run = func(ctx context.Context, args ...string) ([]byte, error) {
			data, err := diskImageCommand(ctx, args...)
			if args[0] == "attach" && err == nil {
				syscall.Kill(os.Getpid(), syscall.SIGKILL)
			}
			return data, err
		}
		_, err = j.Attach(context.Background(), filepath.Join(dir, "test.dmg"), true)
		t.Fatal("helper was not killed", err)
	}
	if os.Getenv("COVE_TEST_MOUNTS") != "1" {
		t.Skip("set COVE_TEST_MOUNTS=1 for owned-image crash recovery")
	}
	for _, tt := range []struct{ name, filesystem, size string }{
		{"hfs", "HFS+", "32m"}, {"apfs", "APFS", "64m"},
	} {
		t.Run(tt.name, func(t *testing.T) { testKilledMountHelper(t, tt.filesystem, tt.size) })
	}
}

func testKilledMountHelper(t *testing.T, filesystem, size string) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	dir, err := os.MkdirTemp("", "cove-mount-test-")
	if err != nil {
		t.Fatal(err)
	}
	// Preserve the journal/image on failure so recovery evidence is not destroyed.
	t.Log("mount recovery artifacts:", dir)
	if _, err := diskImageCommand(ctx, "create", "-size", size, "-fs", filesystem, "-volname", "CoveJournalTest", filepath.Join(dir, "test.dmg")); err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestMountJournalKilledHelper$")
	cmd.Env = append(os.Environ(), "COVE_MOUNT_HELPER_DIR="+dir)
	output, err := cmd.CombinedOutput()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ProcessState.Sys().(syscall.WaitStatus).Signal() != syscall.SIGKILL {
		t.Fatalf("helper: %v: %s", err, output)
	}
	j, err := OpenMountJournal(filepath.Join(dir, "journal"))
	if err != nil {
		t.Fatal(err)
	}
	defer j.Close()
	if len(j.state.Records) != 1 || j.state.Records[0].State != "attaching" {
		t.Fatal(j.state)
	}
	live, err := j.find(ctx, j.state.Records[0])
	if err != nil || live == nil {
		t.Fatalf("missing orphaned owned mount: %v", err)
	}
	if err := j.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	live, err = j.find(ctx, j.state.Records[0])
	if err != nil || live != nil {
		t.Fatalf("mount remains: %v", err)
	}
	if err := j.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
}
func ExampleOpenMountJournal() {
	_, err := OpenMountJournal("")
	fmt.Println(err)
	// Output: mount journal directory is required
}
func ExampleMountJournal() {
	var journal MountJournal
	fmt.Println(journal.Recover(context.Background()))
	// Output: mount journal is closed
}

func ExampleMountJournal_Attach() {
	var j MountJournal
	_, err := j.Attach(context.Background(), "image.dmg", true)
	fmt.Println(err)
	// Output: mount journal is closed
}
func ExampleMountJournal_Detach() {
	var j MountJournal
	fmt.Println(j.Detach(context.Background(), "/dev/disk1"))
	// Output: mount journal is closed
}
func ExampleMountJournal_Recover() {
	var j MountJournal
	fmt.Println(j.Recover(context.Background()))
	// Output: mount journal is closed
}
func ExampleMountJournal_Close() {
	var j MountJournal
	fmt.Println(j.Close())
	// Output: <nil>
}

func TestMountJournalReusedDeviceIsUntouched(t *testing.T) {
	j, m, image := testMountJournal(t)
	if _, err := j.Attach(context.Background(), image, true); err != nil {
		t.Fatal(err)
	}
	m.images[0].Path = filepath.Join(t.TempDir(), "unrelated.dmg")
	if err := j.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	if m.detaches != 0 || len(m.images) != 1 || j.state.Records[0].State != "detached" {
		t.Fatal("touched unrelated reused device")
	}
}
func TestMountJournalCancellationCleansOwnedImage(t *testing.T) {
	j, m, image := testMountJournal(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	j.run = func(ctx context.Context, args ...string) ([]byte, error) {
		data, err := m.run(ctx, args...)
		if args[0] == "attach" {
			cancel()
		}
		return data, err
	}
	if _, err := j.Attach(ctx, image, true); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if m.detaches != 1 || len(m.images) != 0 || j.state.Records[0].State != "detached" {
		t.Fatal("cancellation left owned image attached")
	}
}

func TestMountJournalRefusesUnownedRemount(t *testing.T) {
	j, _, _ := testMountJournal(t)
	called := false
	j.system = func(context.Context, string, ...string) error { called = true; return nil }
	if err := j.Remount(context.Background(), "/dev/disk1", "/Volumes/Other"); err == nil {
		t.Fatal("accepted unowned remount")
	}
	if err := j.Unmount(context.Background(), "/Volumes/Other"); err == nil {
		t.Fatal("accepted unowned unmount")
	}
	if called {
		t.Fatal("invoked system command for unowned volume")
	}
}
func TestMountJournalRemountPrivilegeError(t *testing.T) {
	j, _, image := testMountJournal(t)
	if _, err := j.Attach(context.Background(), image, true); err != nil {
		t.Fatal(err)
	}
	denied := errors.New("permission denied")
	j.system = func(context.Context, string, ...string) error { return denied }
	if err := j.Remount(context.Background(), "/dev/disk91s1", "/private/mount"); !errors.Is(err, denied) {
		t.Fatal(err)
	}
	if j.state.Records[0].State != "attached" {
		t.Fatal("lost mount ownership after denied remount")
	}
}
func ExampleMountJournal_Remount() {
	var j MountJournal
	fmt.Println(j.Remount(context.Background(), "/dev/disk1", "/Volumes/Test"))
	// Output: mount journal is closed
}
func ExampleMountJournal_Unmount() {
	var j MountJournal
	fmt.Println(j.Unmount(context.Background(), "/Volumes/Test"))
	// Output: mount journal is closed
}
