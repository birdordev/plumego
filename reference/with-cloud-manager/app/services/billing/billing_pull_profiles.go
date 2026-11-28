package billingservice

import (
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"

	"with-cloud-manager/app/services/cloud"
	"with-cloud-manager/internal/billing"
	"with-cloud-manager/internal/provider/publiccloud"
)

const (
	billingPeriodToken        = "{period}"
	billingCompactPeriodToken = "{compact_period}"
	maxPreviousBillingMonths  = 3
)

var ErrBillingPullProfiles = errors.New("invalid billing pull profiles")

type BillingMonthWindow struct {
	UTCOffsetMinutes int `json:"utc_offset_minutes"`
	PreviousMonths   int `json:"previous_months"`
}

type BillingPullProfile struct {
	billing.PullJob
	Credentials publiccloud.ObjectCredentials `json:"credentials"`
	MonthWindow *BillingMonthWindow           `json:"month_window,omitempty"`
}

type ScheduledBill struct {
	key    billing.Key
	puller billing.Puller
	Next   time.Time
}

type billingPullSource struct {
	job      billing.PullJob
	reader   billing.PullReader
	calendar *time.Location
	previous int
	month    string
	Bills    []ScheduledBill
}

type billingPullPlan struct {
	store   billing.Store
	Sources []billingPullSource
}

func LoadBillingPullPlan(store billing.Store, path string, transport http.RoundTripper, now time.Time) (billingPullPlan, error) {
	var profiles []BillingPullProfile
	if err := cloud.LoadPrivateProfileWithin(path, &profiles, 1<<20); err != nil || len(profiles) == 0 || len(profiles) > 128 {
		return billingPullPlan{}, ErrBillingPullProfiles
	}
	plan := billingPullPlan{store: store, Sources: make([]billingPullSource, 0, len(profiles))}
	scopes := make(map[billing.Key]bool, len(profiles))
	keys := make(map[billing.Key]struct{}, len(profiles))
	for _, profile := range profiles {
		scope := profile.Key
		scope.Period = ""
		rolling := profile.MonthWindow != nil
		if existingRolling, found := scopes[scope]; found && (rolling || existingRolling) {
			return billingPullPlan{}, ErrBillingPullProfiles
		}
		if _, found := keys[profile.Key]; found {
			return billingPullPlan{}, ErrBillingPullProfiles
		}
		scopes[scope], keys[profile.Key] = rolling, struct{}{}
		source, err := newBillingPullSource(profile, transport)
		if err != nil {
			return billingPullPlan{}, err
		}
		plan.Sources = append(plan.Sources, source)
	}
	if err := plan.Refresh(now); err != nil {
		return billingPullPlan{}, err
	}
	return plan, nil
}

func newBillingPullSource(profile BillingPullProfile, transport http.RoundTripper) (billingPullSource, error) {
	reader, err := publiccloud.NewObjectReader(profile.Manifest.Provider, profile.Region, profile.Bucket, profile.Credentials, transport)
	if err != nil {
		return billingPullSource{}, ErrBillingPullProfiles
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
		return billingPullSource{}, ErrBillingPullProfiles
	}
	for _, object := range profile.Objects {
		hasPeriod := strings.Contains(object.Key, billingPeriodToken) || strings.Contains(object.Key, billingCompactPeriodToken)
		if object.SHA256 != "" || !hasPeriod {
			return billingPullSource{}, ErrBillingPullProfiles
		}
	}
	source.calendar = time.FixedZone("billing", window.UTCOffsetMinutes*60)
	source.previous = window.PreviousMonths
	return source, nil
}

func (plan billingPullPlan) Refresh(now time.Time) error {
	for i := range plan.Sources {
		if err := plan.Sources[i].refresh(plan.store, now); err != nil {
			return err
		}
	}
	return nil
}

func (plan billingPullPlan) Find(key billing.Key) (ScheduledBill, bool) {
	for _, source := range plan.Sources {
		for _, job := range source.Bills {
			if job.key == key {
				return job, true
			}
		}
	}
	return ScheduledBill{}, false
}

func (source *billingPullSource) refresh(store billing.Store, now time.Time) error {
	month := source.job.Period
	var anchor time.Time
	if source.calendar != nil {
		year, localMonth, _ := now.In(source.calendar).Date()
		anchor = time.Date(year, localMonth, 1, 0, 0, 0, 0, time.UTC)
		month = anchor.Format("2006-01")
	}
	if source.Bills != nil && source.month == month {
		return nil
	}
	bills := make([]ScheduledBill, 0, source.previous+1)
	for i := 0; i <= source.previous; i++ {
		job := source.job
		if source.calendar != nil {
			period := anchor.AddDate(0, -i, 0)
			job.Period = period.Format("2006-01")
			var err error
			job.Objects, err = ExpandBillingObjects(job.Objects, job.Period, period.Format("200601"))
			if err != nil {
				return err
			}
		}
		index := slices.IndexFunc(source.Bills, func(existing ScheduledBill) bool { return existing.key == job.Key })
		if index >= 0 {
			bills = append(bills, source.Bills[index])
			continue
		}
		puller, err := billing.NewPuller(store, job, source.reader)
		if err != nil {
			return ErrBillingPullProfiles
		}
		bills = append(bills, ScheduledBill{key: job.Key, puller: puller})
	}
	source.month, source.Bills = month, bills
	return nil
}

func ExpandBillingObjects(objects []billing.PullObject, period, compactPeriod string) ([]billing.PullObject, error) {
	replace := strings.NewReplacer(billingPeriodToken, period, billingCompactPeriodToken, compactPeriod)
	expanded := slices.Clone(objects)
	for i := range expanded {
		object := &expanded[i]
		object.Key = replace.Replace(object.Key)
		if strings.ContainsAny(object.Key, "{}") {
			return nil, ErrBillingPullProfiles
		}
		object.Entries = slices.Clone(object.Entries)
		for j := range object.Entries {
			object.Entries[j] = replace.Replace(object.Entries[j])
			if strings.ContainsAny(object.Entries[j], "{}") {
				return nil, ErrBillingPullProfiles
			}
		}
	}
	return expanded, nil
}
