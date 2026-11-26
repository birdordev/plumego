package billing

import (
	"bytes"
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
	"unicode/utf8"
)

type billLine struct {
	Currency  string  `json:"currency"`
	Billed    string  `json:"billed"`
	Effective *string `json:"effective"`
	List      *string `json:"list_cost"`
	Resource  string  `json:"resource_id"`
	Service   string  `json:"service"`
	Region    string  `json:"region"`
	Category  string  `json:"category"`
	Tags      string  `json:"tags"`
}

type decoder struct {
	columns          Columns
	headers          map[string]int
	account, project string
	period, provider string
	start, end       time.Time
	currency         string
	focus            bool
}

func csvDecoder(key Key, manifest Manifest, remote string, header []string) (decoder, error) {
	d := decoder{account: remote, period: key.Period, currency: manifest.Currency,
		focus: key.Source == Focus, headers: make(map[string]int, len(header))}
	if manifest.Provider == "huawei-ecs" {
		account, project, ok := strings.Cut(remote, ":")
		if !ok || !validID(account) || !validID(project) {
			return decoder{}, ErrInvalid
		}
		d.account, d.project = account, project
	}
	if len(header) > 256 {
		return decoder{}, ErrInvalid
	}
	for index, name := range header {
		name = strings.TrimSpace(name)
		if !validID(name) {
			return decoder{}, ErrInvalid
		}
		if _, exists := d.headers[name]; exists {
			return decoder{}, fmt.Errorf("%w: duplicate CSV header", ErrInvalid)
		}
		d.headers[name] = index
	}
	if d.focus {
		d.provider = focusProvider(manifest.Provider)
		d.start, _ = time.ParseInLocation("2006-01", key.Period, time.FixedZone("", manifest.OffsetMinutes*60))
		d.end = d.start.AddDate(0, 1, 0)
		d.columns = Columns{Account: "BillingAccountId", Currency: "BillingCurrency", Billed: "BilledCost",
			Effective: "EffectiveCost", List: "ListCost", Resource: "ResourceId", Service: "ServiceName",
			Region: "RegionId", Category: "ChargeCategory", Tags: "Tags"}
		if d.project != "" {
			d.columns.Project = "SubAccountId"
		}
		for _, field := range []string{"BillingPeriodStart", "BillingPeriodEnd", "ProviderName", "EffectiveCost"} {
			if _, exists := d.headers[field]; !exists {
				return decoder{}, fmt.Errorf("%w: required FOCUS column missing", ErrInvalid)
			}
		}
	} else {
		d.columns = *manifest.Columns
	}
	if err := d.checkColumns(); err != nil {
		return decoder{}, err
	}
	return d, nil
}

func (d decoder) checkColumns() error {
	c := d.columns
	fields := []string{c.Account, c.Period, c.Currency, c.Billed, c.Project}
	if !d.focus {
		fields = append(fields, c.Effective, c.List, c.Resource, c.Service, c.Region, c.Category, c.Tags)
	}
	seen := make(map[string]struct{}, len(fields))
	for _, field := range fields {
		if field == "" {
			continue
		}
		_, exists := d.headers[field]
		_, used := seen[field]
		if !exists || used {
			return fmt.Errorf("%w: CSV column missing or mapped twice", ErrInvalid)
		}
		seen[field] = struct{}{}
	}
	return nil
}

func (d decoder) line(record []string) (billLine, error) {
	value := func(name string) string {
		if index, exists := d.headers[name]; exists {
			return strings.TrimSpace(record[index])
		}
		return ""
	}
	c := d.columns
	if value(c.Account) != d.account || (d.project != "" && value(c.Project) != d.project) {
		return billLine{}, errors.New("billing row is outside the verified account scope")
	}
	if d.focus {
		start, startErr := time.Parse(time.RFC3339Nano, value("BillingPeriodStart"))
		end, endErr := time.Parse(time.RFC3339Nano, value("BillingPeriodEnd"))
		if startErr != nil || endErr != nil || !start.Equal(d.start) || !end.Equal(d.end) || value("ProviderName") != d.provider {
			return billLine{}, errors.New("FOCUS provider or billing bounds do not match")
		}
	} else if value(c.Period) != d.period {
		return billLine{}, errors.New("billing month does not match")
	}
	currency := d.currency
	if currency == "" {
		currency = value(c.Currency)
	}
	billed := value(c.Billed)
	if !currencyCode.MatchString(currency) || !decimalFormat.MatchString(billed) {
		return billLine{}, errors.New("invalid currency or cost")
	}
	line := billLine{Currency: currency, Billed: billed, Resource: value(c.Resource), Service: value(c.Service),
		Region: value(c.Region), Category: value(c.Category), Tags: value(c.Tags)}
	effective, list := value(c.Effective), value(c.List)
	if (effective != "" || d.focus) && !decimalFormat.MatchString(effective) || list != "" && !decimalFormat.MatchString(list) {
		return billLine{}, errors.New("invalid effective or list cost")
	}
	if effective != "" {
		line.Effective = &effective
	}
	if list != "" {
		line.List = &list
	}
	if len(line.Resource) > 1024 || len(line.Service) > 256 || len(line.Region) > 128 ||
		len(line.Category) > 128 || len(line.Tags) > 8192 {
		return billLine{}, errors.New("billing dimension exceeds its size limit")
	}
	return line, nil
}

func parseCSV(ctx context.Context, key Key, m Manifest, remote string, data []byte, limit int) ([]billLine, error) {
	if len(data) == 0 || len(data) > MaxPartBytes || !utf8.Valid(data) || bytes.IndexByte(data, 0) != -1 {
		return nil, fmt.Errorf("%w: billing CSV must be bounded UTF-8 text", ErrInvalid)
	}
	r := csv.NewReader(bytes.NewReader(bytes.TrimPrefix(data, []byte{0xef, 0xbb, 0xbf})))
	r.ReuseRecord = true
	header, err := r.Read()
	if err != nil {
		return nil, fmt.Errorf("%w: invalid CSV header", ErrInvalid)
	}
	d, err := csvDecoder(key, m, remote, header)
	if err != nil {
		return nil, err
	}
	lines := make([]billLine, 0)
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		record, err := r.Read()
		if errors.Is(err, io.EOF) {
			return lines, nil
		}
		number := len(lines) + 2
		if err != nil {
			return nil, fmt.Errorf("%w: malformed CSV row %d", ErrInvalid, number)
		}
		if len(lines) >= limit {
			return nil, fmt.Errorf("%w: too many billing rows", ErrInvalid)
		}
		line, err := d.line(record)
		if err != nil {
			return nil, fmt.Errorf("%w: row %d: %s", ErrInvalid, number, err)
		}
		lines = append(lines, line)
	}
}
