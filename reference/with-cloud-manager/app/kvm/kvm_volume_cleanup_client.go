package kvmapp

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"

	"with-cloud-manager/app/clients/projecthttp"
)

func planKVMVolumeCleanupRequest(host, placement *url.URL, args []string) (projecthttp.Request, error) {
	usage := errors.New(kvmUsage)
	if args[0] == "run-volume-cleanup" {
		if _, ok := ParseKVMVolumeCleanupTarget(args); !ok {
			return projecthttp.Request{}, usage
		}
		return projecthttp.Request{Target: host, Method: http.MethodGet, Status: http.StatusOK}, nil
	}
	if args[0] == "list-volume-cleanups" {
		if !kvmUnitName.MatchString(args[4]) || len(args) > 6 {
			return projecthttp.Request{}, usage
		}
		target := host.JoinPath("volume-cleanups")
		if len(args) == 6 {
			if !kvmSafePlacement.MatchString(args[5]) {
				return projecthttp.Request{}, usage
			}
			target.RawQuery = url.Values{"after": {args[5]}}.Encode()
		}
		return projecthttp.Request{Target: target, Method: http.MethodGet, Status: http.StatusOK}, nil
	}
	if len(args) < 6 || !kvmSafePlacement.MatchString(args[4]) {
		return projecthttp.Request{}, usage
	}
	target := placement.JoinPath("volume-cleanups")
	if args[0] == "request-volume-cleanup" && len(args) == 6 {
		revision, err := strconv.ParseInt(args[5], 10, 64)
		if err == nil && revision > 0 {
			return projecthttp.Request{Target: target, Method: http.MethodPost, Status: http.StatusAccepted,
				InputLimit: 1024, Match: strconv.Quote(strconv.FormatInt(revision, 10))}, nil
		}
	}
	if !kvmUnitName.MatchString(args[5]) {
		return projecthttp.Request{}, usage
	}
	target = target.JoinPath(args[5])
	if args[0] == "get-volume-cleanup" && len(args) == 6 {
		return projecthttp.Request{Target: target, Method: http.MethodGet, Status: http.StatusOK}, nil
	}
	if len(args) == 7 && (args[0] == "review-volume-cleanup" || args[0] == "resolve-volume-cleanup") {
		revision, err := strconv.ParseInt(args[6], 10, 64)
		if err == nil && revision > 0 {
			action := "review"
			if args[0] == "resolve-volume-cleanup" {
				action = "resolve"
			}
			return projecthttp.Request{Target: target.JoinPath(action), Method: http.MethodPost, Status: http.StatusOK,
				InputLimit: 1024, Match: strconv.Quote(strconv.FormatInt(revision, 10))}, nil
		}
	}
	return projecthttp.Request{}, usage
}
