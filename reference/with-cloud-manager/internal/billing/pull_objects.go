package billing

import (
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"slices"
	"strings"

	"with-cloud-manager/internal/provider"
)

const (
	pullCSV  = "csv"
	pullZIP  = "zip"
	pullGzip = "gzip"
)

var ErrPullArchive = errors.New("invalid billing export archive")

type PullObject struct {
	Key     string   `json:"key"`
	SHA256  string   `json:"sha256,omitempty"`
	Format  string   `json:"format"`
	Entries []string `json:"entries,omitempty"`
}

type pulledObject struct {
	PullObject
	provider.ObjectMeta
	raw []byte
}

func (o PullObject) valid() bool {
	if !provider.ValidObjectKey(o.Key) || o.SHA256 != "" && !observationDigest.MatchString(o.SHA256) {
		return false
	}
	switch o.Format {
	case pullCSV, pullGzip:
		return len(o.Entries) == 0
	case pullZIP:
		if len(o.Entries) < 1 || len(o.Entries) > MaxParts {
			return false
		}
		for i, name := range o.Entries {
			if !provider.ValidObjectKey(name) || !strings.HasSuffix(strings.ToLower(name), ".csv") ||
				slices.Contains(o.Entries[:i], name) {
				return false
			}
		}
		return true
	default:
		return false
	}
}

type expandedBill struct {
	parts [][]byte
	bytes int
}

func (bill *expandedBill) add(ctx context.Context, source io.Reader) error {
	limit := min(MaxPartBytes, MaxTotalBytes-bill.bytes)
	if limit < 1 || len(bill.parts) >= MaxParts {
		return provider.ErrObjectSize
	}
	reader := io.LimitReader(billingContextReader{ctx: ctx, source: source}, int64(limit)+1)
	data, err := io.ReadAll(reader)
	if err != nil && ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil || len(data) == 0 {
		return ErrPullArchive
	}
	if len(data) > limit {
		return provider.ErrObjectSize
	}
	bill.bytes += len(data)
	bill.parts = append(bill.parts, data)
	return nil
}

type billingContextReader struct {
	ctx    context.Context
	source io.Reader
}

func (r billingContextReader) Read(data []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.source.Read(data)
}

func (bill *expandedBill) appendObject(ctx context.Context, object pulledObject) error {
	switch object.Format {
	case pullCSV:
		if err := ctx.Err(); err != nil {
			return err
		}
		if len(bill.parts) >= MaxParts || len(object.raw) > MaxPartBytes || len(object.raw) > MaxTotalBytes-bill.bytes {
			return provider.ErrObjectSize
		}
		bill.bytes += len(object.raw)
		bill.parts = append(bill.parts, object.raw)
		return nil
	case pullGzip:
		stream := bytes.NewReader(object.raw)
		reader, err := gzip.NewReader(stream)
		if err != nil {
			return ErrPullArchive
		}
		defer func() { _ = reader.Close() }()
		reader.Multistream(false)
		if err := bill.add(ctx, reader); err != nil {
			return err
		}
		if stream.Len() != 0 {
			return ErrPullArchive
		}
		return nil
	case pullZIP:
		return bill.appendZIP(ctx, object)
	default:
		return ErrPullArchive
	}
}

func (bill *expandedBill) appendZIP(ctx context.Context, object pulledObject) error {
	archive, err := zip.NewReader(bytes.NewReader(object.raw), int64(len(object.raw)))
	if err != nil || len(archive.File) > 512 {
		return ErrPullArchive
	}
	wanted := make(map[string]*zip.File, len(object.Entries))
	for _, name := range object.Entries {
		wanted[name] = nil
	}
	for _, file := range archive.File {
		previous, selected := wanted[file.Name]
		if !selected {
			continue
		}
		if previous != nil || !file.Mode().IsRegular() || file.UncompressedSize64 == 0 {
			return ErrPullArchive
		}
		if file.UncompressedSize64 > MaxPartBytes {
			return provider.ErrObjectSize
		}
		wanted[file.Name] = file
	}
	for _, name := range object.Entries {
		file := wanted[name]
		if file == nil {
			return ErrPullArchive
		}
		reader, err := file.Open()
		if err != nil {
			return ErrPullArchive
		}
		err = bill.add(ctx, reader)
		closeErr := reader.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return ErrPullArchive
		}
	}
	return nil
}
