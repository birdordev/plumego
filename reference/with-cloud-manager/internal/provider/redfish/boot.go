package redfish

import (
	"bytes"
	"context"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"

	"with-cloud-manager/internal/provider"
)

type bootOverride struct {
	Enabled string   `json:"BootSourceOverrideEnabled"`
	Target  string   `json:"BootSourceOverrideTarget"`
	Targets []string `json:"BootSourceOverrideTarget@Redfish.AllowableValues"`
}

var strongETag = regexp.MustCompile(`^"[!#-~]{1,256}"$`)

func (b bootOverride) oncePXE() bool { return b.Enabled == "Once" && b.Target == "Pxe" }

func (c PowerOperator) PrepareBareMetalPXE(ctx context.Context, scope provider.Scope, id, action string) (func(context.Context) (string, error), error) {
	if !c.config.AllowPXE {
		return nil, &provider.Failure{Kind: provider.Unsupported}
	}
	if action != provider.StageBareMetalPXE && action != provider.CancelBareMetalPXE {
		return nil, &provider.Failure{Kind: provider.Unsupported}
	}
	resource, machine, err := c.locate(ctx, scope, id)
	if err != nil {
		return nil, err
	}
	_, _, err = pxeChange(machine, action)
	if err != nil {
		return nil, err
	}
	return func(ctx context.Context) (string, error) {
		latest, err := c.readSystem(ctx, resource)
		if err != nil {
			return "", err
		}
		if latest.Type != "Physical" || !strings.EqualFold(latest.UUID, id) {
			return "", pxeConflict()
		}
		payload, needed, err := pxeChange(latest, action)
		if err != nil || !needed {
			return "", err
		}
		return "", c.patchBoot(ctx, resource, latest.etag, payload)
	}, nil
}

func pxeChange(machine system, action string) ([]byte, bool, error) {
	if machine.status() != "off" {
		return nil, false, pxeConflict()
	}
	boot := machine.Boot
	if action == provider.StageBareMetalPXE {
		if boot.oncePXE() {
			return nil, false, nil
		}
		if boot.Enabled != "Disabled" || !slices.Contains(boot.Targets, "Pxe") || !strongETag.MatchString(machine.etag) {
			return nil, false, pxeConflict()
		}
		return []byte(`{"Boot":{"BootSourceOverrideEnabled":"Once","BootSourceOverrideTarget":"Pxe"}}`), true, nil
	}
	if boot.Enabled == "Disabled" {
		return nil, false, nil
	}
	if !boot.oncePXE() || !strongETag.MatchString(machine.etag) {
		return nil, false, pxeConflict()
	}
	return []byte(`{"Boot":{"BootSourceOverrideEnabled":"Disabled"}}`), true, nil
}

func pxeConflict() error { return &provider.Failure{Kind: provider.PreconditionFailed} }

func (c PowerOperator) ObserveBareMetalPXE(ctx context.Context, scope provider.Scope, id, action string) (bool, error) {
	if action != provider.StageBareMetalPXE && action != provider.CancelBareMetalPXE {
		return false, &provider.Failure{Kind: provider.Unsupported}
	}
	_, machine, err := c.locate(ctx, scope, id)
	if err != nil || machine.status() != "off" {
		return false, err
	}
	if action == provider.StageBareMetalPXE {
		return machine.Boot.oncePXE(), nil
	}
	return machine.Boot.Enabled == "Disabled", nil
}

func (c PowerOperator) patchBoot(ctx context.Context, target *url.URL, etag string, payload []byte) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodPatch, c.base.ResolveReference(target).String(), bytes.NewReader(payload))
	if err != nil {
		return err
	}
	request.GetBody = nil
	request.SetBasicAuth(c.config.User, c.config.Password)
	request.Header.Set("If-Match", etag)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("OData-Version", "4.0")
	response, err := c.http.Do(request)
	if err != nil {
		return &provider.Failure{Kind: provider.OutcomeUnknown}
	}
	defer func() { _ = response.Body.Close() }()
	switch response.StatusCode {
	case http.StatusOK, http.StatusAccepted, http.StatusNoContent:
		return nil
	default:
		return &provider.Failure{Kind: provider.OutcomeUnknown}
	}
}
