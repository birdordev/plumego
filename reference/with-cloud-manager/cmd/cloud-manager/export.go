package main

import (
	"encoding/csv"
	"net/http"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"with-cloud-manager/internal/inventory"
)

func writeAssetCSV(w http.ResponseWriter, _ *http.Request, assets []inventory.Asset) error {
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="assets.csv"`)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	out := csv.NewWriter(w)
	if err := out.Write([]string{"account", "region", "kind", "id", "name", "status", "inventory_state", "observed_at", "last_full_sync"}); err != nil {
		return err
	}
	for _, asset := range assets {
		last := ""
		if asset.LastFullSync != nil {
			last = asset.LastFullSync.UTC().Format(time.RFC3339Nano)
		}
		if err := out.Write([]string{
			safeCSVCell(asset.Account), safeCSVCell(asset.Region), string(asset.Kind), safeCSVCell(asset.ID),
			safeCSVCell(asset.Name), safeCSVCell(asset.Status), asset.State,
			asset.ObservedAt.UTC().Format(time.RFC3339Nano), last,
		}); err != nil {
			return err
		}
	}
	out.Flush()
	return out.Error()
}

func safeCSVCell(value string) string {
	trimmed := strings.TrimLeftFunc(value, func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsControl(r) || unicode.Is(unicode.Cf, r)
	})
	if strings.HasPrefix(value, "\t") || strings.HasPrefix(value, "\r") || strings.HasPrefix(value, "\n") {
		return "'" + value
	}
	first, _ := utf8.DecodeRuneInString(trimmed)
	if strings.ContainsRune("=+-@＝＋－＠", first) {
		return "'" + value
	}
	return value
}
