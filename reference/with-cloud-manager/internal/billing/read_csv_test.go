package billing

import (
	"errors"
	"strings"
	"testing"
)

func TestBillingCSVProviderScopes(t *testing.T) {
	base := Key{Tenant: "tenant", Project: "finance", Account: "cloud", Period: "2026-11", Source: Native}
	rows := []struct {
		name, remote, content string
		manifest              Manifest
	}{
		{"tencent", "10001", "\ufeff费用归属方UIN,账单月份,折后总价,资源ID\r\n10001,2026-11,10.012345678901,ins-1\r\n", Manifest{
			Provider: "tencent-cvm", Currency: "CNY", Columns: &Columns{Account: "费用归属方UIN", Period: "账单月份", Billed: "折后总价", Resource: "资源ID"}}},
		{"alibaba", "10002", "资源购买账号ID,账单月份,应付金额,币种\n10002,2026-11,-0.123456789123,USD\n", Manifest{
			Provider: "alibaba-ecs", Columns: &Columns{Account: "资源购买账号ID", Period: "账单月份", Billed: "应付金额", Currency: "币种"}}},
		{"huawei", "domain1:project1", "账号ID,IAM项目ID,账期,应付金额\ndomain1,project1,2026-11,0.01\n", Manifest{
			Provider: "huawei-ecs", Currency: "CNY", Columns: &Columns{Account: "账号ID", Project: "IAM项目ID", Period: "账期", Billed: "应付金额"}}},
	}
	for _, test := range rows {
		t.Run(test.name, func(t *testing.T) {
			if err := test.manifest.validate(Native); err != nil {
				t.Fatal(err)
			}
			lines, err := parseCSV(t.Context(), base, test.manifest, test.remote, []byte(test.content), 10)
			if err != nil || len(lines) != 1 || lines[0].Effective != nil {
				t.Fatalf("lines=%+v, err=%v", lines, err)
			}
			if _, err := parseCSV(t.Context(), base, test.manifest, "other:scope", []byte(test.content), 10); !errors.Is(err, ErrInvalid) {
				t.Fatalf("unexpected account accepted: %v", err)
			}
		})
	}
}

func TestFocusRejectsMalformedAmountsAndScope(t *testing.T) {
	key := Key{Period: "2026-11", Source: Focus}
	manifest := Manifest{Provider: "alibaba-ecs", OffsetMinutes: 480}
	header := "BillingAccountId,BillingCurrency,BilledCost,EffectiveCost,ProviderName,BillingPeriodStart,BillingPeriodEnd"
	row := "10002,CNY,-0.012345678901,0,Alibaba Cloud,2026-10-31T16:00:00Z,2026-12-01T00:00:00+08:00"
	read := func(data string, limit int) ([]billLine, error) {
		return parseCSV(t.Context(), key, manifest, "10002", []byte(data), limit)
	}
	lines, err := read(header+"\n"+row, 1)
	if err != nil || len(lines) != 1 || lines[0].Billed != "-0.012345678901" || lines[0].Effective == nil || *lines[0].Effective != "0" {
		t.Fatalf("lines=%+v, err=%v", lines, err)
	}
	for label, data := range map[string]string{
		"duplicate":  header + ",BillingAccountId\n" + row + ",10002",
		"short":      header + "\n10002,CNY,1",
		"month":      header + "\n" + strings.Replace(row, "12-01", "12-02", 1),
		"provider":   header + "\n" + strings.Replace(row, "Alibaba Cloud", "Tencent Cloud", 1),
		"rounding":   header + "\n" + strings.Replace(row, "-0.012345678901", "1.0000000000009", 1),
		"scientific": header + "\n" + strings.Replace(row, "-0.012345678901", "1e-10", 1),
		"null":       header + "\n" + strings.Replace(row, ",0,", ",,", 1),
		"many rows":  header + "\n" + row + "\n" + row,
	} {
		t.Run(label, func(t *testing.T) {
			if _, err := read(data, 1); !errors.Is(err, ErrInvalid) {
				t.Fatalf("invalid CSV accepted: %v", err)
			}
		})
	}
}
