package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"unicode/utf8"

	"with-cloud-manager/internal/billing"
)

const allocationStorageUsage = "usage: cloud-manager billing-allocate TENANT PROJECT ACCOUNT PERIOD SOURCE REPORT_REVISION EXPECTED_ALLOCATION_REVISION POLICY.json | billing-allocation-replay TENANT PROJECT ACCOUNT PERIOD SOURCE REPORT_REVISION ALLOCATION_REVISION"

func runAllocationStorageCommand(ctx context.Context, store billing.Store, key billing.Key,
	report int64, args []string, output io.Writer) error {
	usage := errors.New(allocationStorageUsage)
	if report < 1 || (args[0] == "billing-allocate" && len(args) != 9) || (args[0] == "billing-allocation-replay" && len(args) != 8) {
		return usage
	}
	expected, ok := billingRevision(args[7], args[0] == "billing-allocate")
	if !ok {
		return usage
	}
	var value any
	var err error
	if args[0] == "billing-allocate" {
		var policy billing.AllocationPolicy
		policy, err = readAllocationPolicy(args[8])
		if err == nil {
			value, err = store.BuildAllocation(ctx, key, report, expected, policy)
		}
	} else {
		value, err = store.VerifyAllocation(ctx, key, report, expected)
	}
	if err != nil {
		return billingStorageError(err)
	}
	return json.NewEncoder(output).Encode(value)
}

func readAllocationPolicy(path string) (billing.AllocationPolicy, error) {
	data, err := readBillingFile(path, billing.MaxAllocationPolicyBytes)
	if err != nil {
		return billing.AllocationPolicy{}, err
	}
	return decodeAllocationPolicy(data)
}

func decodeAllocationPolicy(data []byte) (billing.AllocationPolicy, error) {
	if err := checkPolicyJSON(data); err != nil {
		return billing.AllocationPolicy{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var policy billing.AllocationPolicy
	if err := decoder.Decode(&policy); err != nil || decoder.Decode(new(any)) != io.EOF {
		return billing.AllocationPolicy{}, billing.ErrInvalid
	}
	return policy, nil
}

func checkPolicyJSON(data []byte) error {
	if !utf8.Valid(data) {
		return billing.ErrInvalid
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	root, err := decoder.Token()
	if err != nil || root != json.Delim('{') || !uniquePolicyJSON(decoder, '{', 1, false) {
		return billing.ErrInvalid
	}
	if _, err := decoder.Token(); err != io.EOF {
		return billing.ErrInvalid
	}
	return nil
}

func uniquePolicyJSON(decoder *json.Decoder, open json.Delim, depth int, tags bool) bool {
	if depth > 6 {
		return false
	}
	keys := make(map[string]bool)
	for decoder.More() {
		var nextTags bool
		if open == '{' {
			token, err := decoder.Token()
			key, ok := token.(string)
			if err != nil || !ok {
				return false
			}
			if !tags {
				key = strings.ToLower(key)
				nextTags = key == "tags"
			}
			if keys[key] {
				return false
			}
			keys[key] = true
		}
		token, err := decoder.Token()
		if err != nil {
			return false
		}
		if child, ok := token.(json.Delim); ok && !uniquePolicyJSON(decoder, child, depth+1, nextTags) {
			return false
		}
	}
	closeToken, err := decoder.Token()
	return err == nil && ((open == '{' && closeToken == json.Delim('}')) || (open == '[' && closeToken == json.Delim(']')))
}
