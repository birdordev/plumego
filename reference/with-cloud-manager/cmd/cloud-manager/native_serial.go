package main

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/birdordev/plumego/contract"
	plumelog "github.com/birdordev/plumego/log"
	"github.com/birdordev/plumego/router"

	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/nativeconsole"
	"with-cloud-manager/internal/provider"
)

type serialKeyInput struct {
	SSHPublicKey string `json:"ssh_public_key"`
}

type serialActivation struct {
	Grant nativeconsole.Grant    `json:"grant"`
	SSH   provider.SerialReceipt `json:"ssh"`
}

func (h nativeHTTP) activateSerial(ctx context.Context, w http.ResponseWriter, r *http.Request, token string, scope identity.Scope) {
	revision, ok := accountRevision(r)
	if !ok {
		writeNativeError(w, r, nativeconsole.ErrStale, h.logger)
		return
	}
	var input serialKeyInput
	if err := contract.DecodeUniqueJSONRequest(r, &input, contract.JSONRequestOptions{MaxBytes: 8192}); err != nil {
		writeNativeError(w, r, err, h.logger)
		return
	}
	key, keyHash, err := provider.NormalizeSerialPublicKey(input.SSHPublicKey)
	if err != nil {
		writeNativeError(w, r, nativeconsole.ErrInvalid, h.logger)
		return
	}
	id := router.Param(r, "grant")
	session, err := h.store.ClaimSerial(ctx, token, scope, id, revision, keyHash)
	if err != nil {
		writeNativeError(w, r, err, h.logger)
		return
	}
	receipt, uncertain, activationErr := h.issueSerial(ctx, token, scope, session, key)
	settle, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer cancel()
	grant, err := h.store.FinishSerial(settle, scope, id, receipt, uncertain)
	if err != nil {
		h.logger.Warn("serial console settlement failed", plumelog.Fields{})
		writeNativeError(w, r, err, h.logger)
		return
	}
	setGrantETag(w, grant)
	if activationErr != nil {
		writeNativeError(w, r, activationErr, h.logger)
		return
	}
	h.ok(w, r, serialActivation{Grant: grant, SSH: receipt}, nil)
}

func (h nativeHTTP) issueSerial(ctx context.Context, token string, scope identity.Scope, session nativeconsole.Session, key string) (provider.SerialReceipt, bool, error) {
	dispatch, err := h.cfg.nativeSerial(ctx, session, key)
	if err != nil {
		return provider.SerialReceipt{}, false, err
	}
	if dispatch == nil {
		return provider.SerialReceipt{}, false, nativeconsole.ErrUnsupported
	}
	if err := h.store.CheckSerial(ctx, token, scope, session.ID); err != nil {
		return provider.SerialReceipt{}, false, err
	}
	receipt, err := dispatch(ctx)
	if err != nil {
		var failure *provider.Failure
		uncertain := !errors.As(err, &failure) || failure.Kind == provider.OutcomeUnknown
		return provider.SerialReceipt{}, uncertain, err
	}
	if receipt.RequestID == "" || receipt.Host == "" || receipt.Username == "" || receipt.Port != 22 || receipt.ValidUntil.IsZero() {
		return provider.SerialReceipt{}, true, errors.New("invalid AWS serial console receipt")
	}
	return receipt, false, nil
}
