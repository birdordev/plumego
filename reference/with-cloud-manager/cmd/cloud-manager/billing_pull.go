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
)

const billingPullProfiles = "CLOUD_MANAGER_BILLING_PULL_PROFILES"
const billingPullUsage = "usage: cloud-manager billing-pull-controller | billing-pull TENANT PROJECT ACCOUNT PERIOD SOURCE | billing-pull-status TENANT PROJECT ACCOUNT PERIOD SOURCE | billing-pull-source TENANT PROJECT ACCOUNT PERIOD SOURCE SHA256 OBJECT_NUMBER | billing-pull-replay TENANT PROJECT ACCOUNT PERIOD SOURCE SHA256"

func runBillingPull(ctx context.Context, db *sql.DB, args []string, path string, output io.Writer, transport http.RoundTripper) error {
	store := billing.NewStore(db)
	if args[0] == "billing-pull-controller" {
		plan, err := loadBillingPullPlan(store, path, transport, time.Now())
		if err != nil {
			return err
		}
		return runBillingPullController(ctx, plan, time.Now)
	}
	key := billing.Key{Tenant: args[1], Project: args[2], Account: args[3], Period: args[4], Source: args[5]}
	if !key.Valid() {
		return errors.New(billingPullUsage)
	}
	ctx, cancel := context.WithTimeout(ctx, 70*time.Second)
	defer cancel()
	switch args[0] {
	case "billing-pull":
		return pullConfiguredBill(ctx, store, path, key, output, transport, time.Now())
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

func pullConfiguredBill(ctx context.Context, store billing.Store, path string, key billing.Key, output io.Writer, transport http.RoundTripper, now time.Time) error {
	plan, err := loadBillingPullPlan(store, path, transport, now)
	if err != nil {
		return err
	}
	job, ok := plan.find(key)
	if !ok {
		return errors.New("billing pull scope is not configured")
	}
	status, err := job.puller.RunOnce(ctx, true)
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

func runBillingPullController(ctx context.Context, plan billingPullPlan, calendarNow func() time.Time) error {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for ctx.Err() == nil {
		if err := plan.refresh(calendarNow()); err != nil {
			return err
		}
		for i := range plan.sources {
			if err := pollScheduledBills(ctx, plan.sources[i].bills); err != nil {
				return err
			}
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
	return nil
}

func pollScheduledBills(ctx context.Context, bills []scheduledBill) error {
	for i := range bills {
		if ctx.Err() != nil {
			return nil
		}
		job := &bills[i]
		if time.Now().Before(job.next) {
			continue
		}
		status, err := job.puller.RunOnce(ctx, false)
		if ctx.Err() != nil {
			return nil
		}
		if errors.Is(err, identity.ErrForbidden) || errors.Is(err, billing.ErrPullProfile) || errors.Is(err, billing.ErrPullLease) {
			job.next = time.Now().Add(time.Minute)
			continue
		}
		if err != nil {
			return errors.New("billing pull controller state unavailable")
		}
		job.next = status.NextAt
	}
	return nil
}
