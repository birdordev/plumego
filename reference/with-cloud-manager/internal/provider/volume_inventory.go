package provider

import (
	"math"
	"regexp"
)

type VolumeDetails struct {
	SizeGiB      *int   `json:"size_gib,omitempty"`
	StorageClass string `json:"storage_class,omitempty"`
	Usage        string `json:"usage,omitempty"`
	Bootable     *bool  `json:"bootable,omitempty"`
}

var volumeClass = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,95}$`)

func (d VolumeDetails) Valid() bool {
	if d.SizeGiB == nil && d.StorageClass == "" && d.Usage == "" && d.Bootable == nil {
		return false
	}
	return (d.SizeGiB == nil || (*d.SizeGiB > 0 && *d.SizeGiB <= math.MaxInt32)) &&
		(d.StorageClass == "" || volumeClass.MatchString(d.StorageClass)) &&
		(d.Usage == "" || d.Usage == "system" || d.Usage == "data")
}
