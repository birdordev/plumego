package frontend

import (
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
)

func cleanAssetPath(raw string) (string, bool) {
	if raw == "" || strings.Contains(raw, "\x00") || strings.Contains(raw, "\\") {
		return "", false
	}
	if path.IsAbs(raw) {
		return "", false
	}
	if hasUnsafePathSegment(raw) {
		return "", false
	}

	cleaned := path.Clean(raw)
	if cleaned == "." || path.IsAbs(cleaned) {
		return "", false
	}
	if hasUnsafePathSegment(cleaned) {
		return "", false
	}
	return filepath.ToSlash(cleaned), true
}

func hasUnsafePathSegment(raw string) bool {
	for _, part := range strings.Split(raw, "/") {
		if part == "." || part == ".." {
			return true
		}
	}
	return false
}

type localDirFS string

func (fsys localDirFS) Open(name string) (http.File, error) {
	cleaned, ok := cleanAssetPath(name)
	if !ok {
		return nil, os.ErrNotExist
	}

	return os.OpenInRoot(string(fsys), filepath.FromSlash(cleaned))
}

func validateDirectoryIndex(fsys localDirFS, indexFile string) error {
	f, err := fsys.Open(indexFile)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	stat, err := f.Stat()
	if err != nil {
		return err
	}
	if stat.IsDir() {
		return os.ErrInvalid
	}
	return nil
}
