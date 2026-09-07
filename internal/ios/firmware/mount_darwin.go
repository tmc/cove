package firmware

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sync"
	"syscall"
	"time"

	"github.com/tmc/apple/x/plist"
)

type mountEntity struct {
	Device     string `plist:"dev-entry" json:"device"`
	MountPoint string `plist:"mount-point" json:"mountPoint,omitempty"`
}
type mountedImage struct {
	Path     string        `plist:"image-path"`
	UID      *uint32       `plist:"owner-uid"`
	PID      int           `plist:"hdid-pid"`
	Entities []mountEntity `plist:"system-entities"`
}
type mountRecord struct {
	Alias      string        `json:"alias"`
	FileDevice uint64        `json:"fileDevice"`
	FileInode  uint64        `json:"fileInode"`
	UID        uint32        `json:"uid"`
	PID        int           `json:"pid,omitempty"`
	State      string        `json:"state"`
	Entities   []mountEntity `json:"entities,omitempty"`
}
type mountState struct {
	SchemaVersion int           `json:"schemaVersion"`
	Records       []mountRecord `json:"records"`
}

// MountJournal records attempt-owned disk image attachments. Callers must also
// hold the enclosing workflow's lock while using these images. Close releases
// the journal lock without detaching; Recover detaches recorded attachments.
// The zero value is closed.
type MountJournal struct {
	mu        sync.Mutex
	directory string
	lock      *os.File
	state     mountState
	run       func(context.Context, ...string) ([]byte, error)
}

// OpenMountJournal opens an attempt's journal with exclusive ownership. Images
// are attached through private hard links in directory, which must therefore be
// on the same filesystem as the images. No mount is adopted by a similar name.
func OpenMountJournal(directory string) (*MountJournal, error) {
	if directory == "" {
		return nil, fmt.Errorf("mount journal directory is required")
	}
	directory, err := filepath.Abs(directory)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(directory, 0700); err != nil {
		return nil, err
	}
	directory, err = filepath.EvalSymlinks(directory)
	if err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(directory, ".lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, fmt.Errorf("lock mount journal: %w", err)
	}
	j := &MountJournal{directory: directory, lock: f, state: mountState{SchemaVersion: 1}, run: diskImageCommand}
	data, err := os.ReadFile(filepath.Join(directory, "mounts.json"))
	if err == nil {
		err = json.Unmarshal(data, &j.state)
	} else if os.IsNotExist(err) {
		err = nil
	}
	if err == nil && j.state.SchemaVersion != 1 {
		err = fmt.Errorf("unsupported mount journal schema %d", j.state.SchemaVersion)
	}
	if err != nil {
		j.Close()
		return nil, err
	}
	return j, nil
}

// Close releases the journal lock. It waits for any current operation and does
// not declare pending mounts clean. It is safe to call repeatedly.
func (j *MountJournal) Close() error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.lock == nil {
		return nil
	}
	err := j.lock.Close()
	j.lock = nil
	return err
}
func (j *MountJournal) save() error {
	return writeState(filepath.Join(j.directory, "mounts.json"), j.state)
}

