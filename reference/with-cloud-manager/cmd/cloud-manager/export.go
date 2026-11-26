package main

import (
	"encoding/csv"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"with-cloud-manager/internal/inventory"
	"with-cloud-manager/internal/provider"
)

func writeAssetCSV(w http.ResponseWriter, r *http.Request, assets []inventory.Asset) error {
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="assets.csv"`)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	out := csv.NewWriter(w)
	addresses := r.URL.Query().Get("kind") == string(provider.ElasticIP)
	snapshots := r.URL.Query().Get("kind") == string(provider.Snapshot)
	columns := []string{"account", "region", "kind", "id", "name", "status", "inventory_state", "observed_at", "last_full_sync"}
	if addresses {
		columns = append(columns, "public_addresses")
	}
	if snapshots {
		columns = append(columns, "source_volume_id", "snapshot_class", "size_gib")
	}
	if err := out.Write(columns); err != nil {
		return err
	}
	for _, asset := range assets {
		last := ""
		if asset.LastFullSync != nil {
			last = asset.LastFullSync.UTC().Format(time.RFC3339Nano)
		}
		record := []string{
			safeCSVCell(asset.Account), safeCSVCell(asset.Region), string(asset.Kind), safeCSVCell(asset.ID),
			safeCSVCell(asset.Name), safeCSVCell(asset.Status), asset.State,
			asset.ObservedAt.UTC().Format(time.RFC3339Nano), last,
		}
		if addresses {
			record = append(record, safeCSVCell(strings.Join(asset.PublicAddresses, " ")))
		}
		if snapshots {
			class, size := "", ""
			if asset.Snapshot != nil {
				class = safeCSVCell(asset.Snapshot.Class)
				if asset.Snapshot.SizeGiB != 0 {
					size = strconv.FormatInt(asset.Snapshot.SizeGiB, 10)
				}
			}
			record = append(record, safeCSVCell(asset.ParentID), class, size)
		}
		if err := out.Write(record); err != nil {
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
