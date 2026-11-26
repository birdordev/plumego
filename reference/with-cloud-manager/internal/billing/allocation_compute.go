package billing

import (
	"context"
	"database/sql"
	"math/big"
	"slices"
	"strings"
)

type groupKey struct {
	rule     int
	currency string
}

type centerKey struct{ currency, center string }

type allocationProjection struct {
	invalidTagRows int
	totals         []AllocationTotal
}

func computeAllocation(ctx context.Context, tx *sql.Tx, entry reportEntry, policy AllocationPolicy) (allocationProjection, error) {
	source, err := loadReport(ctx, tx, entry)
	if err != nil {
		return allocationProjection{}, err
	}
	groups, invalid, err := allocationGroups(ctx, tx, entry.id, source.Rows, policy.Rules)
	if err != nil {
		return allocationProjection{}, err
	}
	centers := make(map[centerKey]*allocationAmounts)
	if len(policy.Default) == 0 {
		policy.Default = []AllocationShare{{Center: unallocatedCenter, Weight: allocationWeight}}
	}
	for key, group := range groups {
		shares := policy.Default
		if key.rule < len(policy.Rules) {
			shares = policy.Rules[key.rule].Shares
		}
		addShares(centers, key.currency, group, shares)
	}
	if err := reconcileAllocation(source.Totals, centers); err != nil {
		return allocationProjection{}, err
	}
	return allocationProjection{invalidTagRows: invalid, totals: allocationTotals(centers)}, nil
}

func allocationGroups(ctx context.Context, tx *sql.Tx, report string, expected int, rules []AllocationRule) (map[groupKey]*allocationAmounts, int, error) {
	if expected < 0 || expected > maxRows {
		return nil, 0, ErrCorrupt
	}
	rows, err := tx.QueryContext(ctx, `SELECT part_no,row_no,currency,billed::text,effective::text,list_cost::text,
		COALESCE(resource_id,''),COALESCE(service,''),COALESCE(region,''),COALESCE(category,''),COALESCE(tags,'')
		FROM cmp_billing_lines WHERE report_id=$1 ORDER BY part_no,row_no`, report)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = rows.Close() }()
	groups := make(map[groupKey]*allocationAmounts)
	count, invalid := 0, 0
	for rows.Next() {
		var line allocationLine
		var part, row int
		if err := rows.Scan(&part, &row, &line.currency, &line.billed, &line.effective, &line.list,
			&line.resource, &line.service, &line.region, &line.category, &line.tags); err != nil {
			return nil, 0, err
		}
		count++
		if count > expected {
			return nil, 0, ErrCorrupt
		}
		rule, unreadable := allocationMatch(rules, line)
		if unreadable {
			invalid++
		}
		key := groupKey{rule, line.currency}
		group := groups[key]
		if group == nil {
			group = new(allocationAmounts)
			groups[key] = group
		}
		if err := group.add(line); err != nil {
			return nil, 0, explainAllocationPart(part, row, err)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	if count != expected {
		return nil, 0, ErrCorrupt
	}
	return groups, invalid, nil
}

func addShares(centers map[centerKey]*allocationAmounts, currency string, group *allocationAmounts, shares []AllocationShare) {
	billed := splitAmount(&group.billed, shares)
	effective := splitAmount(&group.effective, shares)
	list := splitAmount(&group.list, shares)
	for i, share := range shares {
		key := centerKey{currency, share.Center}
		center := centers[key]
		if center == nil {
			center = new(allocationAmounts)
			centers[key] = center
		}
		center.billed.Add(&center.billed, &billed[i])
		center.effective.Add(&center.effective, &effective[i])
		center.list.Add(&center.list, &list[i])
		center.rows += group.rows
		center.missingEffective += group.missingEffective
		center.missingList += group.missingList
	}
}

func reconcileAllocation(source []Total, centers map[centerKey]*allocationAmounts) error {
	currencies := make(map[string]*allocationAmounts)
	for key, center := range centers {
		sum := currencies[key.currency]
		if sum == nil {
			sum = new(allocationAmounts)
			currencies[key.currency] = sum
		}
		sum.billed.Add(&sum.billed, &center.billed)
		sum.effective.Add(&sum.effective, &center.effective)
		sum.list.Add(&sum.list, &center.list)
		sum.rows += center.rows
		sum.missingEffective += center.missingEffective
		sum.missingList += center.missingList
	}
	for _, total := range source {
		sum, exists := currencies[total.Currency]
		if !exists {
			sum = new(allocationAmounts)
			centers[centerKey{total.Currency, unallocatedCenter}] = sum
		}
		if !sameAmount(&sum.billed, total.Billed) ||
			!sameOptional(&sum.effective, sum.missingEffective, sum.rows, total.Effective) ||
			!sameOptional(&sum.list, sum.missingList, sum.rows, total.List) {
			return ErrCorrupt
		}
		delete(currencies, total.Currency)
	}
	if len(currencies) != 0 {
		return ErrCorrupt
	}
	return nil
}

func sameAmount(atoms *big.Int, expected string) bool {
	value, err := decimalAtoms(expected)
	return err == nil && value.Cmp(atoms) == 0
}

func sameOptional(atoms *big.Int, missing, rows int, expected *string) bool {
	if missing > 0 || rows == 0 {
		return expected == nil
	}
	return expected != nil && sameAmount(atoms, *expected)
}

func allocationTotals(centers map[centerKey]*allocationAmounts) []AllocationTotal {
	totals := make([]AllocationTotal, 0, len(centers))
	for key, value := range centers {
		total := AllocationTotal{
			Currency: key.currency, Center: key.center, Rows: value.rows,
			Billed: decimalAmount(&value.billed), Effective: amountPointer(&value.effective, value.missingEffective, value.rows),
			List: amountPointer(&value.list, value.missingList, value.rows),
		}
		if total.List != nil {
			difference := decimalAmount(new(big.Int).Sub(&value.list, &value.billed))
			total.ListMinusBilled = &difference
		}
		totals = append(totals, total)
	}
	slices.SortFunc(totals, func(a, b AllocationTotal) int {
		if a.Currency != b.Currency {
			return strings.Compare(a.Currency, b.Currency)
		}
		return strings.Compare(a.Center, b.Center)
	})
	return totals
}
