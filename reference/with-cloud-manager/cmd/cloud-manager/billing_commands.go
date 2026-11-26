package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strconv"

	"with-cloud-manager/internal/billing"
	"with-cloud-manager/internal/identity"
)

const billingStorageUsage = "usage: cloud-manager billing-import TENANT PROJECT ACCOUNT PERIOD SOURCE EXPECTED_REVISION MANIFEST CSV... | billing-replay TENANT PROJECT ACCOUNT PERIOD SOURCE REVISION | billing-source TENANT PROJECT ACCOUNT PERIOD SOURCE REVISION PART"

func runBillingStorageCommand(ctx context.Context, db *sql.DB, args []string, output io.Writer) error {
	usage := errors.New(billingStorageUsage)
	key := billing.Key{Tenant: args[1], Project: args[2], Account: args[3], Period: args[4], Source: args[5]}
	revision, ok := billingRevision(args[6], true)
	if !key.Valid() || !ok {
		return usage
	}
	store := billing.NewStore(db)
	if args[0] == "billing-allocate" || args[0] == "billing-allocation-replay" {
		return runAllocationStorageCommand(ctx, store, key, revision, args, output)
	}
	var report billing.Report
	var err error
	switch args[0] {
	case "billing-import":
		report, err = importBillingFiles(ctx, store, key, revision, args[7], args[8:])
	case "billing-replay":
		report, err = store.Verify(ctx, key, revision)
	case "billing-source":
		part, parseErr := strconv.Atoi(args[7])
		if parseErr != nil || strconv.Itoa(part) != args[7] {
			return usage
		}
		data, err := store.Source(ctx, key, revision, part)
		if err != nil {
			return billingStorageError(err)
		}
		_, err = output.Write(data)
		return err
	default:
		return usage
	}
	if err != nil {
		return billingStorageError(err)
	}
	return json.NewEncoder(output).Encode(report)
}

func importBillingFiles(ctx context.Context, store billing.Store, key billing.Key, revision int64, profile string, names []string) (billing.Report, error) {
	encoded, err := readBillingFile(profile, 32<<10)
	if err != nil {
		return billing.Report{}, err
	}
	reader := json.NewDecoder(bytes.NewReader(encoded))
	reader.DisallowUnknownFields()
	var manifest billing.Manifest
	if err := reader.Decode(&manifest); err != nil || reader.Decode(new(any)) != io.EOF {
		return billing.Report{}, billing.ErrInvalid
	}
	parts := make([][]byte, 0, len(names))
	remaining := billing.MaxTotalBytes
	for _, name := range names {
		if err := ctx.Err(); err != nil {
			return billing.Report{}, err
		}
		part, err := readBillingFile(name, min(billing.MaxPartBytes, remaining))
		if err != nil {
			return billing.Report{}, err
		}
		remaining -= len(part)
		parts = append(parts, part)
	}
	return store.Import(ctx, key, manifest, revision, parts)
}

func readBillingFile(name string, limit int) ([]byte, error) {
	info, err := os.Stat(name)
	if err != nil || !info.Mode().IsRegular() || info.Size() < 1 || info.Size() > int64(limit) {
		return nil, errors.Join(billing.ErrInvalid, errors.New("billing file is unavailable or exceeds size limit"))
	}
	file, err := os.Open(name)
	if err != nil {
		return nil, errors.Join(billing.ErrInvalid, errors.New("billing file is unavailable"))
	}
	defer func() { _ = file.Close() }()
	data, err := io.ReadAll(io.LimitReader(file, int64(limit)+1))
	if err != nil || len(data) == 0 || len(data) > limit {
		return nil, errors.Join(billing.ErrInvalid, errors.New("billing file is incomplete or exceeds size limit"))
	}
	return data, nil
}

func billingStorageError(err error) error {
	switch {
	case errors.Is(err, billing.ErrInvalid), errors.Is(err, billing.ErrConflict), errors.Is(err, billing.ErrAllocationConflict),
		errors.Is(err, billing.ErrPullProfile), errors.Is(err, billing.ErrPullLease),
		errors.Is(err, billing.ErrNotFound), errors.Is(err, billing.ErrCorrupt), errors.Is(err, identity.ErrForbidden),
		errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return err
	default:
		return errors.New("billing storage unavailable")
	}
}
