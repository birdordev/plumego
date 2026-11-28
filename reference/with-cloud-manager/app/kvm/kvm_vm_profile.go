package kvmapp

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"path/filepath"
	"regexp"
	"slices"

	"with-cloud-manager/app/clients/projecthttp"
)

var kvmSafePlacement = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:-]{0,127}$`)

type KVMAgentProfile struct {
	Volumes   []string `json:"boot_volumes"`
	ImageRoot string   `json:"image_root"`
}

func readKVMAgentProfile(input io.Reader) (KVMAgentProfile, error) {
	raw, err := projecthttp.ReadInput(input, 8192)
	if err != nil {
		return KVMAgentProfile{}, err
	}
	var profile KVMAgentProfile
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&profile) != nil || decoder.Decode(new(any)) != io.EOF ||
		(len(profile.Volumes) == 0 && profile.ImageRoot == "") || len(profile.Volumes) > 128 ||
		(profile.ImageRoot != "" && (!filepath.IsAbs(profile.ImageRoot) || filepath.Clean(profile.ImageRoot) != profile.ImageRoot)) {
		return KVMAgentProfile{}, errors.New("invalid local KVM agent profile")
	}
	slices.Sort(profile.Volumes)
	for i, volume := range profile.Volumes {
		if !kvmUnitName.MatchString(volume) || (i > 0 && profile.Volumes[i-1] == volume) {
			return KVMAgentProfile{}, errors.New("invalid local KVM boot volume allowlist")
		}
	}
	return profile, nil
}
