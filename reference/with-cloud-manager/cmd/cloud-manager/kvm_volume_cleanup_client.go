package main

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"
)

func planKVMVolumeCleanupRequest(host, placement *url.URL, args []string) (operationRequestPlan, error) {
	usage := errors.New(kvmUsage)
	if args[0] == "run-volume-cleanup" {
		if _, ok := parseKVMVolumeCleanupTarget(args); !ok {
			return operationRequestPlan{}, usage
		}
		return operationRequestPlan{target: host, method: http.MethodGet, status: http.StatusOK}, nil
	}
	if args[0] == "list-volume-cleanups" {
		if !kvmUnitName.MatchString(args[4]) || len(args) > 6 {
			return operationRequestPlan{}, usage
		}
		target := host.JoinPath("volume-cleanups")
		if len(args) == 6 {
			if !kvmSafePlacement.MatchString(args[5]) {
				return operationRequestPlan{}, usage
			}
			target.RawQuery = url.Values{"after": {args[5]}}.Encode()
		}
		return operationRequestPlan{target: target, method: http.MethodGet, status: http.StatusOK}, nil
	}
	if len(args) < 6 || !kvmSafePlacement.MatchString(args[4]) {
		return operationRequestPlan{}, usage
	}
	target := placement.JoinPath("volume-cleanups")
	if args[0] == "request-volume-cleanup" && len(args) == 6 {
		revision, err := strconv.ParseInt(args[5], 10, 64)
		if err == nil && revision > 0 {
			return operationRequestPlan{target: target, method: http.MethodPost, status: http.StatusAccepted,
				inputLimit: 1024, match: strconv.Quote(strconv.FormatInt(revision, 10))}, nil
		}
	}
	if !kvmUnitName.MatchString(args[5]) {
		return operationRequestPlan{}, usage
	}
	target = target.JoinPath(args[5])
	if args[0] == "get-volume-cleanup" && len(args) == 6 {
		return operationRequestPlan{target: target, method: http.MethodGet, status: http.StatusOK}, nil
	}
	if len(args) == 7 && (args[0] == "review-volume-cleanup" || args[0] == "resolve-volume-cleanup") {
		revision, err := strconv.ParseInt(args[6], 10, 64)
		if err == nil && revision > 0 {
			action := "review"
			if args[0] == "resolve-volume-cleanup" {
				action = "resolve"
			}
			return operationRequestPlan{target: target.JoinPath(action), method: http.MethodPost, status: http.StatusOK,
				inputLimit: 1024, match: strconv.Quote(strconv.FormatInt(revision, 10))}, nil
		}
	}
	return operationRequestPlan{}, usage
}
