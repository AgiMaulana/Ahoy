// Package appdir resolves Ahoy's per-user directory, the single place the CLI
// keeps state that is not tied to a project.
package appdir

import (
	"fmt"
	"os"
	"path/filepath"
)

// Dir returns the Ahoy home directory: $AHOY_HOME when set, otherwise ~/.ahoy.
// It does not create or check the directory.
func Dir() (string, error) {
	if base := os.Getenv("AHOY_HOME"); base != "" {
		return base, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	return filepath.Join(home, ".ahoy"), nil
}
