package trace

import (
	"encoding/json"
	"os"
	"path/filepath"
)

func WriteJSONL(path string, events []Event) error {
	// Traces record tool inputs/outputs, which may contain secrets. Keep both
	// the directory and file owner-only.
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	// OpenFile's mode applies only when creating a file. Tighten an existing
	// trace as well, including files produced by older versions with mode 0644.
	if err := file.Chmod(0o600); err != nil {
		file.Close()
		return err
	}
	defer file.Close()

	encoder := json.NewEncoder(file)
	for _, event := range events {
		if err := encoder.Encode(event); err != nil {
			return err
		}
	}
	return nil
}
