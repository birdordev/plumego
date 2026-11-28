package kvmapp

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"runtime"
	"strconv"

	"with-cloud-manager/app/clients/projecthttp"
	"with-cloud-manager/app/models"
)

const kvmUsage = `usage: cloud-manager kvm COMMAND TENANT PROJECT SITE
  register-host < host.json
  register-network < ipv4-pool.json
  get-network NETWORK
  register-image < image.json
  register-shared-storage < storage.json
  get-shared-storage POOL
  get-image IMAGE
  mirror-image IMAGE < mirror.json
  mirror-host-image HOST GENERATION IMAGE < mirror.json
  get-host-image HOST IMAGE
  report-host-image HOST IMAGE < report.json
  plan-network-dhcp HOST GENERATION NETWORK NETWORK_UUID
  apply-network-dhcp HOST GENERATION NETWORK NETWORK_UUID
  get-host HOST
  change-host HOST REVISION < change.json
  get-host-fence HOST
  list-host-fence-work HOST [AFTER]
  confirm-evacuation-cleanup HOST REVISION < cleanup.json
  request-evacuation-batch HOST REVISION < batch.json
  get-evacuation-batch HOST BATCH_ID
  arm-evacuation-plan HOST REVISION < plan.json
  get-evacuation-plan HOST PLAN_ID
  cancel-evacuation-plan HOST PLAN_ID
  change-host-fence HOST REVISION < fence.json
  report-capacity HOST < report.json
  observe-host HOST GENERATION < limits.json
  reserve IDEMPOTENCY_KEY < placement.json
  get PLACEMENT
  release PLACEMENT
  submit-vm < vm.json
  get-vm VM
  get-vm-recovery VM
  request-vm-recovery VM REVISION < recovery.json
  review-vm-recovery VM REVISION < review.json
  get-cold-migration VM
  request-cold-migration VM REVISION < migration.json
  review-cold-migration VM REVISION < review.json
  request-live-migration VM REVISION < migration.json
  get-live-migration VM MIGRATION
  review-live-migration VM MIGRATION VM_REVISION < review.json
  abort-live-migration VM MIGRATION MIGRATION_REVISION < evidence.json
  run-live-migration HOST GENERATION VM MIGRATION < migration-agent.json
  backup-vm HOST GENERATION VM REVISION IMAGE_ID < agent.json
  request-volume-cleanup PLACEMENT HOST_REVISION < cleanup.json
  get-volume-cleanup PLACEMENT CLEANUP
  list-volume-cleanups HOST [AFTER]
  review-volume-cleanup PLACEMENT CLEANUP REVISION < review.json
  resolve-volume-cleanup PLACEMENT CLEANUP REVISION < evidence.json
  run-volume-cleanup HOST GENERATION PLACEMENT CLEANUP < agent.json
  shutdown-vm VM REVISION
  retire-vm VM REVISION
  review-vm VM REVISION < review.json
  run-vm HOST GENERATION < agent.json`

func RunKVMClient(ctx context.Context, args []string, address, token string, input io.Reader, output io.Writer, transport http.RoundTripper) error {
	base, err := projecthttp.BaseURL(address)
	if err != nil {
		return err
	}
	plan, err := PlanKVMRequest(base, args)
	if err != nil {
		return err
	}
	if args[0] == "mirror-image" {
		api := KVMVMAPI{Target: plan.Target, Token: token, Transport: transport}
		return runKVMImageMirror(ctx, api, args[3], args[4], input, output)
	}
	if args[0] == "mirror-host-image" {
		target, _ := ParseKVMHostImageTarget(args)
		api := KVMVMAPI{Target: plan.Target, Token: token, Transport: transport}
		return RunKVMHostImageMirror(ctx, api, target, input, output, runLocalVirsh)
	}
	if args[0] == "backup-vm" {
		target, _ := ParseKVMBackupTarget(args)
		api := KVMVMAPI{Target: plan.Target, Token: token, Transport: transport}
		return RunKVMVMBackup(ctx, api, target, input, output, KVMProbe{Read: runLocalVirsh, FreeMiB: kvmLocalFreeMiB})
	}
	if args[0] == "run-volume-cleanup" {
		target, _ := ParseKVMVolumeCleanupTarget(args)
		api := KVMVMAPI{Target: plan.Target, Token: token, Transport: transport}
		return RunKVMVolumeCleanup(ctx, api, target, input, output, KVMProbe{Read: runLocalVirsh, FreeMiB: kvmLocalFreeMiB}, invokeLocalVirsh)
	}
	if args[0] == "run-vm" {
		api := KVMVMAPI{Target: plan.Target, Token: token, Transport: transport}
		runner := KVMVMRunner{KVMProbe: KVMProbe{Read: runLocalVirsh, FreeMiB: kvmLocalFreeMiB}, Write: invokeLocalVirsh, BootFile: kvmLocalBootFile,
			EnableACPI: runtime.GOARCH == "amd64" || runtime.GOARCH == "386"}
		return RunKVMVMJob(ctx, api, args[4], args[5], input, output, runner)
	}
	if args[0] == "run-live-migration" {
		target, _ := ParseKVMLiveMigrationTarget(args)
		api := KVMVMAPI{Target: plan.Target, Token: token, Transport: transport}
		runner := KVMLiveMigrationRunner{KVMProbe: KVMProbe{Read: runLocalVirsh},
			Write: invokeLocalVirsh, BootFile: kvmLocalBootFile,
			Remote: func(ctx context.Context, address *url.URL, args ...string) ([]byte, error) {
				return invokeKVMVirsh(ctx, address.String(), append([]string{"--readonly"}, args...)...)
			}}
		return RunKVMLiveMigration(ctx, api, target, input, output, runner)
	}
	if args[0] == "plan-network-dhcp" || args[0] == "apply-network-dhcp" {
		api := KVMVMAPI{Target: plan.Target, Token: token, Transport: transport}
		target := KVMDHCPTarget{Host: args[4], Generation: args[5], Network: args[6], Uuid: args[7], Apply: args[0] == "apply-network-dhcp"}
		return RunKVMDHCP(ctx, api, target, output, runLocalVirsh, invokeLocalVirsh)
	}
	if args[0] == "observe-host" {
		return ObserveKVMHost(ctx, plan.Target, args[4], args[5], token, input, output, transport, KVMProbe{Read: runLocalVirsh, FreeMiB: kvmLocalFreeMiB})
	}
	return projecthttp.Run(ctx, plan, token, input, output, transport)
}

