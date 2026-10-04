package checkpoint

import "path/filepath"

// RecoveryPaths returns validated bundle-relative restore targets from the
// pending journal. Callers use them to check external file ownership before
// Recover. It does not acquire runtime ownership.
func (m *Manager) RecoveryPaths() ([]string, error) {
	transaction := filepath.Join(m.root, transactionDirectory)
	if err := realDirectory(transaction); err != nil {
		return nil, err
	}
	journal, err := readJournal(transaction)
	if err != nil {
		return nil, err
	}
	paths := make([]string, 0, len(journal.Files))
	for _, file := range journal.Files {
		paths = append(paths, file.Next.Path)
	}
	return paths, nil
}
