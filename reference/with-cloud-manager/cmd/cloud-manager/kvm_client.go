package main

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"runtime"
	"strconv"
)

const kvmUsage = `usage: cloud-manager kvm COMMAND TENANT PROJECT SITE
  register-host < host.json
  register-network < ipv4-pool.json
  get-network NETWORK
  register-image < image.json
  get-image IMAGE
  mirror-image IMAGE < mirror.json
  plan-network-dhcp HOST GENERATION NETWORK NETWORK_UUID
  apply-network-dhcp HOST GENERATION NETWORK NETWORK_UUID
  get-host HOST
  change-host HOST REVISION < change.json
  get-host-fence HOST
  list-host-fence-work HOST [AFTER]
  confirm-evacuation-cleanup HOST REVISION < cleanup.json
  request-evacuation-batch HOST REVISION < batch.json
  get-evacuation-batch HOST BATCH_ID
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
  backup-vm HOST GENERATION VM REVISION IMAGE_ID < agent.json
  shutdown-vm VM REVISION
  retire-vm VM REVISION
  review-vm VM REVISION < review.json
  run-vm HOST GENERATION < agent.json`

func runKVMClient(ctx context.Context, args []string, address, token string, input io.Reader, output io.Writer, transport http.RoundTripper) error {
	base, err := operationClientBase(address)
	if err != nil {
		return err
	}
	plan, err := planKVMRequest(base, args)
	if err != nil {
		return err
	}
	if args[0] == "mirror-image" {
		api := kvmVMAPI{target: plan.target, token: token, transport: transport}
		return runKVMImageMirror(ctx, api, args[3], args[4], input, output)
	}
	if args[0] == "backup-vm" {
		target, _ := parseKVMBackupTarget(args)
		api := kvmVMAPI{target: plan.target, token: token, transport: transport}
		return runKVMVMBackup(ctx, api, target, input, output, kvmProbe{read: runLocalVirsh, freeMiB: kvmLocalFreeMiB})
	}
	if args[0] == "run-vm" {
		api := kvmVMAPI{target: plan.target, token: token, transport: transport}
		runner := kvmVMRunner{kvmProbe: kvmProbe{read: runLocalVirsh, freeMiB: kvmLocalFreeMiB}, write: invokeLocalVirsh, bootFile: kvmLocalBootFile,
			enableACPI: runtime.GOARCH == "amd64" || runtime.GOARCH == "386"}
		return runKVMVMJob(ctx, api, args[4], args[5], input, output, runner)
	}
	if args[0] == "plan-network-dhcp" || args[0] == "apply-network-dhcp" {
		api := kvmVMAPI{target: plan.target, token: token, transport: transport}
		target := kvmDHCPTarget{host: args[4], generation: args[5], network: args[6], uuid: args[7], apply: args[0] == "apply-network-dhcp"}
		return runKVMDHCP(ctx, api, target, output, runLocalVirsh, invokeLocalVirsh)
	}
	if args[0] == "observe-host" {
		return observeKVMHost(ctx, plan.target, args[4], args[5], token, input, output, transport, kvmProbe{read: runLocalVirsh, freeMiB: kvmLocalFreeMiB})
	}
	return runProjectRequest(ctx, plan, token, input, output, transport)
}

