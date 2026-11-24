package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/birdordev/plumego/x/messaging/mq"
)

const (
	sandboxLabel  = "io.plumego.scheduler.sandbox"
	sandboxMaxAge = 30 * time.Second
)

type sandboxExecutor struct {
	binary, socket, runtime, image, scope string
}

func newSandboxClient() (*sandboxExecutor, error) {
	binary, err := exec.LookPath("docker")
	if err != nil {
		return nil, errors.New("sandbox Docker client unavailable")
	}
	s := &sandboxExecutor{binary: binary, socket: os.Getenv("SCHEDULER_SANDBOX_SOCKET"),
		scope: os.Getenv("SCHEDULER_SANDBOX_SCOPE")}
	if !filepath.IsAbs(s.socket) || !runCursorID.MatchString(s.scope) {
		return nil, errors.New("invalid sandbox socket or scope")
	}
	return s, nil
}

func loadSandboxExecutor(ctx context.Context) (*sandboxExecutor, error) {
	s, err := newSandboxClient()
	if err != nil {
		return nil, err
	}
	s.runtime = os.Getenv("SCHEDULER_SANDBOX_RUNTIME")
	s.image = os.Getenv("SCHEDULER_SANDBOX_IMAGE")
	if s.runtime == "runc" && os.Getenv("SCHEDULER_SANDBOX_BOUNDARY") != "dedicated-vm" ||
		s.runtime != "runc" && s.runtime != "runsc" ||
		!strings.HasPrefix(s.image, "sha256:") || len(s.image) != len("sha256:")+64 {
		return nil, errors.New("invalid sandbox executor configuration")
	}
	if _, err := hex.DecodeString(strings.TrimPrefix(s.image, "sha256:")); err != nil {
		return nil, errors.New("invalid sandbox image digest")
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	var info struct {
		OSType, CgroupDriver, CgroupVersion            string
		MemoryLimit, SwapLimit, CPUCfsQuota, PidsLimit bool
		Runtimes                                       map[string]json.RawMessage
	}
	raw, err := s.command(ctx, "info", "--format", "{{json .}}").Output()
	if err != nil || json.Unmarshal(raw, &info) != nil || info.OSType != "linux" ||
		info.CgroupVersion != "2" || info.CgroupDriver == "" || info.CgroupDriver == "none" ||
		!info.MemoryLimit || !info.SwapLimit || !info.CPUCfsQuota || !info.PidsLimit || len(info.Runtimes[s.runtime]) == 0 {
		return nil, errors.New("sandbox runtime lacks required Linux resource controls")
	}
	var image struct {
		ID, OS string
		Config struct {
			Env     []string
			Volumes map[string]json.RawMessage
		}
	}
	raw, err = s.command(ctx, "image", "inspect", "--format", "{{json .}}", s.image).Output()
	if err != nil || json.Unmarshal(raw, &image) != nil || image.ID != s.image || image.OS != "linux" ||
		len(image.Config.Volumes) != 0 {
		return nil, errors.New("sandbox requires a locally pinned Linux image without volumes")
	}
	for _, item := range image.Config.Env {
		if !strings.HasPrefix(item, "PATH=") {
			return nil, errors.New("sandbox image contains unexpected environment variables")
		}
	}
	// 用实际执行路径检查镜像、运行时和内置系统调用策略。
	if err := s.execute(ctx, "exit 0"); err != nil {
		return nil, errors.New("sandbox executor self-test failed")
	}
	return s, nil
}

func (s *sandboxExecutor) command(ctx context.Context, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, s.binary, append([]string{"--host", "unix://" + s.socket}, args...)...)
	cmd.Env = []string{"LANG=C", "PATH=/usr/local/bin:/usr/bin:/bin", "DOCKER_CONFIG=/dev/null"}
	cmd.WaitDelay = time.Second
	return cmd
}

