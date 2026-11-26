package billing

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	Native = "native"
	Focus  = "focus-1.0"

	MaxPartBytes  = 16 << 20
	MaxTotalBytes = 64 << 20
	MaxParts      = 32
	maxRows       = 250000
)

var (
	ErrInvalid    = errors.New("invalid billing input")
	ErrConflict   = errors.New("billing report revision changed")
	ErrNotFound   = errors.New("billing report not found")
	ErrCorrupt    = errors.New("billing archive verification failed")
	currencyCode  = regexp.MustCompile(`^[A-Z]{3}$`)
	decimalFormat = regexp.MustCompile(`^[+-]?[0-9]{1,26}(\.[0-9]{1,12})?$`)
)

type Key struct {
	Tenant, Project, Account, Period, Source string
}

func (k Key) Valid() bool {
	period, err := time.Parse("2006-01", k.Period)
	return validID(k.Tenant) && validID(k.Project) && validID(k.Account) &&
		err == nil && period.Year() >= 2000 && (k.Source == Native || k.Source == Focus)
}

func validID(value string) bool {
	return value != "" && len(value) <= 128 && utf8.ValidString(value) &&
		strings.TrimSpace(value) == value && !strings.ContainsRune(value, 0)
}

type Columns struct {
	Account   string `json:"account"`
	Project   string `json:"project,omitempty"`
	Period    string `json:"period"`
	Currency  string `json:"currency,omitempty"`
	Billed    string `json:"billed"`
	Effective string `json:"effective,omitempty"`
	List      string `json:"list,omitempty"`
	Resource  string `json:"resource,omitempty"`
	Service   string `json:"service,omitempty"`
	Region    string `json:"region,omitempty"`
	Category  string `json:"category,omitempty"`
	Tags      string `json:"tags,omitempty"`
}

type Manifest struct {
	Provider       string            `json:"provider"`
	Columns        *Columns          `json:"columns,omitempty"`
	Currency       string            `json:"currency,omitempty"`
	OffsetMinutes  int               `json:"offset_minutes,omitempty"`
	ExpectedBilled map[string]string `json:"expected_billed,omitempty"`
}

func (m Manifest) validate(source string) error {
	if focusProvider(m.Provider) == "" || len(m.ExpectedBilled) > 64 ||
		m.OffsetMinutes < -12*60 || m.OffsetMinutes > 14*60 {
		return ErrInvalid
	}
	for currency, amount := range m.ExpectedBilled {
		if !currencyCode.MatchString(currency) || !decimalFormat.MatchString(amount) {
			return fmt.Errorf("%w: invalid reconciliation amount or currency", ErrInvalid)
		}
	}
	if source == Focus {
		if m.Columns != nil || m.Currency != "" {
			return ErrInvalid
		}
		return nil
	}
	c := m.Columns
	if c == nil || m.OffsetMinutes != 0 || c.Account == "" || c.Period == "" || c.Billed == "" ||
		(c.Currency == "") == (m.Currency == "") || (m.Currency != "" && !currencyCode.MatchString(m.Currency)) ||
		(m.Provider == "huawei-ecs") != (c.Project != "") {
		return ErrInvalid
	}
	return nil
}

func focusProvider(provider string) string {
	switch provider {
	case "tencent-cvm":
		return "Tencent Cloud"
	case "alibaba-ecs":
		return "Alibaba Cloud"
	case "huawei-ecs":
		return "Huawei Cloud"
	default:
		return ""
	}
}

type Report struct {
	Provider   string    `json:"provider"`
	Account    string    `json:"account"`
	Period     string    `json:"period"`
	Source     string    `json:"source"`
	Revision   int64     `json:"revision"`
	Digest     string    `json:"sha256"`
	ImportedAt time.Time `json:"imported_at"`
	Rows       int       `json:"rows"`
	Parts      []Part    `json:"parts"`
	Totals     []Total   `json:"totals"`
	Current    bool      `json:"current"`
	Replayed   bool      `json:"replayed,omitempty"`
}

type Part struct {
	Number int    `json:"number"`
	Digest string `json:"sha256"`
	Bytes  int    `json:"bytes"`
	Rows   int    `json:"rows"`
}

type Total struct {
	Currency       string  `json:"currency"`
	Billed         string  `json:"billed_cost"`
	Effective      *string `json:"effective_cost,omitempty"`
	List           *string `json:"list_cost,omitempty"`
	ExpectedBilled *string `json:"expected_billed_cost,omitempty"`
	Variance       *string `json:"variance,omitempty"`
}
