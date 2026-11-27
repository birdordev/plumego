package main

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/birdordev/plumego/contract"
	plumelog "github.com/birdordev/plumego/log"

	"with-cloud-manager/internal/kvm"
)

func decodeKVMBody(r *http.Request, destination any, limit int64) error {
	return contract.DecodeUniqueJSONRequest(r, destination, contract.JSONRequestOptions{MaxBytes: limit})
}

func (h kvmHTTP) writeHost(w http.ResponseWriter, r *http.Request, status int, host kvm.Host) {
	w.Header().Set("ETag", strconv.Quote(strconv.FormatInt(host.Revision, 10)))
	h.write(w, r, status, host)
}

func (h kvmHTTP) write(w http.ResponseWriter, r *http.Request, status int, data any) {
	var err error
	switch status {
	case http.StatusCreated:
		err = contract.WriteResponse(w, r, http.StatusCreated, data, nil)
	case http.StatusAccepted:
		err = contract.WriteResponse(w, r, http.StatusAccepted, data, nil)
	default:
		err = contract.WriteResponse(w, r, http.StatusOK, data, nil)
	}
	if err != nil {
		h.logger.Warn("KVM response write failed", plumelog.Fields{})
	}
}

func (h kvmHTTP) writeVM(w http.ResponseWriter, r *http.Request, status int, vm kvm.VM) {
	w.Header().Set("ETag", strconv.Quote(strconv.FormatInt(vm.Revision, 10)))
	h.write(w, r, status, vm)
}

func writeKVMError(w http.ResponseWriter, r *http.Request, err error, logger plumelog.StructuredLogger) {
	switch {
	case errors.Is(err, kvm.ErrInvalid):
		err = contract.NewErrorBuilder().Type(contract.TypeBadRequest).Message("invalid KVM site parameters").Build()
	case errors.Is(err, kvm.ErrStale):
		err = contract.NewErrorBuilder().Type(contract.TypePreconditionFailed).Message("current KVM revision, generation or sequence required").Build()
	case errors.Is(err, kvm.ErrConflict):
		err = contract.NewErrorBuilder().Type(contract.TypeConflict).Message("KVM request conflicts with current state").Build()
	case errors.Is(err, kvm.ErrNoIPv4):
		err = contract.NewErrorBuilder().Type(contract.TypeConflict).Message("no matching KVM IPv4 address is available").Build()
	case errors.Is(err, kvm.ErrNoCapacity):
		err = contract.NewErrorBuilder().Type(contract.TypeConflict).Message("no matching KVM host has capacity").Build()
	case errors.Is(err, kvm.ErrNoVerifiedTarget):
		err = contract.NewErrorBuilder().Type(contract.TypeConflict).Message("no eligible KVM target with a fresh verified backup").Build()
	}
	writeAccountError(w, r, err, logger)
}
