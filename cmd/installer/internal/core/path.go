package core

import (
	"fmt"
	"os"
	"path/filepath"
)

func validateInstallPath(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve install directory: %w", err)
	}
	absolute = filepath.Clean(absolute)
	volume := filepath.VolumeName(absolute)
	if absolute == volume+string(filepath.Separator) {
		return "", fmt.Errorf("refusing to use a filesystem root as the install directory")
	}
	current := string(filepath.Separator)
	if volume != "" {
		current = volume + string(filepath.Separator)
	}
	parts := splitPathParts(stringsTrimVolume(absolute, volume))
	for _, part := range parts {
		current = filepath.Join(current, part)
		info, statErr := os.Lstat(current)
		if os.IsNotExist(statErr) {
			continue
		}
		if statErr != nil {
			return "", fmt.Errorf("inspect install path %s: %w", current, statErr)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("refusing install path with symlink component: %s", current)
		}
	}
	if info, statErr := os.Lstat(absolute); statErr == nil && !info.IsDir() {
		return "", fmt.Errorf("install target exists and is not a directory: %s", absolute)
	} else if statErr != nil && !os.IsNotExist(statErr) {
		return "", fmt.Errorf("inspect install target: %w", statErr)
	}
	return absolute, nil
}

func stringsTrimVolume(path, volume string) string {
	return filepath.Clean(path[len(volume):])
}

func splitPathParts(path string) []string {
	var parts []string
	for path != string(filepath.Separator) && path != "." && path != "" {
		dir, base := filepath.Split(path)
		if base != "" {
			parts = append([]string{base}, parts...)
		}
		path = filepath.Clean(dir)
	}
	return parts
}

func validateRegularTarget(path string) error {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect target %s: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("refusing unsafe managed-file target: %s", path)
	}
	return nil
}

func validateDirectoryTarget(path string) error {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect data directory %s: %w", path, err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("refusing unsafe data-directory target: %s", path)
	}
	return nil
}
