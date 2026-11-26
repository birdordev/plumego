package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

	"with-cloud-manager/internal/billing"
	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/provider/publiccloud"
)

const billingPullProfiles = "CLOUD_MANAGER_BILLING_PULL_PROFILES"
const billingPullUsage = "usage: cloud-manager billing-pull-controller | billing-pull TENANT PROJECT ACCOUNT PERIOD SOURCE | billing-pull-status TENANT PROJECT ACCOUNT PERIOD SOURCE | billing-pull-source TENANT PROJECT ACCOUNT PERIOD SOURCE SHA256 OBJECT_NUMBER | billing-pull-replay TENANT PROJECT ACCOUNT PERIOD SOURCE SHA256"

type billingPullProfile struct {
	billing.PullJob
	Credentials publiccloud.ObjectCredentials `json:"credentials"`
}

type scheduledBill struct {
	key    billing.Key
	puller billing.Puller
	next   time.Time
}

func loadBillingPullProfiles(store billing.Store, path string, transport http.RoundTripper) ([]scheduledBill, error) {
	invalid := errors.New("invalid billing pull profiles")
	var profiles []billingPullProfile
	if err := loadPrivateProfileWithin(path, &profiles, 1<<20); err != nil || len(profiles) == 0 || len(profiles) > 128 {
		return nil, invalid
	}
	out := make([]scheduledBill, 0, len(profiles))
	seen := make(map[billing.Key]bool)
	for _, profile := range profiles {
		if seen[profile.Key] {
			return nil, invalid
		}
		seen[profile.Key] = true
		reader, err := publiccloud.NewObjectReader(profile.Manifest.Provider, profile.Region, profile.Bucket, profile.Credentials, transport)
		if err != nil {
			return nil, invalid
		}
		puller, err := billing.NewPuller(store, profile.PullJob, reader)
		if err != nil {
			return nil, invalid
		}
		out = append(out, scheduledBill{key: profile.Key, puller: puller})
	}
	return out, nil
}

func runBillingPull(ctx context.Context, db *sql.DB, args []string, path string, output io.Writer, transport http.RoundTripper) error {
	store := billing.NewStore(db)
	if args[0] == "billing-pull-controller" {
		profiles, err := loadBillingPullProfiles(store, path, transport)
		if err != nil {
			return err
		}
		return runBillingPullController(ctx, profiles)
	}
	key := billing.Key{Tenant: args[1], Project: args[2], Account: args[3], Period: args[4], Source: args[5]}
	if !key.Valid() {
		return errors.New(billingPullUsage)
	}
	ctx, cancel := context.WithTimeout(ctx, 70*time.Second)
	defer cancel()
	switch args[0] {
	case "billing-pull":
		return pullConfiguredBill(ctx, store, path, key, output, transport)
	case "billing-pull-status":
		status, err := store.PullStatus(ctx, key)
		if err != nil {
			return billingStorageError(err)
		}
		return json.NewEncoder(output).Encode(status)
	case "billing-pull-replay":
		report, err := store.VerifyPulledExport(ctx, key, args[6])
		if err != nil {
			return billingStorageError(err)
		}
		return json.NewEncoder(output).Encode(report)
	case "billing-pull-source":
		number, err := strconv.Atoi(args[7])
		if err != nil || strconv.Itoa(number) != args[7] {
			return errors.New(billingPullUsage)
		}
		data, err := store.PulledObject(ctx, key, args[6], number)
		if err != nil {
			return billingStorageError(err)
		}
		_, err = output.Write(data)
		return err
	default:
		return errors.New(billingPullUsage)
	}
}

func pullConfiguredBill(ctx context.Context, store billing.Store, path string, key billing.Key, output io.Writer, transport http.RoundTripper) error {
	profiles, err := loadBillingPullProfiles(store, path, transport)
	if err != nil {
		return err
	}
	for _, profile := range profiles {
		if profile.key != key {
			continue
		}
		status, err := profile.puller.RunOnce(ctx, true)
		if err != nil {
			return billingStorageError(err)
		}
		if err := json.NewEncoder(output).Encode(status); err != nil {
			return err
		}
		switch status.Outcome {
		case "published", "unchanged", "replayed", "historical":
			return nil
		default:
			return errors.New("billing pull did not publish a report")
		}
	}
	return errors.New("billing pull scope is not configured")
}

func runBillingPullController(ctx context.Context, profiles []scheduledBill) error {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for ctx.Err() == nil {
		for i := range profiles {
			profile := &profiles[i]
			if time.Now().Before(profile.next) {
				continue
			}
			status, err := profile.puller.RunOnce(ctx, false)
			if ctx.Err() != nil {
				return nil
			}
			if errors.Is(err, identity.ErrForbidden) || errors.Is(err, billing.ErrPullProfile) || errors.Is(err, billing.ErrPullLease) {
				profile.next = time.Now().Add(time.Minute)
				continue
			}
			if err != nil {
				return errors.New("billing pull controller state unavailable")
			}
			profile.next = status.NextAt
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
	return nil
}
