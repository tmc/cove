package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/tmc/cove/internal/mutationguard"
	"github.com/tmc/cove/internal/vmconfig"
)

type pullPublicationDeps struct {
	live    func(string) (bool, error)
	handoff func()
}

type pullPublication struct {
	directory       string
	identity        os.FileInfo
	parentIdentity  os.FileInfo
	storageIdentity os.FileInfo
	root            *os.Root
	parent          *os.Root
	lock            *RunLock
	partial         os.FileInfo
	destination     os.FileInfo
	deps            pullPublicationDeps
}

func beginPullPublication(ctx context.Context, plan *pullPlan, opts pullOptions) (*pullPublication, error) {
	return beginPullPublicationWithDeps(ctx, plan, opts, pullPublicationDeps{live: func(dir string) (bool, error) {
		_, live, err := liveVMProcessForDirectory(dir, defaultVMProcessCollector())
		return live, err
	}})
}
func beginPullPublicationWithDeps(ctx context.Context, plan *pullPlan, opts pullOptions, deps pullPublicationDeps) (result *pullPublication, err error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	guard, err := mutationguard.Acquire(vmconfig.StateDir())
	if err != nil {
		return nil, fmt.Errorf("guard pull admission: %w", err)
	}
	defer guard.Release()
	if deps.live == nil {
		return nil, fmt.Errorf("pull owner observation unavailable")
	}
	dir, err := filepath.Abs(plan.VMDir)
	if err != nil {
		return nil, err
	}
	if info, err := os.Lstat(dir); err == nil {
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("pull destination is not a real directory")
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	if err := checkIncompletePullDisk(dir, filepath.Join(dir, "disk.img"), opts.Resume); err != nil {
		return nil, err
	}
	live, err := deps.live(dir)
	if err != nil {
		return nil, fmt.Errorf("observe pull destination owner: %w", err)
	}
	if live {
		return nil, fmt.Errorf("pull destination has a live runtime owner")
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, err
	}
	canonical, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(canonical)
	if err != nil {
		return nil, err
	}
	p := &pullPublication{directory: canonical, root: root, deps: deps}
	p.storageIdentity, err = os.Stat(vmconfig.StateDir())
	if err != nil {
		root.Close()
		return nil, err
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, p.Close())
		}
	}()
	p.identity, err = root.Lstat(".")
	if err != nil {
		return nil, err
	}
	p.parent, err = os.OpenRoot(filepath.Dir(canonical))
	if err != nil {
		return nil, err
	}
	p.parentIdentity, err = p.parent.Lstat(".")
	if err != nil {
		return nil, err
	}
	if err := p.checkDirectory(); err != nil {
		return nil, err
	}
	p.lock, err = AcquireRunLock(canonical)
	if err != nil {
		return nil, fmt.Errorf("lock pull destination: %w", err)
	}
	if err := p.checkOwner(); err != nil {
		return nil, err
	}
	p.destination, err = optionalPullFile(root, "disk.img")
	if err != nil {
		return nil, err
	}
	if _, err := optionalPullFile(root, "disk.img.partial"); err != nil {
		return nil, err
	}
	if !plan.SuppressAliases && plan.VMName != "" {
		if err := vmconfig.EnsureCompatibilityAliasWithGuard(plan.VMName, canonical, guard); err != nil {
			return nil, err
		}
	}
	plan.VMDir = canonical
	return p, nil
}
func (p *pullPublication) Close() error {
	if p == nil {
		return nil
	}
	var err error
	if p.lock != nil {
		err = errors.Join(err, p.lock.Release())
		p.lock = nil
	}
	if p.root != nil {
		err = errors.Join(err, p.root.Close())
		p.root = nil
	}
	if p.parent != nil {
		err = errors.Join(err, p.parent.Close())
		p.parent = nil
	}
	return err
}
func optionalPullFile(root *os.Root, name string) (os.FileInfo, error) {
	info, err := root.Lstat(name)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("pull %s is not a regular file", name)
	}
	return info, nil
}
func (p *pullPublication) checkDirectory() error {
	parent, err := os.Lstat(filepath.Dir(p.directory))
	if err != nil {
		return err
	}
	if !parent.IsDir() || !os.SameFile(parent, p.parentIdentity) {
		return fmt.Errorf("pull destination parent changed")
	}
	current, err := p.parent.Lstat(filepath.Base(p.directory))
	if err != nil {
		return err
	}
	if !current.IsDir() || current.Mode()&os.ModeSymlink != 0 || !os.SameFile(current, p.identity) {
		return fmt.Errorf("pull destination directory changed")
	}
	held, err := p.root.Lstat(".")
	if err != nil {
		return err
	}
	if !os.SameFile(held, p.identity) {
		return fmt.Errorf("pull destination handle changed")
	}
	canonical, err := filepath.EvalSymlinks(p.directory)
	if err != nil {
		return err
	}
	if canonical != p.directory {
		return fmt.Errorf("pull destination path changed")
	}
	return nil
}
func (p *pullPublication) checkOwner() error {
	live, err := p.deps.live(p.directory)
	if err != nil {
		return fmt.Errorf("observe pull destination owner: %w", err)
	}
	if live {
		return fmt.Errorf("pull destination has a live runtime owner")
	}
	return nil
}
func (p *pullPublication) recordPartial(file *os.File) error {
	info, err := file.Stat()
	if err != nil {
		return err
	}
	current, err := optionalPullFile(p.root, "disk.img.partial")
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || current == nil || !os.SameFile(info, current) {
		return fmt.Errorf("pull partial disk identity differs")
	}
	p.partial = info
	return nil
}
func (p *pullPublication) publish(ctx context.Context, digest string) (err error) {
	if p.partial == nil {
		return fmt.Errorf("pull partial disk identity required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := p.lock.Release(); err != nil {
		return err
	}
	p.lock = nil
	if p.deps.handoff != nil {
		p.deps.handoff()
	}
	storage, err := os.Stat(vmconfig.StateDir())
	if err != nil {
		return err
	}
	if !os.SameFile(storage, p.storageIdentity) {
		return fmt.Errorf("pull storage root changed")
	}
	guard, err := mutationguard.Acquire(vmconfig.StateDir())
	if err != nil {
		return fmt.Errorf("guard pull publication: %w", err)
	}
	defer func() { err = errors.Join(err, guard.Release()) }()
	if err := p.checkStorage(guard); err != nil {
		return err
	}
	if err := p.checkDirectory(); err != nil {
		return err
	}
	p.lock, err = AcquireRunLock(p.directory)
	if err != nil {
		return fmt.Errorf("relock pull publication: %w", err)
	}
	defer func() { err = errors.Join(err, p.lock.Release()); p.lock = nil }()
	if err := p.checkDirectory(); err != nil {
		return err
	}
	if err := p.checkOwner(); err != nil {
		return err
	}
	partial, err := optionalPullFile(p.root, "disk.img.partial")
	if err != nil {
		return err
	}
	if partial == nil || !os.SameFile(partial, p.partial) {
		return fmt.Errorf("pull partial disk changed before publication")
	}
	destination, err := optionalPullFile(p.root, "disk.img")
	if err != nil {
		return err
	}
	if (destination == nil) != (p.destination == nil) {
		return fmt.Errorf("pull destination disk changed before publication")
	}
	if destination != nil && (!os.SameFile(destination, p.destination) || destination.Size() != p.destination.Size() || !destination.ModTime().Equal(p.destination.ModTime())) {
		return fmt.Errorf("pull destination disk changed before publication")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := p.checkStorage(guard); err != nil {
		return err
	}
	if err := p.root.Rename("disk.img.partial", "disk.img"); err != nil {
		return fmt.Errorf("publish partial disk: %w", err)
	}
	if err := writePullProvenance(p.directory, digest); err != nil {
		return err
	}
	directory, err := p.root.Open(".")
	if err != nil {
		return err
	}
	return errors.Join(directory.Sync(), directory.Close())
}

func (p *pullPublication) checkStorage(guard *mutationguard.Guard) error {
	if err := guard.Check(vmconfig.StateDir()); err != nil {
		return err
	}
	current, err := os.Stat(vmconfig.StateDir())
	if err != nil {
		return err
	}
	if !os.SameFile(current, p.storageIdentity) {
		return fmt.Errorf("pull storage root changed")
	}
	return nil
}
