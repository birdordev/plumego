package main

import (
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"

	"with-cloud-manager/internal/billing"
	"with-cloud-manager/internal/provider/publiccloud"
)

const (
	billingPeriodToken        = "{period}"
	billingCompactPeriodToken = "{compact_period}"
	maxPreviousBillingMonths  = 3
)

var errBillingPullProfiles = errors.New("invalid billing pull profiles")

type billingMonthWindow struct {
	UTCOffsetMinutes int `json:"utc_offset_minutes"`
	PreviousMonths   int `json:"previous_months"`
}

type billingPullProfile struct {
	billing.PullJob
	Credentials publiccloud.ObjectCredentials `json:"credentials"`
	MonthWindow *billingMonthWindow           `json:"month_window,omitempty"`
}

type scheduledBill struct {
	key    billing.Key
	puller billing.Puller
	next   time.Time
}

type billingPullSource struct {
	job      billing.PullJob
	reader   billing.PullReader
	calendar *time.Location
	previous int
	month    string
	bills    []scheduledBill
}

type billingPullPlan struct {
	store   billing.Store
	sources []billingPullSource
}

func loadBillingPullPlan(store billing.Store, path string, transport http.RoundTripper, now time.Time) (billingPullPlan, error) {
	var profiles []billingPullProfile
	if err := loadPrivateProfileWithin(path, &profiles, 1<<20); err != nil || len(profiles) == 0 || len(profiles) > 128 {
		return billingPullPlan{}, errBillingPullProfiles
	}
	plan := billingPullPlan{store: store, sources: make([]billingPullSource, 0, len(profiles))}
	scopes := make(map[billing.Key]bool, len(profiles))
	keys := make(map[billing.Key]struct{}, len(profiles))
	for _, profile := range profiles {
		scope := profile.Key
		scope.Period = ""
		rolling := profile.MonthWindow != nil
		if existingRolling, found := scopes[scope]; found && (rolling || existingRolling) {
			return billingPullPlan{}, errBillingPullProfiles
		}
		if _, found := keys[profile.Key]; found {
			return billingPullPlan{}, errBillingPullProfiles
		}
		scopes[scope], keys[profile.Key] = rolling, struct{}{}
		source, err := newBillingPullSource(profile, transport)
		if err != nil {
			return billingPullPlan{}, err
		}
		plan.sources = append(plan.sources, source)
	}
	if err := plan.refresh(now); err != nil {
		return billingPullPlan{}, err
	}
	return plan, nil
}

func newBillingPullSource(profile billingPullProfile, transport http.RoundTripper) (billingPullSource, error) {
	reader, err := publiccloud.NewObjectReader(profile.Manifest.Provider, profile.Region, profile.Bucket, profile.Credentials, transport)
	if err != nil {
		return billingPullSource{}, errBillingPullProfiles
	}
	source := billingPullSource{job: profile.PullJob, reader: reader}
	window := profile.MonthWindow
	if window == nil {
		return source, nil
	}
	if profile.Period != "" || window.PreviousMonths < 0 || window.PreviousMonths > maxPreviousBillingMonths ||
		window.UTCOffsetMinutes < -12*60 || window.UTCOffsetMinutes > 14*60 ||
		(profile.Source == billing.Focus && profile.Manifest.OffsetMinutes != window.UTCOffsetMinutes) ||
		len(profile.Manifest.ExpectedBilled) != 0 {
		return billingPullSource{}, errBillingPullProfiles
	}
	for _, object := range profile.Objects {
		if object.SHA256 != "" || !(strings.Contains(object.Key, billingPeriodToken) || strings.Contains(object.Key, billingCompactPeriodToken)) {
			return billingPullSource{}, errBillingPullProfiles
		}
	}
	source.calendar = time.FixedZone("billing", window.UTCOffsetMinutes*60)
	source.previous = window.PreviousMonths
	return source, nil
}

func (plan billingPullPlan) refresh(now time.Time) error {
	for i := range plan.sources {
		if err := plan.sources[i].refresh(plan.store, now); err != nil {
			return err
		}
	}
	return nil
}

func (plan billingPullPlan) find(key billing.Key) (scheduledBill, bool) {
	for _, source := range plan.sources {
		for _, job := range source.bills {
			if job.key == key {
				return job, true
			}
		}
	}
	return scheduledBill{}, false
}

func (source *billingPullSource) refresh(store billing.Store, now time.Time) error {
	month := source.job.Period
	var anchor time.Time
	if source.calendar != nil {
		year, localMonth, _ := now.In(source.calendar).Date()
		anchor = time.Date(year, localMonth, 1, 0, 0, 0, 0, time.UTC)
		month = anchor.Format("2006-01")
	}
	if source.bills != nil && source.month == month {
		return nil
	}
	bills := make([]scheduledBill, 0, source.previous+1)
	for i := 0; i <= source.previous; i++ {
		job := source.job
		if source.calendar != nil {
			period := anchor.AddDate(0, -i, 0)
			job.Period = period.Format("2006-01")
			var err error
			job.Objects, err = expandBillingObjects(job.Objects, job.Period, period.Format("200601"))
			if err != nil {
				return err
			}
		}
		index := slices.IndexFunc(source.bills, func(existing scheduledBill) bool { return existing.key == job.Key })
		if index >= 0 {
			bills = append(bills, source.bills[index])
			continue
		}
		puller, err := billing.NewPuller(store, job, source.reader)
		if err != nil {
			return errBillingPullProfiles
		}
		bills = append(bills, scheduledBill{key: job.Key, puller: puller})
	}
	source.month, source.bills = month, bills
	return nil
}

func expandBillingObjects(objects []billing.PullObject, period, compactPeriod string) ([]billing.PullObject, error) {
	replace := strings.NewReplacer(billingPeriodToken, period, billingCompactPeriodToken, compactPeriod)
	expanded := slices.Clone(objects)
	for i := range expanded {
		object := &expanded[i]
		object.Key = replace.Replace(object.Key)
		if strings.ContainsAny(object.Key, "{}") {
			return nil, errBillingPullProfiles
		}
		object.Entries = slices.Clone(object.Entries)
		for j := range object.Entries {
			object.Entries[j] = replace.Replace(object.Entries[j])
			if strings.ContainsAny(object.Entries[j], "{}") {
				return nil, errBillingPullProfiles
			}
		}
	}
	return expanded, nil
}
