package config

import (
	"bufio"
	"os"
)

const envScannerMaxTokenSize = 1024 * 1024

// LoadEnvFile loads environment variables from a file.
// If overwrite is true, existing environment variables will be overwritten.
// If overwrite is false, existing environment variables will be preserved.
func LoadEnvFile(filepath string, overwrite bool) error {
	file, err := os.Open(filepath)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), envScannerMaxTokenSize)
	for scanner.Scan() {
		key, value, ok := parseEnvLine(scanner.Text())
		if !ok {
			continue
		}
		if overwrite || os.Getenv(key) == "" {
			if err := os.Setenv(key, value); err != nil {
				return err
			}
		}
	}

	return scanner.Err()
}