// Attach records intent before invoking hdiutil and returns its attach plist.
// Cancellation or an attach error triggers owned-mount recovery. If the outcome
// cannot be established, intent stays pending and further Attach calls fail.
// Pre-existing attachments of the same file are rejected, including hard links.
func (j *MountJournal) Attach(ctx context.Context, image string, readOnly bool) ([]byte, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.lock == nil {
		return nil, fmt.Errorf("mount journal is closed")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	for _, r := range j.state.Records {
		if r.State == "attaching" {
			return nil, fmt.Errorf("mount journal has unresolved attachment intent")
		}
	}
	original, err := os.Stat(image)
	if err != nil {
		return nil, err
	}
	if !original.Mode().IsRegular() {
		return nil, fmt.Errorf("mount image is not a regular file")
	}
	live, err := j.inventory(ctx)
	if err != nil {
		return nil, err
	}
	for _, im := range live {
		info, err := os.Stat(im.Path)
		if err != nil {
			return nil, fmt.Errorf("inspect existing image attachment %s: %w", im.Path, err)
		}
		if os.SameFile(original, info) {
			return nil, fmt.Errorf("image is already attached")
		}
	}
	private, err := os.MkdirTemp(j.directory, "image-")
	if err != nil {
		return nil, err
	}
	alias := filepath.Join(private, "image.dmg")
	if err := os.Link(image, alias); err != nil {
		return nil, fmt.Errorf("create private image link: %w", err)
	}
	dev, ino, err := fileIdentity(alias)
	if err != nil {
		return nil, err
	}
	originalStat := original.Sys().(*syscall.Stat_t)
	if uint64(originalStat.Dev) != dev || originalStat.Ino != ino {
		return nil, fmt.Errorf("mount image changed while creating its private link")
	}
	r := mountRecord{Alias: alias, FileDevice: dev, FileInode: ino, UID: uint32(os.Geteuid()), State: "attaching"}
	j.state.Records = append(j.state.Records, r)
	index := len(j.state.Records) - 1
	// Both the private link and the intent must survive before attachment starts.
	if err := syncTree(private); err != nil {
		return nil, err
	}
	if err := j.save(); err != nil {
		return nil, err
	}
	args := []string{"attach", "-plist", "-nobrowse", "-noautoopen", "-mountroot", private}
	if readOnly {
		args = append(args, "-readonly")
	}
	args = append(args, alias)
	data, err := j.run(ctx, args...)
	if err == nil {
		var response struct {
			Entities []mountEntity `plist:"system-entities"`
		}
		_, err = plist.Unmarshal(data, &response)
		if err == nil && len(response.Entities) == 0 {
			err = fmt.Errorf("attach returned no devices")
		}
		if err == nil {
			current, e := j.find(ctx, j.state.Records[index])
			err = e
			if err == nil && current == nil {
				err = fmt.Errorf("attached image is absent from inventory")
			}
			if err == nil && !sameDevices(response.Entities, current.Entities) {
				err = fmt.Errorf("attach devices differ from live inventory")
			}
			if err == nil {
				j.state.Records[index].State = "attached"
				j.state.Records[index].Entities = current.Entities
				j.state.Records[index].PID = current.PID
				err = j.save()
			}
		}
	}
	if err == nil {
		err = ctx.Err()
	}
	if err != nil {
		cleanup, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		return nil, errors.Join(err, j.recoverRecord(cleanup, index))
	}
	return data, nil
}

// Detach detaches the recorded image containing device, after checking its live
// identity and entire device set. It never falls back to detaching an unrecorded
// device. A busy or uncertain attachment stays recorded for later recovery.
func (j *MountJournal) Detach(ctx context.Context, device string) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.lock == nil {
		return fmt.Errorf("mount journal is closed")
	}
	for i, r := range j.state.Records {
		for _, e := range r.Entities {
			if e.Device == device {
				return j.recoverRecord(ctx, i)
			}
		}
	}
	return fmt.Errorf("device is not owned by this mount journal")
}

// Recover reconciles recorded attachments against live inventory and detaches
// only proven matches. An absent image with unresolved attach intent remains an
// error: absence alone cannot prove that a killed attach will not finish later.
// Callers must not start another writer until Recover succeeds.
func (j *MountJournal) Recover(ctx context.Context) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.lock == nil {
		return fmt.Errorf("mount journal is closed")
	}
	var result error
	for i := range j.state.Records {
		result = errors.Join(result, j.recoverRecord(ctx, i))
	}
	return result
}

func (j *MountJournal) inventory(ctx context.Context) ([]mountedImage, error) {
	data, err := j.run(ctx, "info", "-plist")
	if err != nil {
		return nil, err
	}
	var report struct {
		Images []mountedImage `plist:"images"`
	}
	if _, err := plist.Unmarshal(data, &report); err != nil {
		return nil, fmt.Errorf("decode disk image inventory: %w", err)
	}
	if report.Images == nil {
		return nil, fmt.Errorf("disk image inventory is missing images")
	}
	return report.Images, nil
}

