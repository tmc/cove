package checkpoint

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

type restoreFile struct {
	Next     Entry  `json:"next"`
	Original *Entry `json:"original,omitempty"`
}

type restoreJournal struct {
	Version       int           `json:"version"`
	Phase         string        `json:"phase"`
	Compatibility string        `json:"compatibility"`
	Files         []restoreFile `json:"files"`
}

// Pending reports whether restore recovery must finish before the VM is used.
func (m *Manager) Pending() (bool, error) {
	if err := realDirectory(m.root); err != nil {
		return false, err
	}
	_, err := os.Lstat(filepath.Join(m.root, transactionDirectory))
	if os.IsNotExist(err) {
		return false, nil
	}
	return err == nil, err
}

// Restore installs all checkpoint files. An interrupted restore leaves a
// transaction that Recover must finish before the VM is used again.
func (m *Manager) Restore(name, compatibility string) error {
	release, err := lockOperation(m.root, true)
	if err != nil {
		return err
	}
	defer release()
	manifest, err := m.Read(name)
	if err != nil {
		return err
	}
	if manifest.Compatibility != compatibility {
		return fmt.Errorf("checkpoint compatibility does not match this guest")
	}
	if pending, err := m.Pending(); err != nil {
		return err
	} else if pending {
		return fmt.Errorf("checkpoint restore recovery is required")
	}
	for _, entry := range manifest.Files {
		if err := restoreParents(m.root, entry.Path); err != nil {
			return err
		}
	}
	stage, err := os.MkdirTemp(m.root, ".checkpoint-prepare-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	journal := restoreJournal{Version: Version, Phase: "prepared", Compatibility: compatibility}
	checkpointRoot := filepath.Join(m.root, directory, name)
	for _, entry := range manifest.Files {
		file := restoreFile{Next: entry}
		size, digest, err := hashFile(m.root, entry.Path)
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		if err == nil {
			original := Entry{Source: entry.Source, Size: size, SHA256: digest}
			file.Original = &original
			if err := m.check("backup:" + entry.Path); err != nil {
				return err
			}
			if err := copyFile(filepath.Join(m.root, entry.Path), filepath.Join(stage, "old", entry.Path)); err != nil {
				return err
			}
			if err := verifyEntry(filepath.Join(stage, "old"), original); err != nil {
				return err
			}
			if err := verifyEntry(m.root, original); err != nil {
				return err
			}
		}
		if err := m.check("prepare:" + entry.Path); err != nil {
			return err
		}
		if err := copyFile(filepath.Join(checkpointRoot, entry.Path), filepath.Join(stage, "new", entry.Path)); err != nil {
			return err
		}
		if err := verifyEntry(filepath.Join(stage, "new"), entry); err != nil {
			return err
		}
		journal.Files = append(journal.Files, file)
	}
	if err := writeJSON(filepath.Join(stage, "journal.json"), journal); err != nil {
		return err
	}
	if err := syncTree(stage); err != nil {
		return err
	}
	if err := m.check("prepare-publish"); err != nil {
		return err
	}
	transaction := filepath.Join(m.root, transactionDirectory)
	if err := publish(stage, transaction); err != nil {
		return err
	}
	if err := syncDir(m.root); err != nil {
		return err
	}
	if err := m.check("prepared"); err != nil {
		return err
	}
	journal.Phase = "applying"
	if err := m.writeJournal(transaction, journal); err != nil {
		return err
	}
	for _, file := range journal.Files {
		if err := m.check("replace:" + file.Next.Path); err != nil {
			return err
		}
		if err := matchesCurrent(m.root, file); err != nil {
			return err
		}
		if err := os.Rename(filepath.Join(transaction, "new", file.Next.Path), filepath.Join(m.root, file.Next.Path)); err != nil {
			return err
		}
		if err := syncDir(filepath.Dir(filepath.Join(m.root, file.Next.Path))); err != nil {
			return err
		}
		if err := m.check("replaced:" + file.Next.Path); err != nil {
			return err
		}
	}
	journal.Phase = "committed"
	if err := m.writeJournal(transaction, journal); err != nil {
		return err
	}
	if err := m.check("committed"); err != nil {
		return err
	}
	return m.removeTransaction(transaction)
}

func matchesCurrent(root string, file restoreFile) error {
	_, err := regularFile(root, file.Next.Path)
	if os.IsNotExist(err) && file.Original == nil {
		return nil
	}
	if err != nil {
		return err
	}
	if file.Original == nil {
		return fmt.Errorf("checkpoint target appeared during restore: %s", file.Next.Path)
	}
	return verifyEntry(root, *file.Original)
}

func (m *Manager) writeJournal(root string, journal restoreJournal) error {
	if err := m.check("journal:" + journal.Phase); err != nil {
		return err
	}
	return writeJSON(filepath.Join(root, "journal.json"), journal)
}

func (m *Manager) removeTransaction(root string) error {
	if err := m.check("cleanup"); err != nil {
		return err
	}
	retired, err := os.MkdirTemp(m.root, ".checkpoint-retired-*")
	if err != nil {
		return err
	}
	if err := os.Remove(retired); err != nil {
		return err
	}
	// Retire the complete journal atomically before deleting any recovery data.
	if err := publish(root, retired); err != nil {
		return err
	}
	if err := syncDir(m.root); err != nil {
		return err
	}
	if err := m.check("cleanup-retired"); err != nil {
		return err
	}
	if err := os.RemoveAll(retired); err != nil {
		return err
	}
	return syncDir(m.root)
}

// Recover rolls an uncommitted restore back, or cleans a committed restore.
func (m *Manager) Recover() error {
	release, err := lockOperation(m.root, true)
	if err != nil {
		return err
	}
	defer release()
	pending, err := m.Pending()
	if err != nil || !pending {
		return err
	}
	transaction := filepath.Join(m.root, transactionDirectory)
	if err := realDirectory(transaction); err != nil {
		return err
	}
	journal, err := readJournal(transaction)
	if err != nil {
		return err
	}
	switch journal.Phase {
	case "prepared", "rolled-back":
		return m.removeTransaction(transaction)
	case "committed":
		for _, file := range journal.Files {
			if err := verifyEntry(m.root, file.Next); err != nil {
				return err
			}
		}
		return m.removeTransaction(transaction)
	case "applying", "rolling-back":
	default:
		return fmt.Errorf("unsupported checkpoint restore phase %q", journal.Phase)
	}
	oldRoot := filepath.Join(transaction, "old")
	for _, file := range journal.Files {
		if file.Original != nil {
			if err := verifyEntry(oldRoot, *file.Original); err != nil {
				return err
			}
		}
	}
	journal.Phase = "rolling-back"
	if err := m.writeJournal(transaction, journal); err != nil {
		return err
	}
	for _, file := range journal.Files {
		path := filepath.Join(m.root, file.Next.Path)
		exists := true
		if _, err := regularFile(m.root, file.Next.Path); err != nil {
			if os.IsNotExist(err) {
				exists = false
			} else {
				return err
			}
		}
		if exists {
			nextErr := verifyEntry(m.root, file.Next)
			originalErr := errors.New("original absent")
			if file.Original != nil {
				originalErr = verifyEntry(m.root, *file.Original)
			}
			if nextErr != nil && originalErr != nil {
				return fmt.Errorf("checkpoint target %s changed outside the restore transaction", file.Next.Path)
			}
		}
		if err := m.check("rollback:" + file.Next.Path); err != nil {
			return err
		}
		if file.Original == nil {
			if exists {
				if err := os.Remove(path); err != nil {
					return err
				}
			}
		} else {
			undo := filepath.Join(transaction, "undo", file.Next.Path)
			if err := os.Remove(undo); err != nil && !os.IsNotExist(err) {
				return err
			}
			if err := copyFile(filepath.Join(oldRoot, file.Next.Path), undo); err != nil {
				return err
			}
			if err := verifyEntry(filepath.Join(transaction, "undo"), *file.Original); err != nil {
				return err
			}
			if err := os.Rename(undo, path); err != nil {
				return err
			}
		}
		if err := syncDir(filepath.Dir(path)); err != nil {
			return err
		}
		if err := m.check("rolled-back:" + file.Next.Path); err != nil {
			return err
		}
	}
	journal.Phase = "rolled-back"
	if err := m.writeJournal(transaction, journal); err != nil {
		return err
	}
	return m.removeTransaction(transaction)
}

func readJournal(root string) (restoreJournal, error) {
	var journal restoreJournal
	info, err := os.Lstat(filepath.Join(root, "journal.json"))
	if err != nil {
		return journal, err
	}
	if !info.Mode().IsRegular() {
		return journal, fmt.Errorf("checkpoint restore journal is not regular")
	}
	data, err := readMetadata(filepath.Join(root, "journal.json"))
	if err != nil {
		return journal, err
	}
	if err := json.Unmarshal(data, &journal); err != nil {
		return journal, err
	}
	manifest := Manifest{Version: journal.Version, Name: "restore", Compatibility: journal.Compatibility}
	for _, file := range journal.Files {
		manifest.Files = append(manifest.Files, file.Next)
		if file.Original != nil {
			if file.Original.Path != file.Next.Path {
				return journal, fmt.Errorf("checkpoint backup target mismatch")
			}
			if err := validateManifest(Manifest{Version: Version, Name: "original", Compatibility: journal.Compatibility, Files: []Entry{*file.Original}}); err != nil {
				return journal, err
			}
		}
	}
	if err := validateManifest(manifest); err != nil {
		return journal, err
	}
	return journal, nil
}
