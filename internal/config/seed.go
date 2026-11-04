package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// SeedDefaults writes each domain file missing from dir, copied from src, and
// returns the names it created. It never overwrites: an existing file is the
// operator's, whatever it holds. credentials.yaml is not seeded; first-run
// /auth/setup owns it. Call it after MigrateLegacy, which refuses to run
// next to domain files that differ from the legacy values.
func SeedDefaults(dir string, src fs.FS) ([]string, error) {
	var created []string
	for _, d := range Domains {
		name := d.File()
		data, err := fs.ReadFile(src, name)
		if err != nil {
			return created, fmt.Errorf("default %s: %w", name, err)
		}
		f, err := os.OpenFile(filepath.Join(dir, name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if errors.Is(err, fs.ErrExist) {
			continue
		}
		if err != nil {
			return created, fmt.Errorf("create %s: %w", name, err)
		}
		_, werr := f.Write(data)
		if cerr := f.Close(); werr == nil {
			werr = cerr
		}
		if werr != nil {
			os.Remove(f.Name())
			return created, fmt.Errorf("write %s: %w", name, werr)
		}
		created = append(created, name)
	}
	return created, nil
}
