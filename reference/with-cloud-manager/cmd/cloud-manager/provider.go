package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"time"

	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/inventory"
	"with-cloud-manager/internal/provider"
	"with-cloud-manager/internal/provider/awsec2"
	"with-cloud-manager/internal/provider/openstack"
	"with-cloud-manager/internal/provider/publiccloud"
)

type openStackProfile struct {
	Ref string `json:"credential_ref"`
	openstack.Config
}

func loadPrivateProfile(path string, profile any) error {
	f, err := os.Open(path)
	if err != nil {
		return errors.New("cannot open profile")
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	const maxProfile = 32 << 10
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > maxProfile {
		return errors.New("profile must be a private regular file within the size limit")
	}
	reader := &io.LimitedReader{R: f, N: maxProfile + 1}
	decoder := json.NewDecoder(reader)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(profile); err != nil {
		return errors.New("invalid profile")
	}
	if err := decoder.Decode(new(json.RawMessage)); err != io.EOF || reader.N == 0 {
		return errors.New("invalid profile")
	}
	return nil
}

type awsProfile struct {
	Ref string `json:"credential_ref"`
	awsec2.Config
}

type publicCloudProfile struct {
	Ref string `json:"credential_ref"`
	publiccloud.Config
}

type inventoryReader interface {
	provider.Discovery
	Identity() string
}

type connectReader func(context.Context) (inventoryReader, error)

func runOpenStack(parent context.Context, db *sql.DB, args []string, path string, httpClient *http.Client) error {
	var profile openStackProfile
	if err := loadPrivateProfile(path, &profile); err != nil {
		return err
	}
	return runInventoryCommand(parent, db, args, openstack.Name, profile.Ref,
		func(ctx context.Context) (inventoryReader, error) {
			return openstack.Connect(ctx, profile.Config, httpClient)
		})
}

func runAWS(parent context.Context, db *sql.DB, args []string, path string) error {
	var profile awsProfile
	if err := loadPrivateProfile(path, &profile); err != nil {
		return err
	}
	return runInventoryCommand(parent, db, args, awsec2.Name, profile.Ref,
		func(ctx context.Context) (inventoryReader, error) { return awsec2.Connect(ctx, profile.Config) })
}

func runPublicCloud(parent context.Context, db *sql.DB, args []string, name, path string, transport http.RoundTripper) error {
	var profile publicCloudProfile
	if err := loadPrivateProfile(path, &profile); err != nil {
		return err
	}
	return runInventoryCommand(parent, db, args, name, profile.Ref,
		func(ctx context.Context) (inventoryReader, error) {
			return publiccloud.Connect(ctx, name, profile.Config, transport)
		})
}

func publicCloudCommand(command string) (name, variable string) {
	switch command {
	case "verify-tencent", "scan-tencent":
		return provider.TencentCVM, "CLOUD_MANAGER_TENCENT_PROFILE"
	case "verify-alibaba", "scan-alibaba":
		return provider.AlibabaECS, "CLOUD_MANAGER_ALIBABA_PROFILE"
	case "verify-huawei", "scan-huawei":
		return provider.HuaweiECS, "CLOUD_MANAGER_HUAWEI_PROFILE"
	default:
		return "", ""
	}
}

func runInventoryCommand(parent context.Context, db *sql.DB, args []string, providerName, reference string, connect connectReader) error {
	verify := len(args) == 3
	var kind provider.Kind
	var region string
	if !verify {
		kind, region = provider.Kind(args[4]), args[3]
		if region == "-" {
			region = ""
		}
		if !kind.Valid() || (kind == provider.Region) != (region == "") {
			return errors.New("invalid provider resource scope")
		}
	}
	ctx, cancel := context.WithTimeout(parent, 9*time.Minute)
	defer cancel()
	tenant, account := args[1], args[2]
	admin := identity.NewStore(db)
	binding, err := admin.CloudAccountBinding(ctx, tenant, account)
	if err != nil || reference == "" || binding.Provider != providerName || binding.CredentialRef != reference {
		return errors.New("provider account or credential binding unavailable")
	}
	if !verify && (binding.RemoteIdentity == "" || binding.Mode == "pending") {
		return errors.New("provider account requires verification")
	}
	client, err := connect(ctx)
	if err != nil {
		return err
	}
	if binding.RemoteIdentity != "" && binding.RemoteIdentity != client.Identity() {
		return errors.New("provider account identity changed")
	}
	if verify {
		if err := admin.VerifyCloudAccount(ctx, tenant, account, binding, client.Identity()); err != nil {
			return errors.New("cannot verify provider account")
		}
		return nil
	}
	store := inventory.NewStore(db)
	claim, err := store.ClaimWithCredentialRevision(ctx, inventory.Key{
		Tenant: tenant, Account: account, Region: region, Kind: kind,
	}, 10*time.Minute, binding.Revision)
	if err != nil {
		return inventory.ErrUnavailable
	}
	for {
		next, done, err := store.ScanPage(ctx, claim, client)
		if err != nil {
			cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
			releaseErr := store.Yield(cleanup, claim)
			cancel()
			var failure *provider.Failure
			if !errors.As(err, &failure) {
				err = errors.New("provider inventory checkpoint failed")
			}
			if releaseErr != nil {
				err = errors.Join(err, errors.New("inventory lease could not be yielded"))
			}
			return err
		}
		if done {
			return nil
		}
		claim = next
	}
}
