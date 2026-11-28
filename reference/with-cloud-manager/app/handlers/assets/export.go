package assets

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

func WriteAssetCSV(w http.ResponseWriter, r *http.Request, assets []inventory.Asset) error {
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="assets.csv"`)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	out := csv.NewWriter(w)
	kind := provider.Kind(r.URL.Query().Get("kind"))
	loadBalancers := kind == provider.LoadBalancer
	addresses := kind == provider.ElasticIP || loadBalancers
	snapshots := kind == provider.Snapshot
	volumes := kind == provider.Volume
	databases := kind == provider.DatabaseInstance
	caches := kind == provider.CacheInstance
	clusters := kind == provider.KubernetesCluster
	buckets := kind == provider.ObjectBucket
	columns := []string{"account", "region", "kind", "id", "name", "status", "inventory_state", "observed_at", "last_full_sync"}
	if addresses {
		columns = append(columns, "public_addresses")
	}
	if snapshots {
		columns = append(columns, "source_volume_id", "snapshot_class", "size_gib")
	}
	if volumes {
		columns = append(columns, "size_gib", "storage_class", "usage", "bootable")
	}
	if loadBalancers {
		columns = append(columns, "product", "address_type", "dns_name", "private_addresses")
	}
	if databases {
		columns = append(columns, "product", "engine", "engine_version")
	}
	if caches {
		columns = append(columns, "product", "engine", "engine_version", "capacity_mib")
	}
	if clusters {
		columns = append(columns, "product", "cluster_type", "kubernetes_version", "node_count")
	}
	if buckets {
		columns = append(columns, "product", "location", "storage_class", "created_at")
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
			SafeCSVCell(asset.Account), SafeCSVCell(asset.Region), string(asset.Kind), SafeCSVCell(asset.ID),
			SafeCSVCell(asset.Name), SafeCSVCell(asset.Status), asset.State,
			asset.ObservedAt.UTC().Format(time.RFC3339Nano), last,
		}
		if addresses {
			record = append(record, SafeCSVCell(strings.Join(asset.PublicAddresses, " ")))
		}
		if snapshots {
			class, size := "", ""
			if asset.Snapshot != nil {
				class = SafeCSVCell(asset.Snapshot.Class)
				if asset.Snapshot.SizeGiB != 0 {
					size = strconv.FormatInt(asset.Snapshot.SizeGiB, 10)
				}
			}
			record = append(record, SafeCSVCell(asset.ParentID), class, size)
		}
		if volumes {
			size, class, usage, bootable := "", "", "", ""
			if volume := asset.Volume; volume != nil {
				class, usage = SafeCSVCell(volume.StorageClass), volume.Usage
				if volume.SizeGiB != nil {
					size = strconv.Itoa(*volume.SizeGiB)
				}
				if volume.Bootable != nil {
					bootable = strconv.FormatBool(*volume.Bootable)
				}
			}
			record = append(record, size, class, usage, bootable)
		}
		if loadBalancers {
			product, addressType, dns, private := "", "", "", ""
			if lb := asset.LoadBalancer; lb != nil {
				product, addressType, dns = lb.Product, lb.AddressType, lb.DNSName
				private = strings.Join(lb.PrivateAddresses, " ")
			}
			record = append(record, SafeCSVCell(product), SafeCSVCell(addressType), SafeCSVCell(dns), SafeCSVCell(private))
		}
		if databases {
			product, engine, version := "", "", ""
			if db := asset.Database; db != nil {
				product, engine, version = db.Product, db.Engine, db.Version
			}
			record = append(record, SafeCSVCell(product), SafeCSVCell(engine), SafeCSVCell(version))
		}
		if caches {
			product, engine, version, capacity := "", "", "", ""
			if cache := asset.Cache; cache != nil {
				product, engine, version = cache.Product, cache.Engine, cache.Version
				if cache.CapacityMiB > 0 {
					capacity = strconv.FormatFloat(cache.CapacityMiB, 'f', -1, 64)
				}
			}
			record = append(record, SafeCSVCell(product), SafeCSVCell(engine), SafeCSVCell(version), capacity)
		}
		if clusters {
			product, clusterType, version, nodeCount := "", "", "", ""
			if cluster := asset.Cluster; cluster != nil {
				product, clusterType, version = cluster.Product, cluster.Type, cluster.Version
				if cluster.NodeCount != nil {
					nodeCount = strconv.Itoa(*cluster.NodeCount)
				}
			}
			record = append(record, SafeCSVCell(product), SafeCSVCell(clusterType), SafeCSVCell(version), nodeCount)
		}
		if buckets {
			product, location, class, created := "", "", "", ""
			if bucket := asset.Bucket; bucket != nil {
				product, location, class, created = bucket.Product, bucket.Location, bucket.StorageClass, bucket.CreatedAt.UTC().Format(time.RFC3339Nano)
			}
			record = append(record, SafeCSVCell(product), SafeCSVCell(location), SafeCSVCell(class), created)
		}
		if err := out.Write(record); err != nil {
			return err
		}
	}
	out.Flush()
	return out.Error()
}

func SafeCSVCell(value string) string {
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