func planKVMRequest(base *url.URL, args []string) (operationRequestPlan, error) {
	usage := errors.New(kvmUsage)
	if len(args) < 4 || !operationClientSegment(args[1]) || !operationClientSegment(args[2]) || !operationClientSegment(args[3]) {
		return operationRequestPlan{}, usage
	}
	site := base.JoinPath("v1", "tenants", url.PathEscape(args[1]), "projects", url.PathEscape(args[2]), "kvm", "sites", url.PathEscape(args[3]))
	if args[0] == "register-host" && len(args) == 4 {
		return operationRequestPlan{target: site.JoinPath("hosts"), method: http.MethodPost, status: http.StatusCreated, inputLimit: 8192}, nil
	}
	if args[0] == "register-network" && len(args) == 4 {
		return operationRequestPlan{target: site.JoinPath("networks"), method: http.MethodPost, status: http.StatusCreated, inputLimit: 2048}, nil
	}
	if args[0] == "register-image" && len(args) == 4 {
		return operationRequestPlan{target: site.JoinPath("images"), method: http.MethodPost, status: http.StatusCreated, inputLimit: 1024}, nil
	}
	if args[0] == "submit-vm" && len(args) == 4 {
		return operationRequestPlan{target: site.JoinPath("vms"), method: http.MethodPost, status: http.StatusAccepted, inputLimit: 2048}, nil
	}
	if len(args) < 5 || !operationClientSegment(args[4]) {
		return operationRequestPlan{}, usage
	}
	id := url.PathEscape(args[4])
	host := site.JoinPath("hosts", id)
	placement := site.JoinPath("placements", id)
	if args[0] == "get-evacuation-batch" && len(args) == 6 && operationClientSegment(args[5]) {
		return operationRequestPlan{target: host.JoinPath("fence", "evacuation-batches", url.PathEscape(args[5])),
			method: http.MethodGet, status: http.StatusOK}, nil
	}
	if args[0] == "backup-vm" {
		if _, ok := parseKVMBackupTarget(args); !ok {
			return operationRequestPlan{}, usage
		}
		return operationRequestPlan{target: host, method: http.MethodGet, status: http.StatusOK}, nil
	}
	if args[0] == "list-host-fence-work" && (len(args) == 5 || len(args) == 6) {
		target := host.JoinPath("fence", "work")
		if len(args) == 6 {
			if !operationClientSegment(args[5]) {
				return operationRequestPlan{}, usage
			}
			target.RawQuery = url.Values{"after": {args[5]}}.Encode()
		}
		return operationRequestPlan{target: target, method: http.MethodGet, status: http.StatusOK}, nil
	}
	if len(args) == 5 {
		switch args[0] {
		case "get-network":
			return operationRequestPlan{target: site.JoinPath("networks", id), method: http.MethodGet, status: http.StatusOK}, nil
		case "get-image", "mirror-image":
			return operationRequestPlan{target: site.JoinPath("images", id), method: http.MethodGet, status: http.StatusOK}, nil
		case "get-vm":
			return operationRequestPlan{target: site.JoinPath("vms", id), method: http.MethodGet, status: http.StatusOK}, nil
		case "get-vm-recovery":
			return operationRequestPlan{target: site.JoinPath("vms", id, "fence-recovery"), method: http.MethodGet, status: http.StatusOK}, nil
		case "get-cold-migration":
			return operationRequestPlan{target: site.JoinPath("vms", id, "cold-migration"), method: http.MethodGet, status: http.StatusOK}, nil
		case "get-host":
			return operationRequestPlan{target: host, method: http.MethodGet, status: http.StatusOK}, nil
		case "get-host-fence":
			return operationRequestPlan{target: host.JoinPath("fence"), method: http.MethodGet, status: http.StatusOK}, nil
		case "report-capacity":
			return operationRequestPlan{target: host.JoinPath("capacity"), method: http.MethodPut, status: http.StatusOK, inputLimit: 16 << 10}, nil
		case "reserve":
			return operationRequestPlan{target: site.JoinPath("placements"), method: http.MethodPost, status: http.StatusCreated, inputLimit: 4096, key: args[4]}, nil
		case "get":
			return operationRequestPlan{target: placement, method: http.MethodGet, status: http.StatusOK}, nil
		case "release":
			return operationRequestPlan{target: placement, method: http.MethodDelete, status: http.StatusOK}, nil
		}
	}
	if len(args) == 8 && (args[0] == "plan-network-dhcp" || args[0] == "apply-network-dhcp") {
		generation, err := strconv.ParseInt(args[5], 10, 64)
		if err != nil || generation < 1 || !kvmUnitName.MatchString(args[6]) || !kvmMachineUUID.MatchString(args[7]) {
			return operationRequestPlan{}, usage
		}
		return operationRequestPlan{target: host, method: http.MethodGet, status: http.StatusOK}, nil
	}
	if len(args) == 6 {
		revision, err := strconv.ParseInt(args[5], 10, 64)
		if err != nil || revision < 1 {
			return operationRequestPlan{}, usage
		}
		switch args[0] {
		case "change-host":
			return operationRequestPlan{target: host, method: http.MethodPatch, status: http.StatusOK,
				inputLimit: 1024, match: strconv.Quote(strconv.FormatInt(revision, 10))}, nil
		case "change-host-fence":
			return operationRequestPlan{target: host.JoinPath("fence"), method: http.MethodPost, status: http.StatusOK,
				inputLimit: 1024, match: strconv.Quote(strconv.FormatInt(revision, 10))}, nil
		case "confirm-evacuation-cleanup":
			return operationRequestPlan{target: host.JoinPath("fence", "cleanup"), method: http.MethodPost, status: http.StatusOK,
				inputLimit: 1024, match: strconv.Quote(strconv.FormatInt(revision, 10))}, nil
		case "request-evacuation-batch":
			return operationRequestPlan{target: host.JoinPath("fence", "evacuation-batches"), method: http.MethodPost, status: http.StatusAccepted,
				inputLimit: kvmEvacuationBatchBodyLimit, match: strconv.Quote(strconv.FormatInt(revision, 10))}, nil
		case "shutdown-vm":
			return operationRequestPlan{target: site.JoinPath("vms", id, "shutdown"), method: http.MethodPost,
				status: http.StatusAccepted, match: strconv.Quote(strconv.FormatInt(revision, 10))}, nil
		case "retire-vm":
			return operationRequestPlan{target: site.JoinPath("vms", id, "retirement"), method: http.MethodPost,
				status: http.StatusAccepted, match: strconv.Quote(strconv.FormatInt(revision, 10))}, nil
		case "review-vm":
			return operationRequestPlan{target: site.JoinPath("vms", id, "review"), method: http.MethodPost, status: http.StatusOK,
				inputLimit: 1024, match: strconv.Quote(strconv.FormatInt(revision, 10))}, nil
		case "request-vm-recovery":
			return operationRequestPlan{target: site.JoinPath("vms", id, "fence-recovery"), method: http.MethodPost, status: http.StatusAccepted,
				inputLimit: 2048, match: strconv.Quote(strconv.FormatInt(revision, 10))}, nil
		case "review-vm-recovery":
			return operationRequestPlan{target: site.JoinPath("vms", id, "fence-recovery", "review"), method: http.MethodPost, status: http.StatusOK,
				inputLimit: 1024, match: strconv.Quote(strconv.FormatInt(revision, 10))}, nil
		case "request-cold-migration":
			return operationRequestPlan{target: site.JoinPath("vms", id, "cold-migration"), method: http.MethodPost, status: http.StatusAccepted,
				inputLimit: 2048, match: strconv.Quote(strconv.FormatInt(revision, 10))}, nil
		case "review-cold-migration":
			return operationRequestPlan{target: site.JoinPath("vms", id, "cold-migration", "review"), method: http.MethodPost, status: http.StatusOK,
				inputLimit: 1024, match: strconv.Quote(strconv.FormatInt(revision, 10))}, nil
		case "observe-host", "run-vm":
			return operationRequestPlan{target: host, method: http.MethodGet, status: http.StatusOK}, nil
		}
	}
	return operationRequestPlan{}, usage
}
