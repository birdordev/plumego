package kvm

import (
	"errors"
	"regexp"
	"slices"
	"time"
)

var (
	ErrInvalid    = errors.New("invalid KVM site parameters")
	ErrConflict   = errors.New("KVM request conflicts with current state")
	ErrStale      = errors.New("KVM revision or report generation is stale")
	ErrNoCapacity = errors.New("no matching KVM host has capacity")
)

var identifier = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$`)
var hostUUID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

const reportFreshnessSeconds = 90

type Capacity struct {
	VCPU      int64 `json:"vcpu"`
	MemoryMiB int64 `json:"memory_mib"`
	DiskMiB   int64 `json:"disk_mib"`
	GPU       int64 `json:"gpu"`
}

func (c Capacity) valid() bool {
	return c.VCPU >= 0 && c.VCPU <= 4096 && c.MemoryMiB >= 0 && c.MemoryMiB <= 1<<24 &&
		c.DiskMiB >= 0 && c.DiskMiB <= 1<<40 && c.GPU >= 0 && c.GPU <= 128
}

func (c Capacity) fits(limit, used Capacity) bool {
	return c.VCPU <= limit.VCPU-used.VCPU && c.MemoryMiB <= limit.MemoryMiB-used.MemoryMiB &&
		c.DiskMiB <= limit.DiskMiB-used.DiskMiB && c.GPU <= limit.GPU-used.GPU
}

type HostSpec struct {
	ID            string   `json:"id"`
	UUID          string   `json:"uuid"`
	Agent         string   `json:"agent"`
	FailureDomain string   `json:"failure_domain"`
	Networks      []string `json:"networks"`
	StoragePool   string   `json:"storage_pool"`
	GPUModel      string   `json:"gpu_model,omitempty"`
}

func (h HostSpec) valid() bool {
	if !identifier.MatchString(h.ID) || !hostUUID.MatchString(h.UUID) || h.UUID == "00000000-0000-0000-0000-000000000000" ||
		!identifier.MatchString(h.Agent) || !identifier.MatchString(h.FailureDomain) ||
		!identifier.MatchString(h.StoragePool) || !optionalID(h.GPUModel) ||
		len(h.Networks) == 0 || len(h.Networks) > 32 {
		return false
	}
	networks := slices.Clone(h.Networks)
	slices.Sort(networks)
	for i, network := range networks {
		if !identifier.MatchString(network) || (i > 0 && network == networks[i-1]) {
			return false
		}
	}
	return true
}

type Host struct {
	HostSpec
	Site       string     `json:"site"`
	Revision   int64      `json:"revision"`
	Generation int64      `json:"generation"`
	Sequence   int64      `json:"sequence"`
	Draining   bool       `json:"draining"`
	ReportedAt *time.Time `json:"reported_at,omitempty"`
	Capacity   Capacity   `json:"capacity"`
}

type HostChange struct {
	Action string `json:"action"`
	Agent  string `json:"agent,omitempty"`
}

type Report struct {
	UUID       string             `json:"uuid"`
	Generation int64              `json:"generation"`
	Sequence   int64              `json:"sequence"`
	Capacity   Capacity           `json:"capacity"`
	Inventory  *CapacityInventory `json:"inventory,omitempty"`
}

var ErrNoIPv4 = errors.New("no matching KVM IPv4 address")

type PlacementInput struct {
	Capacity
	Network               string `json:"network"`
	StoragePool           string `json:"storage_pool"`
	GPUModel              string `json:"gpu_model,omitempty"`
	RequiredHost          string `json:"required_host,omitempty"`
	FailureDomain         string `json:"failure_domain,omitempty"`
	ExcludedFailureDomain string `json:"excluded_failure_domain,omitempty"`
	TTLSeconds            int    `json:"ttl_seconds"`
	IPv4                  string `json:"ipv4,omitempty"`
}

func (p PlacementInput) valid() bool {
	return p.Capacity.valid() && p.VCPU > 0 && p.MemoryMiB > 0 && p.DiskMiB > 0 &&
		identifier.MatchString(p.Network) && identifier.MatchString(p.StoragePool) &&
		optionalID(p.GPUModel) && ((p.GPU == 0) == (p.GPUModel == "")) &&
		optionalID(p.RequiredHost) && optionalID(p.FailureDomain) && optionalID(p.ExcludedFailureDomain) &&
		(p.FailureDomain == "" || p.FailureDomain != p.ExcludedFailureDomain) && p.TTLSeconds >= 1 && p.TTLSeconds <= 900 && (p.IPv4 == "" || canonicalIPv4(p.IPv4).IsValid())
}

func optionalID(id string) bool { return id == "" || identifier.MatchString(id) }

type Placement struct {
	IPv4Assignment
	ID                 string    `json:"id"`
	Site               string    `json:"site"`
	Host               string    `json:"host"`
	Generation         int64     `json:"generation"`
	Network            string    `json:"network"`
	StoragePool        string    `json:"storage_pool"`
	GPUModel           string    `json:"gpu_model,omitempty"`
	Capacity           Capacity  `json:"capacity"`
	State              string    `json:"state"`
	ExpiresAt          time.Time `json:"expires_at"`
	BootVolumeRetained bool      `json:"boot_volume_retained,omitempty"`
}