func (j *MountJournal) find(ctx context.Context, r mountRecord) (*mountedImage, error) {
	rel, err := filepath.Rel(j.directory, r.Alias)
	if err != nil || !filepath.IsLocal(rel) {
		return nil, fmt.Errorf("image link is outside its journal")
	}
	dev, ino, err := fileIdentity(r.Alias)
	if err != nil {
		return nil, err
	}
	if dev != r.FileDevice || ino != r.FileInode {
		return nil, fmt.Errorf("journal image file identity changed")
	}
	images, err := j.inventory(ctx)
	if err != nil {
		return nil, err
	}
	var found *mountedImage
	for _, im := range images {
		if filepath.Clean(im.Path) != r.Alias {
			continue
		}
		if found != nil {
			return nil, fmt.Errorf("multiple attachments for private image link")
		}
		if im.UID == nil || *im.UID != r.UID {
			return nil, fmt.Errorf("image attachment owner changed")
		}
		if len(r.Entities) != 0 && (!sameDevices(r.Entities, im.Entities) || (r.PID != 0 && r.PID != im.PID)) {
			return nil, fmt.Errorf("image attachment identity changed")
		}
		copy := im
		found = &copy
	}
	return found, nil
}

var wholeDisk = regexp.MustCompile(`^/dev/disk[0-9]+$`)

func (j *MountJournal) recoverRecord(ctx context.Context, index int) error {
	r := j.state.Records[index]
	if r.State == "detached" {
		return nil
	}
	if r.State != "attaching" && r.State != "attached" {
		return fmt.Errorf("unknown mount journal state %q", r.State)
	}
	live, err := j.find(ctx, r)
	if err != nil {
		return err
	}
	if live == nil {
		if r.State == "attaching" {
			return fmt.Errorf("attachment outcome is unknown; retaining mount intent")
		}
		j.state.Records[index].State = "detached"
		return j.save()
	}
	if len(live.Entities) == 0 || !wholeDisk.MatchString(live.Entities[0].Device) {
		return fmt.Errorf("disk image inventory has no backing device")
	}
	// Save the complete live identity before detaching an attach whose helper died.
	j.state.Records[index].State = "attached"
	j.state.Records[index].Entities = live.Entities
	j.state.Records[index].PID = live.PID
	if err := j.save(); err != nil {
		return err
	}
	if _, err := j.run(ctx, "detach", live.Entities[0].Device); err != nil {
		return fmt.Errorf("detach owned image: %w", err)
	}
	current, err := j.find(ctx, j.state.Records[index])
	if err != nil {
		return err
	}
	if current != nil {
		return fmt.Errorf("owned image remains attached after detach")
	}
	j.state.Records[index].State = "detached"
	return j.save()
}

func sameDevices(a, b []mountEntity) bool {
	devices := func(entries []mountEntity) []string {
		result := make([]string, 0, len(entries))
		for _, e := range entries {
			result = append(result, e.Device)
		}
		slices.Sort(result)
		return result
	}
	return slices.Equal(devices(a), devices(b))
}
func fileIdentity(name string) (uint64, uint64, error) {
	info, err := os.Lstat(name)
	if err != nil {
		return 0, 0, err
	}
	if !info.Mode().IsRegular() {
		return 0, 0, fmt.Errorf("private image link is not a regular file")
	}
	stat := info.Sys().(*syscall.Stat_t)
	return uint64(stat.Dev), stat.Ino, nil
}
func diskImageCommand(ctx context.Context, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/usr/bin/hdiutil", args...)
	out, stderr := &boundedOutput{limit: 4 << 20}, &boundedOutput{limit: 1 << 20}
	cmd.Stdout, cmd.Stderr = out, stderr
	if err := runPatcherCommand(ctx, cmd); err != nil {
		return nil, fmt.Errorf("hdiutil %s: %w: %s", args[0], err, stderr.data)
	}
	if out.overflow {
		return nil, fmt.Errorf("disk image plist exceeds limit")
	}
	return out.data, nil
}
