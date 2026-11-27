package main

import (
	"context"
	"net/http"
	"net/url"
	"strconv"

	"github.com/birdordev/plumego/router"

	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/kvm"
)

func (h kvmHTTP) getHostFence(ctx context.Context, w http.ResponseWriter, r *http.Request, token string, scope identity.Scope, site string) error {
	result, err := h.store.GetHostFence(ctx, token, scope, site, router.Param(r, "host"))
	if err != nil {
		return err
	}
	h.write(w, r, http.StatusOK, result)
	return nil
}

func (h kvmHTTP) listHostFenceWork(ctx context.Context, w http.ResponseWriter, r *http.Request, token string, scope identity.Scope, site string) error {
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return kvm.ErrInvalid
	}
	limit := 50
	for key, values := range query {
		if len(values) != 1 || values[0] == "" || (key != "after" && key != "limit") {
			return kvm.ErrInvalid
		}
		if key == "limit" {
			limit, err = strconv.Atoi(values[0])
			if err != nil {
				return kvm.ErrInvalid
			}
		}
	}
	result, err := h.store.ListHostFenceWork(ctx, token, scope, site, router.Param(r, "host"), query.Get("after"), limit)
	if err != nil {
		return err
	}
	h.write(w, r, http.StatusOK, result)
	return nil
}

func (h kvmHTTP) changeHostFence(ctx context.Context, w http.ResponseWriter, r *http.Request, token string, scope identity.Scope, site string) error {
	revision, ok := accountRevision(r)
	if !ok {
		return kvm.ErrStale
	}
	var input kvm.HostFenceChange
	if err := decodeKVMBody(r, &input, 1024); err != nil {
		return err
	}
	host, err := h.store.ChangeHostFence(ctx, token, scope, site, router.Param(r, "host"), revision, input)
	if err != nil {
		return err
	}
	h.writeHost(w, r, http.StatusOK, host)
	return nil
}

func (h kvmHTTP) confirmEvacuationCleanup(ctx context.Context, w http.ResponseWriter, r *http.Request, token string, scope identity.Scope, site string) error {
	revision, ok := accountRevision(r)
	if !ok {
		return kvm.ErrStale
	}
	var proof kvm.EvacuationCleanup
	if err := decodeKVMBody(r, &proof, 1024); err != nil {
		return err
	}
	migration, err := h.store.ConfirmEvacuationCleanup(ctx, token, scope, site, router.Param(r, "host"), revision, proof)
	if err != nil {
		return err
	}
	h.write(w, r, http.StatusOK, migration)
	return nil
}
