package billingservice

import (
	"strconv"
)

func ParseRevision(value string, zero bool) (int64, bool) {
	revision, err := strconv.ParseInt(value, 10, 64)
	return revision, err == nil && (revision > 0 || zero && revision == 0) && strconv.FormatInt(revision, 10) == value
}
