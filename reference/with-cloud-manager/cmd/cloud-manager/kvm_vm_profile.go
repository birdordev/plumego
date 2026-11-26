package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"path/filepath"
	"regexp"
	"slices"
)

var kvmSafePlacement = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:-]{0,127}$`)

type kvmAgentProfile struct {
	Volumes   []string `json:"boot_volumes"`
	ImageRoot string   `json:"image_root"`
}

func readKVMAgentProfile(input io.Reader) (kvmAgentProfile, error) {
	raw, err := readOperationInput(input, 8192)
	if err != nil {
		return kvmAgentProfile{}, err
	}
	var profile kvmAgentProfile
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&profile) != nil || decoder.Decode(new(any)) != io.EOF ||
		(len(profile.Volumes) == 0 && profile.ImageRoot == "") || len(profile.Volumes) > 128 ||
		(profile.ImageRoot != "" && (!filepath.IsAbs(profile.ImageRoot) || filepath.Clean(profile.ImageRoot) != profile.ImageRoot)) {
		return kvmAgentProfile{}, errors.New("invalid local KVM agent profile")
	}
	slices.Sort(profile.Volumes)
	for i, volume := range profile.Volumes {
		if !kvmUnitName.MatchString(volume) || (i > 0 && profile.Volumes[i-1] == volume) {
			return kvmAgentProfile{}, errors.New("invalid local KVM boot volume allowlist")
		}
	}
	return profile, nil
}
