package file

import (
	"crypto/rand"
	"encoding/hex"
	"io"
	"path/filepath"
	"strings"

	storefile "github.com/birdordev/plumego/store/file"
)

func generateID() (string, error) {
	return generateIDFromReader(rand.Reader)
}

func generateIDFromReader(r io.Reader) (string, error) {
	b := make([]byte, 16)
	if _, err := io.ReadFull(r, b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func isPathSafe(path string) bool {
	if strings.Contains(path, "..") {
		return false
	}
	if filepath.IsAbs(path) {
		return false
	}
	return filepath.Clean(path) == path
}

func isListPrefixSafe(prefix string) bool {
	if prefix == "" {
		return true
	}
	trimmed := strings.TrimSuffix(prefix, "/")
	return trimmed != "" && isPathSafe(trimmed)
}

func cleanTenantID(tenantID string) (string, error) {
	tenantID = strings.TrimSpace(tenantID)
	if tenantID == "" || tenantID == "." {
		return "", storefile.ErrInvalidPath
	}
	if strings.Contains(tenantID, "..") || strings.ContainsAny(tenantID, `/\`) {
		return "", storefile.ErrInvalidPath
	}
	if filepath.IsAbs(tenantID) || filepath.Clean(tenantID) != tenantID {
		return "", storefile.ErrInvalidPath
	}
	return tenantID, nil
}

func mimeToExt(mimeType string) string {
	switch mimeType {
	case "image/jpeg":
		return ".jpg"
	case "image/png":
		return ".png"
	case "image/gif":
		return ".gif"
	case "image/webp":
		return ".webp"
	case "image/svg+xml":
		return ".svg"
	case "application/pdf":
		return ".pdf"
	case "text/plain":
		return ".txt"
	case "text/html":
		return ".html"
	case "text/css":
		return ".css"
	case "text/javascript":
		return ".js"
	case "application/json":
		return ".json"
	case "application/xml":
		return ".xml"
	case "application/zip":
		return ".zip"
	default:
		return ""
	}
}

func extToMime(ext string) string {
	ext = strings.ToLower(ext)
	if !strings.HasPrefix(ext, ".") {
		ext = "." + ext
	}
	switch ext {
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	case ".svg":
		return "image/svg+xml"
	case ".pdf":
		return "application/pdf"
	case ".txt":
		return "text/plain"
	case ".html":
		return "text/html"
	case ".css":
		return "text/css"
	case ".js":
		return "text/javascript"
	case ".json":
		return "application/json"
	case ".xml":
		return "application/xml"
	case ".zip":
		return "application/zip"
	default:
		return "application/octet-stream"
	}
}
