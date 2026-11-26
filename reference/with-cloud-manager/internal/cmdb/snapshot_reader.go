package cmdb

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"strings"

	"with-cloud-manager/internal/inventory"
	"with-cloud-manager/internal/provider"
)

func readSnapshot(input io.Reader, project Project, visit func(inventory.CMDBAsset, int) error) (Manifest, Summary, error) {
	reader := bufio.NewReaderSize(input, maxLineBytes)
	size := 0
	line, first, err := snapshotLine(reader, &size)
	if err != nil {
		return Manifest{}, Summary{}, err
	}
	if first.Type != "manifest" || first.Manifest == nil || first.Asset != nil || first.Summary != nil ||
		first.Manifest.SchemaVersion != SchemaVersion || first.Manifest.Project != project || first.Manifest.CapturedAt.IsZero() {
		return Manifest{}, Summary{}, ErrInvalidSnapshot
	}
	digest := sha256.New()
	_, _ = digest.Write(line)
	count := 0
	for {
		line, entry, err := snapshotLine(reader, &size)
		if err != nil {
			return Manifest{}, Summary{}, err
		}
		switch entry.Type {
		case "complete":
			if entry.Summary == nil || entry.Asset != nil || entry.Manifest != nil ||
				entry.Summary.Count != count || entry.Summary.SHA256 != hex.EncodeToString(digest.Sum(nil)) {
				return Manifest{}, Summary{}, ErrInvalidSnapshot
			}
			if _, err := reader.Peek(1); !errors.Is(err, io.EOF) {
				return Manifest{}, Summary{}, ErrInvalidSnapshot
			}
			return *first.Manifest, *entry.Summary, nil
		case "asset":
			if entry.Asset == nil || entry.Manifest != nil || entry.Summary != nil || !validAsset(*entry.Asset) {
				return Manifest{}, Summary{}, ErrInvalidSnapshot
			}
			if count >= MaxAssets {
				return Manifest{}, Summary{}, ErrSnapshotLimit
			}
			count++
			_, _ = digest.Write(line)
			if err := visit(*entry.Asset, len(line)); err != nil {
				return Manifest{}, Summary{}, err
			}
		default:
			return Manifest{}, Summary{}, ErrInvalidSnapshot
		}
	}
}

func snapshotLine(reader *bufio.Reader, total *int) ([]byte, record, error) {
	line, err := reader.ReadSlice('\n')
	if errors.Is(err, bufio.ErrBufferFull) || len(line)+*total > maxSnapshotBytes {
		return nil, record{}, ErrSnapshotLimit
	}
	if err != nil || len(line) < 3 || line[0] != '{' || line[len(line)-2] != '}' {
		return nil, record{}, ErrInvalidSnapshot
	}
	*total += len(line)
	decoder := json.NewDecoder(bytes.NewReader(line))
	decoder.DisallowUnknownFields()
	var entry record
	if err := decoder.Decode(&entry); err != nil || decoder.InputOffset() != int64(len(line)-1) {
		return nil, record{}, ErrInvalidSnapshot
	}
	return line, entry, nil
}

func validAsset(a inventory.CMDBAsset) bool {
	if a.Account == "" || len(a.Account) > 256 || len(a.Region) > 256 || a.Provider == "" || len(a.Provider) > 128 ||
		a.OwnerProject == "" || len(a.OwnerProject) > 256 || a.OwnershipRevision < 1 || !a.Kind.Valid() ||
		((a.Kind == provider.Region) != (a.Region == "")) || a.Links == nil ||
		strings.ContainsRune(a.Account+a.Region+a.Provider+a.OwnerProject+a.ID+a.Name+a.Status, 0) {
		return false
	}
	if a.State != "observed" && a.State != "stale" && a.State != "missing_candidate" {
		return false
	}
	return (provider.Page{Objects: []provider.Object{{ID: a.ID, Name: a.Name, Status: a.Status, Links: a.Links}}}).Validate("") == nil
}
