package iosbundle

import (
	"errors"
	"fmt"
	"os"
)

// ValidatePrepared checks configuration and access to existing bundle files
// without writing them. It does not validate firmware contents, native hardware
// support, or guest boot. Files must resolve within dir, including symlinks.
func (c Config) ValidatePrepared(dir string) error {
	if err := c.Validate(); err != nil {
		return fmt.Errorf("ios configuration: %w", err)
	}
	var problems []error
	if c.ROM == "" {
		problems = append(problems, errors.New("ios rom: configure a prepared boot rom with a bundle-relative path"))
	}
	if c.BootArgs != "" {
		problems = append(problems, errors.New("ios bootArgs: runtime nvram updates are unsupported; prepare nvram externally and clear bootArgs"))
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return errors.Join(append(problems, fmt.Errorf("ios bundle %q: open directory: %w", dir, err))...)
	}
	defer root.Close()
	type artifact struct {
		name  string
		write bool
	}
	files := []artifact{
		{"hw.model", false},
		{"machine.id", false},
		{"aux.img", true},
		{"sep.img", true},
		{"disk.img", true},
	}
	if c.ROM != "" {
		files = append(files, artifact{c.ROM, false})
	}
	if c.SEPROM != "" {
		files = append(files, artifact{c.SEPROM, false})
	}
	var seen []os.FileInfo
	var names []string
	for _, file := range files {
		info, err := root.Stat(file.name)
		if err != nil {
			problems = append(problems, fmt.Errorf("ios file %q: resolve existing file inside bundle: %w", file.name, err))
			continue
		}
		if !info.Mode().IsRegular() || info.Size() == 0 {
			problems = append(problems, fmt.Errorf("ios file %q: expected a nonempty regular file", file.name))
			continue
		}
		flags, access := os.O_RDONLY, "read"
		if file.write {
			flags, access = os.O_RDWR, "read/write"
		}
		f, err := root.OpenFile(file.name, flags, 0)
		if err != nil {
			problems = append(problems, fmt.Errorf("ios file %q: %s access required: %w", file.name, access, err))
			continue
		}
		opened, statErr := f.Stat()
		closeErr := f.Close()
		if statErr != nil || closeErr != nil {
			problems = append(problems, fmt.Errorf("ios file %q: inspect opened file: %w", file.name, errors.Join(statErr, closeErr)))
			continue
		}
		if !os.SameFile(info, opened) || !opened.Mode().IsRegular() || opened.Size() == 0 {
			problems = append(problems, fmt.Errorf("ios file %q: changed during validation; retry with an idle bundle", file.name))
			continue
		}
		for i, previous := range seen {
			if os.SameFile(previous, opened) {
				problems = append(problems, fmt.Errorf("ios file %q: aliases %q; each state and firmware artifact must be a separate file", file.name, names[i]))
			}
		}
		seen = append(seen, opened)
		names = append(names, file.name)
	}
	return errors.Join(problems...)
}
