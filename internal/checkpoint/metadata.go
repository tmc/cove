package checkpoint

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func readMetadata(path string) ([]byte, error) {
	const limit = 4 << 20
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if len(data) > limit {
		return nil, fmt.Errorf("checkpoint metadata exceeds %d bytes", limit)
	}
	return data, nil
}

func restoreParents(root, path string) error {
	if err := safePath(path); err != nil {
		return err
	}
	parts := strings.Split(path, string(filepath.Separator))
	current := root
	for _, part := range parts[:len(parts)-1] {
		current = filepath.Join(current, part)
		if err := realDirectory(current); err != nil {
			return fmt.Errorf("checkpoint restore parent: %w", err)
		}
	}
	return nil
}
