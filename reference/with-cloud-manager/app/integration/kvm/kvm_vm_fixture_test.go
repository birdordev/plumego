package kvmtest

import (
	"context"
	"encoding/xml"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"

	kvmapp "with-cloud-manager/app/kvm"
)

type fakeKVMDaemon struct {
	mu             sync.Mutex
	spec           []byte
	liveNetwork    string
	savedNetwork   string
	id             string
	expectedVolume string
	running        bool
	transient      bool
	snapshot       bool
	checkpoint     bool
	managedSave    bool
	autoStart      bool
	dropProbe      bool
	writes         []string
}

func (f *fakeKVMDaemon) runner(t *testing.T) kvmapp.KVMVMRunner {
	t.Helper()
	probe := fixtureKVMProbe(t, fixtureKVMUUID, "", 64_000)
	original := probe.Read
	probe.Read = func(ctx context.Context, args ...string) ([]byte, error) {
		f.mu.Lock()
		defer f.mu.Unlock()
		switch strings.Join(args, " ") {
		case "list --all --uuid", "list --all --uuid --persistent":
			if f.transient && len(args) == 4 {
				return nil, nil
			}
			if f.dropProbe {
				f.dropProbe = false
				return nil, errors.New("fixture dropped libvirt read after write")
			}
			return []byte(f.id), nil
		case "net-dumpxml --network app":
			return []byte(f.liveNetwork), nil
		case "net-dumpxml --network app --inactive":
			return []byte(f.savedNetwork), nil
		case "vol-dumpxml --pool local --vol boot-ready":
			return []byte(`<volume type="file"><name>boot-ready</name><capacity unit="bytes">805306368</capacity>
				<target><path>/srv/kvm/boot-ready</path><format type="raw"/></target></volume>`), nil
		case "dumpxml " + f.id + " --inactive", "dumpxml " + f.id:
			if f.id != "" {
				return f.spec, nil
			}
		case "dominfo " + f.id:
			state, save, auto := "shut off", "no", "disable"
			if f.running {
				state = "running"
			}
			if f.managedSave {
				save = "yes"
			}
			if f.autoStart {
				auto = "enable"
			}
			return []byte("UUID: " + f.id + "\nState: " + state + "\nPersistent: yes\nManaged save: " + save + "\nAutostart: " + auto + "\n"), nil
		case "snapshot-list " + f.id + " --name":
			if f.snapshot {
				return []byte("before-maintenance\n"), nil
			}
			return nil, nil
		case "checkpoint-list " + f.id + " --name":
			if f.checkpoint {
				return []byte("backup-checkpoint\n"), nil
			}
			return nil, nil
		case "domstate " + f.id:
			if f.running {
				return []byte("running\n"), nil
			}
			return []byte("shut off\n"), nil
		}
		return original(ctx, args...)
	}
	write := func(_ context.Context, args ...string) ([]byte, error) {
		f.mu.Lock()
		defer f.mu.Unlock()
		switch {
		case len(args) == 3 && args[0] == "define" && args[2] == "--validate":
			if f.id != "" {
				t.Fatal("persistent VM was defined twice")
			}
			spec, err := os.ReadFile(args[1])
			if err != nil {
				t.Fatal(err)
			}
			var domain kvmapp.KVMDomain
			expectedVolume := f.expectedVolume
			if expectedVolume == "" {
				expectedVolume = "boot-ready"
			}
			if err := xml.Unmarshal(spec, &domain); err != nil || domain.Metadata.Owner == nil || domain.Type != "kvm" ||
				domain.Features == nil || domain.Features.ACPI == nil ||
				len(domain.Networks) != 1 || domain.Networks[0].Source.Network != "app" ||
				len(domain.Disks) != 1 || domain.Disks[0].Source.Volume != expectedVolume || domain.Disks[0].Driver.Format != "raw" {
				t.Fatalf("incorrect generated libvirt specification: %s, %v", spec, err)
			}
			f.id, f.spec = domain.UUID, spec
		case len(args) == 2 && args[0] == "start" && args[1] == f.id && len(f.spec) > 0:
			if f.running {
				t.Fatal("VM was started twice")
			}
			f.running = true
		case len(args) == 4 && args[0] == "shutdown" && args[1] == f.id && args[2] == "--mode" && args[3] == "acpi":
			if !f.running || f.transient {
				t.Errorf("shutdown issued for inactive or transient domain")
				return nil, errors.New("fixture rejected shutdown")
			}
			// 访客异步关机，由测试提供下一次观测。
		case len(args) == 2 && args[0] == "undefine" && f.id != "" && args[1] == f.id:
			if f.running || f.transient || f.snapshot || f.checkpoint || f.managedSave || f.autoStart {
				t.Errorf("undefine issued despite current domain protection")
				return nil, errors.New("fixture rejected undefine")
			}
			f.id = ""
		default:
			t.Fatalf("unexpected libvirt command: %v", args)
		}
		f.writes = append(f.writes, args[0])
		f.dropProbe = true
		return nil, errors.New("fixture lost libvirt write reply")
	}
	return kvmapp.KVMVMRunner{KVMProbe: probe, Write: write, EnableACPI: true, BootFile: func(path string, size int64) error {
		if path != "/srv/kvm/boot-ready" || size != 805306368 {
			t.Fatalf("unexpected boot file validation: %q %d", path, size)
		}
		return nil
	}}
}