func PlanKVMRequest(base *url.URL, args []string) (projecthttp.Request, error) {
	usage := errors.New(kvmUsage)
	if len(args) < 4 || !models.ValidRequestSegment(args[1]) || !models.ValidRequestSegment(args[2]) || !models.ValidRequestSegment(args[3]) {
		return projecthttp.Request{}, usage
	}
	site := base.JoinPath("v1", "tenants", url.PathEscape(args[1]), "projects", url.PathEscape(args[2]), "kvm", "sites", url.PathEscape(args[3]))
	if args[0] == "register-host" && len(args) == 4 {
		return projecthttp.Request{Target: site.JoinPath("hosts"), Method: http.MethodPost, Status: http.StatusCreated, InputLimit: 8192}, nil
	}
	if args[0] == "register-network" && len(args) == 4 {
		return projecthttp.Request{Target: site.JoinPath("networks"), Method: http.MethodPost, Status: http.StatusCreated, InputLimit: 2048}, nil
	}
	if args[0] == "register-image" && len(args) == 4 {
		return projecthttp.Request{Target: site.JoinPath("images"), Method: http.MethodPost, Status: http.StatusCreated, InputLimit: 1024}, nil
	}
	if args[0] == "register-shared-storage" && len(args) == 4 {
		return projecthttp.Request{Target: site.JoinPath("shared-storage-pools"), Method: http.MethodPost, Status: http.StatusCreated, InputLimit: 1024}, nil
	}
	if args[0] == "submit-vm" && len(args) == 4 {
		return projecthttp.Request{Target: site.JoinPath("vms"), Method: http.MethodPost, Status: http.StatusAccepted, InputLimit: 2048}, nil
	}
	if len(args) < 5 || !models.ValidRequestSegment(args[4]) {
		return projecthttp.Request{}, usage
	}
	id := url.PathEscape(args[4])
	host := site.JoinPath("hosts", id)
	placement := site.JoinPath("placements", id)
	switch args[0] {
	case "request-volume-cleanup", "get-volume-cleanup", "list-volume-cleanups", "review-volume-cleanup", "resolve-volume-cleanup", "run-volume-cleanup":
		return planKVMVolumeCleanupRequest(host, placement, args)
	}
	if args[0] == "run-live-migration" {
		if _, ok := ParseKVMLiveMigrationTarget(args); !ok {
			return projecthttp.Request{}, usage
		}
		return projecthttp.Request{Target: host, Method: http.MethodGet, Status: http.StatusOK}, nil
	}
	if kvmMachineUUID.MatchString(args[4]) && len(args) >= 6 && kvmUnitName.MatchString(args[5]) {
		switch args[0] {
		case "get-live-migration":
			if len(args) == 6 {
				return projecthttp.Request{Target: site.JoinPath("vms", id, "live-migrations", args[5]), Method: http.MethodGet, Status: http.StatusOK}, nil
			}
		case "review-live-migration", "abort-live-migration":
			if len(args) == 7 {
				revision, err := strconv.ParseInt(args[6], 10, 64)
				if err == nil && revision > 0 {
					action := "review"
					if args[0] == "abort-live-migration" {
						action = "abort"
					}
					return projecthttp.Request{Target: site.JoinPath("vms", id, "live-migrations", args[5], action), Method: http.MethodPost,
						Status: http.StatusOK, InputLimit: 1024, Match: strconv.Quote(strconv.FormatInt(revision, 10))}, nil
				}
			}
		}
	}
	if args[0] == "mirror-host-image" {
		if _, ok := ParseKVMHostImageTarget(args); !ok {
			return projecthttp.Request{}, usage
		}
		return projecthttp.Request{Target: host, Method: http.MethodGet, Status: http.StatusOK}, nil
	}
	if len(args) == 6 && kvmUnitName.MatchString(args[4]) && kvmUnitName.MatchString(args[5]) {
		switch args[0] {
		case "get-host-image":
			return projecthttp.Request{Target: host.JoinPath("images", args[5]), Method: http.MethodGet, Status: http.StatusOK}, nil
		case "report-host-image":
			return projecthttp.Request{Target: host.JoinPath("images", args[5]), Method: http.MethodPut, Status: http.StatusOK, InputLimit: 1024}, nil
		}
	}
	if args[0] == "get-evacuation-batch" && len(args) == 6 && models.ValidRequestSegment(args[5]) {
		return projecthttp.Request{Target: host.JoinPath("fence", "evacuation-batches", url.PathEscape(args[5])),
			Method: http.MethodGet, Status: http.StatusOK}, nil
	}
	if (args[0] == "get-evacuation-plan" || args[0] == "cancel-evacuation-plan") && len(args) == 6 && models.ValidRequestSegment(args[5]) {
		method := http.MethodGet
		if args[0] == "cancel-evacuation-plan" {
			method = http.MethodDelete
		}
		return projecthttp.Request{Target: host.JoinPath("fence", "evacuation-plans", url.PathEscape(args[5])),
			Method: method, Status: http.StatusOK}, nil
	}
	if args[0] == "backup-vm" {
		if _, ok := ParseKVMBackupTarget(args); !ok {
			return projecthttp.Request{}, usage
		}
		return projecthttp.Request{Target: host, Method: http.MethodGet, Status: http.StatusOK}, nil
	}
	if args[0] == "list-host-fence-work" && (len(args) == 5 || len(args) == 6) {
		target := host.JoinPath("fence", "work")
		if len(args) == 6 {
			if !models.ValidRequestSegment(args[5]) {
				return projecthttp.Request{}, usage
			}
			target.RawQuery = url.Values{"after": {args[5]}}.Encode()
		}
		return projecthttp.Request{Target: target, Method: http.MethodGet, Status: http.StatusOK}, nil
	}
	if len(args) == 5 {
		switch args[0] {
		case "get-shared-storage":
			return projecthttp.Request{Target: site.JoinPath("shared-storage-pools", id), Method: http.MethodGet, Status: http.StatusOK}, nil
		case "get-network":
			return projecthttp.Request{Target: site.JoinPath("networks", id), Method: http.MethodGet, Status: http.StatusOK}, nil
		case "get-image", "mirror-image":
			return projecthttp.Request{Target: site.JoinPath("images", id), Method: http.MethodGet, Status: http.StatusOK}, nil
		case "get-vm":
			return projecthttp.Request{Target: site.JoinPath("vms", id), Method: http.MethodGet, Status: http.StatusOK}, nil
		case "get-vm-recovery":
			return projecthttp.Request{Target: site.JoinPath("vms", id, "fence-recovery"), Method: http.MethodGet, Status: http.StatusOK}, nil
		case "get-cold-migration":
			return projecthttp.Request{Target: site.JoinPath("vms", id, "cold-migration"), Method: http.MethodGet, Status: http.StatusOK}, nil
		case "get-host":
			return projecthttp.Request{Target: host, Method: http.MethodGet, Status: http.StatusOK}, nil
		case "get-host-fence":
			return projecthttp.Request{Target: host.JoinPath("fence"), Method: http.MethodGet, Status: http.StatusOK}, nil
		case "report-capacity":
			return projecthttp.Request{Target: host.JoinPath("capacity"), Method: http.MethodPut, Status: http.StatusOK, InputLimit: 16 << 10}, nil
		case "reserve":
			return projecthttp.Request{Target: site.JoinPath("placements"), Method: http.MethodPost, Status: http.StatusCreated, InputLimit: 4096, Key: args[4]}, nil
		case "get":
			return projecthttp.Request{Target: placement, Method: http.MethodGet, Status: http.StatusOK}, nil
		case "release":
			return projecthttp.Request{Target: placement, Method: http.MethodDelete, Status: http.StatusOK}, nil
		}
	}
	if len(args) == 8 && (args[0] == "plan-network-dhcp" || args[0] == "apply-network-dhcp") {
		generation, err := strconv.ParseInt(args[5], 10, 64)
		if err != nil || generation < 1 || !kvmUnitName.MatchString(args[6]) || !kvmMachineUUID.MatchString(args[7]) {
			return projecthttp.Request{}, usage
		}
		return projecthttp.Request{Target: host, Method: http.MethodGet, Status: http.StatusOK}, nil
	}
	if len(args) == 6 {
		revision, err := strconv.ParseInt(args[5], 10, 64)
		if err != nil || revision < 1 {
			return projecthttp.Request{}, usage
		}
		switch args[0] {
		case "request-live-migration":
			if !kvmMachineUUID.MatchString(args[4]) {
				return projecthttp.Request{}, usage
			}
			return projecthttp.Request{Target: site.JoinPath("vms", id, "live-migrations"), Method: http.MethodPost, Status: http.StatusAccepted,
				InputLimit: 1024, Match: strconv.Quote(strconv.FormatInt(revision, 10))}, nil
		case "change-host":
			return projecthttp.Request{Target: host, Method: http.MethodPatch, Status: http.StatusOK,
				InputLimit: 1024, Match: strconv.Quote(strconv.FormatInt(revision, 10))}, nil
		case "change-host-fence":
			return projecthttp.Request{Target: host.JoinPath("fence"), Method: http.MethodPost, Status: http.StatusOK,
				InputLimit: 1024, Match: strconv.Quote(strconv.FormatInt(revision, 10))}, nil
		case "confirm-evacuation-cleanup":
			return projecthttp.Request{Target: host.JoinPath("fence", "cleanup"), Method: http.MethodPost, Status: http.StatusOK,
				InputLimit: 1024, Match: strconv.Quote(strconv.FormatInt(revision, 10))}, nil
		case "request-evacuation-batch":
			return projecthttp.Request{Target: host.JoinPath("fence", "evacuation-batches"), Method: http.MethodPost, Status: http.StatusAccepted,
				InputLimit: kvmEvacuationBatchBodyLimit, Match: strconv.Quote(strconv.FormatInt(revision, 10))}, nil
		case "arm-evacuation-plan":
			return projecthttp.Request{Target: host.JoinPath("fence", "evacuation-plans"), Method: http.MethodPost, Status: http.StatusCreated,
				InputLimit: kvmEvacuationBatchBodyLimit, Match: strconv.Quote(strconv.FormatInt(revision, 10))}, nil
		case "shutdown-vm":
			return projecthttp.Request{Target: site.JoinPath("vms", id, "shutdown"), Method: http.MethodPost,
				Status: http.StatusAccepted, Match: strconv.Quote(strconv.FormatInt(revision, 10))}, nil
		case "retire-vm":
			return projecthttp.Request{Target: site.JoinPath("vms", id, "retirement"), Method: http.MethodPost,
				Status: http.StatusAccepted, Match: strconv.Quote(strconv.FormatInt(revision, 10))}, nil
		case "review-vm":
			return projecthttp.Request{Target: site.JoinPath("vms", id, "review"), Method: http.MethodPost, Status: http.StatusOK,
				InputLimit: 1024, Match: strconv.Quote(strconv.FormatInt(revision, 10))}, nil
		case "request-vm-recovery":
			return projecthttp.Request{Target: site.JoinPath("vms", id, "fence-recovery"), Method: http.MethodPost, Status: http.StatusAccepted,
				InputLimit: 2048, Match: strconv.Quote(strconv.FormatInt(revision, 10))}, nil
		case "review-vm-recovery":
			return projecthttp.Request{Target: site.JoinPath("vms", id, "fence-recovery", "review"), Method: http.MethodPost, Status: http.StatusOK,
				InputLimit: 1024, Match: strconv.Quote(strconv.FormatInt(revision, 10))}, nil
		case "request-cold-migration":
			return projecthttp.Request{Target: site.JoinPath("vms", id, "cold-migration"), Method: http.MethodPost, Status: http.StatusAccepted,
				InputLimit: 2048, Match: strconv.Quote(strconv.FormatInt(revision, 10))}, nil
		case "review-cold-migration":
			return projecthttp.Request{Target: site.JoinPath("vms", id, "cold-migration", "review"), Method: http.MethodPost, Status: http.StatusOK,
				InputLimit: 1024, Match: strconv.Quote(strconv.FormatInt(revision, 10))}, nil
		case "observe-host", "run-vm":
			return projecthttp.Request{Target: host, Method: http.MethodGet, Status: http.StatusOK}, nil
		}
	}
	return projecthttp.Request{}, usage
}
