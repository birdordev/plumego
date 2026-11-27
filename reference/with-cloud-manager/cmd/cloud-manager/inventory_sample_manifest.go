package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"

	"with-cloud-manager/internal/inventory"
)

const inventoryScopeManifestLimit = 1 << 20

func (options *inventorySampleConfig) readManifest(input io.Reader, scope inventorySampleScope) error {
	raw, err := readOperationInput(input, inventoryScopeManifestLimit)
	if err != nil {
		return err
	}
	var manifest struct {
		Tenant  string                        `json:"tenant"`
		Project string                        `json:"project"`
		Scopes  []inventory.FreshnessScopeKey `json:"scopes"`
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil || len(manifest.Scopes) == 0 {
		return errors.New("invalid inventory scope manifest")
	}
	if manifest.Tenant != scope.Tenant || manifest.Project != scope.Project {
		return errors.New("inventory scope manifest does not match the requested project")
	}
	return options.setScopes(manifest.Scopes, scope)
}

func (options *inventorySampleConfig) setScopes(scopes []inventory.FreshnessScopeKey, scope inventorySampleScope) error {
	total := int64(len(scopes))
	if options.expected != 0 && options.expected != total {
		return errors.New("inventory scope manifest does not match the expected scope count")
	}
	for _, key := range scopes {
		if (scope.Provider != "" && key.Provider != scope.Provider) || (scope.Account != "" && key.Account != scope.Account) {
			return errors.New("inventory scope manifest includes a scope outside the requested filters")
		}
	}
	fingerprint, err := inventory.FreshnessScopeFingerprint(scopes)
	if err != nil {
		return errors.New("invalid or duplicate inventory scope in manifest")
	}
	options.expected, options.fingerprint = total, fingerprint
	options.query.Set("scope_fingerprint", "true")
	return nil
}
