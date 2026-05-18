//go:build !darwin

package vmconfig

func markFinderPackage(path string) error {
	return nil
}
