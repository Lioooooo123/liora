package config

import (
	"bufio"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

func LoadDefaultEnv() error {
	if err := LoadDotEnvLocal(".env.local"); err != nil {
		return err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	return LoadDotEnvLocal(filepath.Join(home, ".config", "liora", ".env"))
}

func LoadDotEnvLocal(path string) error {
	file, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.Trim(strings.TrimSpace(value), `"'`)
		if key == "" || os.Getenv(key) != "" {
			continue
		}
		if err := os.Setenv(key, value); err != nil {
			return err
		}
	}
	return scanner.Err()
}
