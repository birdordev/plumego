package publiccloud

import (
	"context"
	"strconv"
	"strings"

	"with-cloud-manager/internal/provider"
)

const (
	regionalInstancePageSize = 50
	maxRegionalInstances     = 500_000
)

type regionalInstanceReader func(context.Context, string, int, string) ([]provider.Object, *int, error)

type regionalInstancePosition struct{ product, offset, total int }

func parseRegionalInstancePosition(cursor string, products, limit int) (regionalInstancePosition, error) {
	if cursor == "" {
		return regionalInstancePosition{}, nil
	}
	parts := strings.Split(cursor, ":")
	if len(parts) == 3 {
		product, productErr := strconv.Atoi(parts[0])
		offset, offsetErr := strconv.Atoi(parts[1])
		total, totalErr := strconv.Atoi(parts[2])
		if productErr == nil && offsetErr == nil && totalErr == nil && product >= 0 && product < products &&
			total <= maxRegionalInstances && cursor == strconv.Itoa(product)+":"+strconv.Itoa(offset)+":"+strconv.Itoa(total) &&
			((offset == 0 && total == 0 && product > 0) ||
				(offset > 0 && offset%limit == 0 && total > offset)) {
			return regionalInstancePosition{product, offset, total}, nil
		}
	}
	return regionalInstancePosition{}, &provider.Failure{Kind: provider.ExpiredCursor}
}

func discoverRegionalInstances(ctx context.Context, region, cursor string, limit int, readers []regionalInstanceReader) (provider.Page, error) {
	if len(readers) == 0 {
		return provider.Page{}, &provider.Failure{Kind: provider.Unsupported}
	}
	position, err := parseRegionalInstancePosition(cursor, len(readers), limit)
	if err != nil {
		return provider.Page{}, err
	}
	rows, total, err := readers[position.product](ctx, region, position.offset, "")
	if err != nil {
		return provider.Page{}, err
	}
	if err := checkRegionalInstancePage(position.offset, limit, total, rows); err != nil {
		return provider.Page{}, err
	}
	if position.offset != 0 && position.total != *total {
		return provider.Page{}, &provider.Failure{Kind: provider.ExpiredCursor}
	}
	next := ""
	if end := position.offset + len(rows); end < *total {
		next = strconv.Itoa(position.product) + ":" + strconv.Itoa(end) + ":" + strconv.Itoa(*total)
	} else if position.product+1 < len(readers) {
		next = strconv.Itoa(position.product+1) + ":0:0"
	}
	return provider.Page{Objects: rows, Next: next}, nil
}

func checkRegionalInstancePage(offset, limit int, total *int, rows []provider.Object) error {
	if rows == nil || total == nil || *total < 0 || *total > maxRegionalInstances || len(rows) > limit {
		return &provider.Failure{Kind: provider.InvalidResponse}
	}
	if offset > *total || len(rows) != min(limit, *total-offset) {
		return &provider.Failure{Kind: provider.ExpiredCursor}
	}
	return nil
}

func observeRegionalInstance(ctx context.Context, region, id string, read regionalInstanceReader) (provider.Object, error) {
	rows, total, err := read(ctx, region, 0, id)
	if err != nil {
		return provider.Object{}, err
	}
	if err := checkRegionalInstancePage(0, regionalInstancePageSize, total, rows); err != nil {
		return provider.Object{}, err
	}
	switch {
	case *total == 0:
		return provider.Object{}, &provider.Failure{Kind: provider.Absent}
	case *total == 1 && rows[0].ID == id:
		item := rows[0]
		if err := (provider.Page{Objects: rows}).Validate(""); err != nil {
			return provider.Object{}, err
		}
		return item, nil
	default:
		return provider.Object{}, &provider.Failure{Kind: provider.InvalidResponse}
	}
}

func projectRegionalInstances[T any](raw []T, total *int, project func(T) (provider.Object, error)) ([]provider.Object, *int, error) {
	if raw == nil || len(raw) > regionalInstancePageSize {
		return nil, nil, &provider.Failure{Kind: provider.InvalidResponse}
	}
	rows := make([]provider.Object, 0, len(raw))
	for _, source := range raw {
		item, err := project(source)
		if err != nil {
			return nil, nil, err
		}
		rows = append(rows, item)
	}
	return rows, total, nil
}
