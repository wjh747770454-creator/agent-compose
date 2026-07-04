package execution

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	appconfig "agent-compose/pkg/config"
	driverpkg "agent-compose/pkg/driver"
	domain "agent-compose/pkg/model"
)

func TestDriverConversionWorkflows(t *testing.T) {
	if ToDriverSession(nil) != nil {
		t.Fatalf("nil session should map to nil")
	}
	now := time.Date(2026, 7, 4, 8, 0, 0, 0, time.UTC)
	session := &domain.Session{
		Summary: domain.SessionSummary{
			ID: "session-1", Driver: "docker", GuestImage: "guest:latest", RuntimeRef: "runtime-1",
			WorkspacePath: "/workspace", CreatedAt: now, UpdatedAt: now,
		},
		EnvItems:        []domain.SessionEnvVar{{Name: "A", Value: "B", Secret: true}},
		RuntimeEnvItems: []domain.SessionEnvVar{{Name: "R", Value: "V"}},
	}
	driverSession := ToDriverSession(session)
	if driverSession.Summary.ID != "session-1" || len(driverSession.EnvItems) != 1 || !driverSession.EnvItems[0].Secret || len(driverSession.RuntimeEnvItems) != 1 {
		t.Fatalf("driver session = %#v", driverSession)
	}
	vmState := domain.VMState{Driver: "docker", Mode: "runtime", BoxName: "box", BoxID: "box-id", Image: "image", Registry: "registry", RuntimeHome: "/root", StartedAt: now, StoppedAt: now, LastError: "none", BootstrapRef: "boot"}
	driverVMState := ToDriverVMState(vmState)
	if got := FromDriverVMState(driverVMState); got != vmState {
		t.Fatalf("vm state round trip = %#v", got)
	}
	proxyState := domain.ProxyState{ProxyPath: "/jupyter", GuestHost: "127.0.0.1", HostPort: 7410, GuestPort: 8888, JupyterURL: "http://guest", Token: "token"}
	driverProxyState := ToDriverProxyState(proxyState)
	if got := FromDriverProxyState(driverProxyState); got != proxyState {
		t.Fatalf("proxy state round trip = %#v", got)
	}
	spec := ToDriverExecSpec(domain.ExecSpec{Command: "echo", Args: []string{"ok"}, Env: map[string]string{"A": "B"}, Cwd: "/workspace"})
	if spec.Command != "echo" || spec.Args[0] != "ok" || spec.Env["A"] != "B" || spec.Cwd != "/workspace" {
		t.Fatalf("exec spec = %#v", spec)
	}
	info := FromDriverSessionVMInfo(driverpkg.SessionVMInfo{BoxID: "box-id", JupyterURL: "http://jupyter", ProxyState: &driverProxyState})
	if info.BoxID != "box-id" || info.ProxyState == nil || info.ProxyState.Token != "token" {
		t.Fatalf("session vm info = %#v", info)
	}
	result := FromDriverExecResult(driverpkg.ExecResult{ExitCode: 2, Stdout: "out", Stderr: "err", Output: "outerr", Success: false})
	if result.ExitCode != 2 || result.Output != "outerr" || result.Success {
		t.Fatalf("exec result = %#v", result)
	}

	config := &appconfig.Config{GuestWorkspacePath: "/workspace", GuestStateRoot: "/state", GuestRuntimeRoot: "/runtime", Version: "v-test"}
	commandReq := RuntimeCommandRequestPayload(config, domain.LoaderCommandRequest{
		Mode: "SHELL", Script: "echo ok", Env: map[string]string{"A": "B"},
	}, "/state/cells/cell-1")
	if commandReq.Mode != "shell" || commandReq.Cwd != "/workspace" || commandReq.MaxOutputBytes != DefaultLoaderCommandMaxOutputBytes {
		t.Fatalf("runtime command request = %#v", commandReq)
	}
	session.EnvItems = []domain.SessionEnvVar{{Name: "USER_VAR", Value: "ok"}, {Name: "LLM_API_KEY", Value: "secret"}}
	session.RuntimeEnvItems = []domain.SessionEnvVar{{Name: "MANAGED", Value: "yes"}}
	env := BuildSessionExecEnv(config, session, "/home/agent")
	if env["USER_VAR"] != "ok" || env["LLM_API_KEY"] != "" || env["MANAGED"] != "yes" || env["SESSION_ID"] != "session-1" {
		t.Fatalf("session exec env = %#v", env)
	}
	execSpec := BuildLoaderCommandExecSpec(config, session, "/state/cells/cell-1/request.json", "/home/agent")
	if execSpec.Command != "sh" || len(execSpec.Args) != 2 || !strings.Contains(execSpec.Args[1], "agent-compose-runtime exec") || execSpec.Cwd != "/workspace" {
		t.Fatalf("loader command spec = %#v", execSpec)
	}
	artifactDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(artifactDir, "stdout.txt"), []byte("existing"), 0o644); err != nil {
		t.Fatalf("write existing artifact: %v", err)
	}
	if err := MirrorRuntimeCommandArtifacts(artifactDir, domain.RuntimeCommandResult{Stdout: "new", Stderr: "err", Output: "out"}); err != nil {
		t.Fatalf("MirrorRuntimeCommandArtifacts returned error: %v", err)
	}
	if got, err := os.ReadFile(filepath.Join(artifactDir, "stdout.txt")); err != nil || string(got) != "existing" {
		t.Fatalf("stdout artifact = %q err=%v", got, err)
	}
	if got, err := os.ReadFile(filepath.Join(artifactDir, "stderr.txt")); err != nil || string(got) != "err" {
		t.Fatalf("stderr artifact = %q err=%v", got, err)
	}
}