func parseSandboxScript(payload json.RawMessage) (string, error) {
	var input struct {
		Script string `json:"script"`
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil || strings.TrimSpace(input.Script) == "" ||
		len(input.Script) > 16<<10 || strings.ContainsRune(input.Script, 0) {
		return "", errInvalidTask
	}
	return input.Script, nil
}

var errSandboxExecution = errors.New("sandbox task failed")
var errSandboxCleanup = errors.New("sandbox cleanup unavailable")
var errSandboxRuntime = errors.New("sandbox runtime unavailable")

func (s *sandboxExecutor) execute(ctx context.Context, script string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	runCtx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	name := "plumego-job-" + rand.Text()
	label := sandboxLabel + "=" + s.scope
	cmd := s.command(runCtx, "run",
		"--name", name, "--label", label, "--runtime", s.runtime,
		"--rm", "--interactive", "--pull=never", "--no-healthcheck", "--log-driver=none",
		"--network=none", "--ipc=none", "--read-only", "--tmpfs=/tmp:rw,noexec,nosuid,nodev,size=8m,mode=1777",
		"--cap-drop=ALL", "--security-opt=no-new-privileges", "--security-opt=seccomp=builtin",
		"--user=65534:65534", "--workdir=/tmp", "--pids-limit=16", "--ulimit=nofile=64:64",
		"--cpus=0.5", "--memory=64m", "--memory-swap=64m",
		"--entrypoint=/usr/bin/timeout", s.image, "--signal=KILL", "8s", "/bin/sh", "-s")
	cmd.Stdin = strings.NewReader(script)
	err := cmd.Run()
	if err == nil {
		return nil
	}
	cleanupCtx, stopCleanup := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer stopCleanup()
	if err := s.remove(cleanupCtx, name); err != nil {
		return err
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if runCtx.Err() != nil {
		return errSandboxRuntime
	}
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() == 125 || exit.ExitCode() < 0 {
		return errSandboxRuntime
	}
	return errSandboxExecution
}

func (s *sandboxExecutor) remove(ctx context.Context, id string) error {
	if err := s.command(ctx, "rm", "--force", id).Run(); err == nil {
		return nil
	}
	if present, err := s.exists(ctx, id); err != nil || present {
		return errSandboxCleanup
	}
	return nil
}

func (s *sandboxExecutor) exists(ctx context.Context, id string) (bool, error) {
	filter := "id=" + id
	if strings.HasPrefix(id, "plumego-job-") {
		filter = "name=^/" + id + "$"
	}
	raw, err := s.command(ctx, "ps", "--all", "--quiet", "--filter", filter).Output()
	return len(bytes.TrimSpace(raw)) != 0, err
}

func (s *sandboxExecutor) reap(ctx context.Context, cutoff time.Time) error {
	raw, err := s.command(ctx, "ps", "--all", "--quiet", "--no-trunc", "--filter", "label="+sandboxLabel+"="+s.scope).Output()
	if err != nil {
		return errSandboxRuntime
	}
	for _, id := range strings.Fields(string(raw)) {
		createdRaw, err := s.command(ctx, "inspect", "--type=container", "--format", "{{.Created}}", id).Output()
		if err != nil {
			// 容器可能已由守护进程自动清理。确认其已消失后继续扫描。
			if present, err := s.exists(ctx, id); err != nil || present {
				return errSandboxCleanup
			}
			continue
		}
		created, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(string(createdRaw)))
		if err != nil {
			return errSandboxRuntime
		}
		if !created.After(cutoff) {
			if err := s.remove(ctx, id); err != nil {
				return err
			}
		}
	}
	return nil
}

func (r repository) sandboxHandler(sandbox *sandboxExecutor) mq.TaskHandler {
	return func(ctx context.Context, task mq.Task) error {
		script, err := parseSandboxScript(task.Payload)
		if err != nil {
			return &mq.TaskFailure{Kind: mq.TaskFailurePermanent}
		}
		if sandbox == nil {
			return errSandboxRuntime
		}
		if err := sandbox.execute(ctx, script); err != nil {
			if errors.Is(err, errSandboxExecution) {
				return &mq.TaskFailure{Kind: mq.TaskFailurePermanent, Cause: err}
			}
			return err
		}
		return r.recordResult(ctx, task)
	}
}

func runSandboxGC(ctx context.Context, sandbox *sandboxExecutor) error {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for ctx.Err() == nil {
		gcCtx, cancel := context.WithTimeout(ctx, 4*time.Second)
		raw, err := sandbox.command(gcCtx, "info", "--format", "{{.SystemTime}}").Output()
		if err == nil {
			var now time.Time
			now, err = time.Parse(time.RFC3339Nano, strings.TrimSpace(string(raw)))
			if err == nil {
				err = sandbox.reap(gcCtx, now.Add(-sandboxMaxAge))
			}
		}
		cancel()
		if err != nil && ctx.Err() == nil {
			return errSandboxCleanup
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
	return nil
}
