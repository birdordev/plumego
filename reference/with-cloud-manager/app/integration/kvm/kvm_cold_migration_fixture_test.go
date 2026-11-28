//go:build linux || darwin

package kvmtest

import (
	"bytes"
	"context"
	"fmt"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	kvmapp "with-cloud-manager/app/kvm"
	"with-cloud-manager/internal/kvm"
)

func migrationAgentFixture(t *testing.T, server *httptest.Server, destination string, vm kvm.VM, contents []byte) (*fakeKVMDaemon, kvmapp.KVMVMRunner, kvmapp.KVMVMAPI, string) {
	t.Helper()
	source := imageFixtureDir(t)
	if err := os.WriteFile(filepath.Join(source, vm.ImageSHA256+".raw"), contents, 0600); err != nil {
		t.Fatal(err)
	}
	return recoveryAgentFixture(t, server, destination, fixtureMigrationUUID, fixtureKVMReplacement, source, vm)
}

func recoveryAgentFixture(t *testing.T, server *httptest.Server, destination, uuid, token, source string, vm kvm.VM) (*fakeKVMDaemon, kvmapp.KVMVMRunner, kvmapp.KVMVMAPI, string) {
	t.Helper()
	pool := imageFixtureDir(t)
	target := filepath.Join(pool, vm.Volume)
	network := `<network><name>app</name><forward mode="nat"/><ip address="10.77.0.1" prefix="29"><dhcp>
		<host ip="` + vm.Address + `" mac="` + vm.MAC + `" name="fixture-vm"/>
	</dhcp></ip></network>`
	daemon := &fakeKVMDaemon{expectedVolume: vm.Volume, liveNetwork: network, savedNetwork: network}
	runner := daemon.runner(t)
	read, write := runner.Read, runner.Write
	runner.Read = func(ctx context.Context, args ...string) ([]byte, error) {
		switch strings.Join(args, " ") {
		case "capabilities":
			data, err := read(ctx, args...)
			return bytes.ReplaceAll(data, []byte(fixtureKVMUUID), []byte(uuid)), err
		case "pool-dumpxml --pool local":
			return []byte(`<pool type="dir"><name>local</name><target><path>` + pool + `</path></target></pool>`), nil
		case "vol-dumpxml --pool local --vol " + vm.Volume:
			info, err := os.Stat(target)
			if err != nil {
				return nil, err
			}
			return []byte(fmt.Sprintf(`<volume><name>%s</name><capacity unit="bytes">%d</capacity><target><path>%s</path><format type="raw"/></target></volume>`,
				vm.Volume, info.Size(), target)), nil
		default:
			return read(ctx, args...)
		}
	}
	runner.Write = func(ctx context.Context, args ...string) ([]byte, error) {
		if strings.Join(args, " ") == "pool-refresh local" {
			return nil, nil
		}
		return write(ctx, args...)
	}
	runner.FreeMiB = func(path string) (int64, error) {
		if path != pool {
			return 0, kvmapp.ErrKVMUnsafeTarget
		}
		return 64_000, nil
	}
	runner.BootFile = func(path string, size int64) error {
		if path != target || size != vm.ImageSizeBytes {
			return kvmapp.ErrKVMUnsafeTarget
		}
		return nil
	}
	base, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	api := kvmapp.KVMVMAPI{Target: base.JoinPath(destination), Token: token, Transport: server.Client().Transport}
	return daemon, runner, api, fmt.Sprintf(`{"image_root":%q}`, source)
}
