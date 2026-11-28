package kvmapp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"with-cloud-manager/internal/kvm"
)

const kvmMountInfoLimit = 4 << 20

type KVMPoolXML struct {
	XMLName xml.Name `xml:"pool"`
	Type    string   `xml:"type,attr"`
	Name    string   `xml:"name"`
	Path    string   `xml:"target>path"`
	Hosts   []struct {
		Name string `xml:"name,attr"`
		Port string `xml:"port,attr"`
	} `xml:"source>host"`
	Directories []struct {
		Path string `xml:"path,attr"`
	} `xml:"source>dir"`
	Formats []struct {
		Type string `xml:"type,attr"`
	} `xml:"source>format"`
}

func loadKVMSharedStorage(ctx context.Context, api KVMVMAPI, host kvm.Host) (kvm.SharedStorageSpec, error) {
	if host.SharedStoragePool == "" {
		return kvm.SharedStorageSpec{}, nil
	}
	if !kvmUnitName.MatchString(host.SharedStoragePool) {
		return kvm.SharedStorageSpec{}, ErrKVMUnsafeTarget
	}
	target := api.Target.JoinPath("..", "..", "shared-storage-pools", host.SharedStoragePool)
	target.RawQuery = "usage=false"
	var pool kvm.SharedStorageSpec
	if err := api.request(ctx, target, http.MethodGet, nil, &pool); err != nil {
		return kvm.SharedStorageSpec{}, err
	}
	if pool.ID != host.SharedStoragePool || !pool.Valid() {
		return kvm.SharedStorageSpec{}, ErrKVMUnsafeTarget
	}
	return pool, nil
}

func (probe KVMProbe) sharedPoolPath(ctx context.Context, volume KVMPoolXML) (string, error) {
	source := probe.shared.SharedStorageSource
	if volume.Type != "netfs" || len(volume.Hosts) != 1 || len(volume.Directories) != 1 || len(volume.Formats) != 1 ||
		volume.Hosts[0].Name != source.Server || (volume.Hosts[0].Port != "" && volume.Hosts[0].Port != "2049") ||
		volume.Directories[0].Path != source.Export || volume.Formats[0].Type != "nfs" {
		return "", ErrInvalidKVMObservation
	}
	actualPath, err := filepath.EvalSymlinks(volume.Path)
	if err != nil || actualPath != volume.Path || actualPath == "/" {
		return "", ErrInvalidKVMObservation
	}
	read := probe.MountInfo
	if read == nil {
		read = readKVMMountInfo
	}
	data, err := read(ctx)
	if err != nil {
		return "", err
	}
	if err := verifyKVMMountInfo(data, actualPath, source); err != nil {
		return "", err
	}
	return actualPath, nil
}

func readKVMMountInfo(ctx context.Context) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	file, err := os.Open("/proc/self/mountinfo")
	if err != nil {
		return nil, ErrInvalidKVMObservation
	}
	data, err := io.ReadAll(io.LimitReader(file, kvmMountInfoLimit+1))
	return data, errors.Join(err, file.Close(), ctx.Err())
}

func verifyKVMMountInfo(data []byte, target string, source kvm.SharedStorageSource) error {
	if len(data) > kvmMountInfoLimit {
		return ErrInvalidKVMObservation
	}
	replace := strings.NewReplacer(`\040`, " ", `\011`, "\t", `\012`, "\n", `\134`, `\`)
	scanner := bufio.NewScanner(bytes.NewReader(data))
	matches := 0
	for scanner.Scan() {
		mount, filesystem, found := strings.Cut(scanner.Text(), " - ")
		if !found {
			return ErrInvalidKVMObservation
		}
		fields := strings.Fields(mount)
		if len(fields) < 6 {
			return ErrInvalidKVMObservation
		}
		point := replace.Replace(fields[4])
		if strings.HasPrefix(point, target+"/") {
			return ErrInvalidKVMObservation
		}
		if point != target {
			continue
		}
		if fields[3] != "/" || !slices.Contains(strings.Split(fields[5], ","), "rw") ||
			!matchesKVMNFS(strings.Fields(filesystem), source) {
			return ErrInvalidKVMObservation
		}
		matches++
	}
	if scanner.Err() != nil || matches != 1 {
		return ErrInvalidKVMObservation
	}
	return nil
}

func matchesKVMNFS(fields []string, source kvm.SharedStorageSource) bool {
	if len(fields) != 3 || (fields[0] != "nfs" && fields[0] != "nfs4") || fields[1] != source.Server+":"+source.Export {
		return false
	}
	options := strings.Split(fields[2], ",")
	for _, option := range options {
		key, value, _ := strings.Cut(option, "=")
		if key == "nolock" || (key == "local_lock" && value != "none") || (key == "port" && value != "2049" && value != "0") {
			return false
		}
	}
	return slices.Contains(options, "rw") && slices.Contains(options, "proto=tcp") &&
		slices.Contains(options, "addr="+source.Server) &&
		(slices.Contains(options, "vers=4") || slices.Contains(options, "vers=4.0") ||
			slices.Contains(options, "vers=4.1") || slices.Contains(options, "vers=4.2"))
}
