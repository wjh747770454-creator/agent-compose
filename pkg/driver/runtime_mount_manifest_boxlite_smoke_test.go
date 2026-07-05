//go:build boxlitecgo

package driver

import (
	"context"
	"testing"
	"time"
)

func TestSmokeBoxLiteRuntimeMountManifestDirectoryOnlyStarts(t *testing.T) {
	runtimeSmokeEnabled(t, RuntimeDriverBoxlite)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	config := newRuntimeSmokeConfig(t, RuntimeDriverBoxlite)
	session, vmState, proxyState := newRuntimeSmokeSession(t, ctx, config, RuntimeDriverBoxlite)
	assertDirectoryOnlyRuntimeSmokeManifest(t, session, RuntimeDriverBoxlite)

	runtime := &cgoBoxRuntime{config: config}
	info, err := runtime.EnsureSession(ctx, session, vmState, proxyState)
	if err != nil {
		t.Fatalf("EnsureSession returned error: %v", err)
	}
	vmState.BoxID = info.BoxID
	t.Cleanup(func() {
		if t.Failed() && runtimeSmokeKeepTmp() {
			return
		}
		stopCtx, stopCancel := context.WithTimeout(context.Background(), config.SessionStopTimeout)
		defer stopCancel()
		_, _ = runtime.StopSession(stopCtx, session, vmState)
	})
	assertBoxLiteRuntimeSmokeGuestPaths(t, ctx, runtime, vmState)
	assertRuntimeSmokeHomeFiles(t, ctx, runtime, session, vmState)
}

func TestSmokeBoxLiteUsesGoContainerRegistryOCIImage(t *testing.T) {
	runtimeSmokeEnabled(t, RuntimeDriverBoxlite)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	config := newRuntimeSmokeConfig(t, RuntimeDriverBoxlite)
	config.BoxRootfsPath = ""
	config.DefaultImage = prepareRuntimeSmokeGoContainerRegistryOCIImage(t, ctx, config)
	session, vmState, proxyState := newRuntimeSmokeSession(t, ctx, config, RuntimeDriverBoxlite)
	assertDirectoryOnlyRuntimeSmokeManifest(t, session, RuntimeDriverBoxlite)

	runtime := &cgoBoxRuntime{config: config}
	info, err := runtime.EnsureSession(ctx, session, vmState, proxyState)
	if err != nil {
		t.Fatalf("EnsureSession returned error: %v", err)
	}
	vmState.BoxID = info.BoxID
	t.Cleanup(func() {
		if t.Failed() && runtimeSmokeKeepTmp() {
			return
		}
		stopCtx, stopCancel := context.WithTimeout(context.Background(), config.SessionStopTimeout)
		defer stopCancel()
		_, _ = runtime.StopSession(stopCtx, session, vmState)
	})
	assertBoxLiteRuntimeSmokeGuestPaths(t, ctx, runtime, vmState)
	assertRuntimeSmokeHomeFiles(t, ctx, runtime, session, vmState)
}

func assertBoxLiteRuntimeSmokeGuestPaths(t *testing.T, ctx context.Context, runtime *cgoBoxRuntime, vmState VMState) {
	t.Helper()
	box, err := runtime.getBox(ctx, vmState.BoxID)
	if err != nil {
		t.Fatalf("getBox returned error: %v", err)
	}
	defer box.free()
	script := `
set -eu
mountpoint -q /root
test ! -L /root
test "$(stat -c '%d:%i' /root)" = "$(stat -c '%d:%i' /data/home)"
test -f /root/.codex/config.toml
test -f /root/.gitconfig
test -f /root/.claude.json
test "$(readlink /workspace)" = "/data/workspace"
cd /workspace
printf ok > /root/.agent-compose-smoke-home
printf ok > /data/state/runtime-mount-smoke.txt
`
	result, err := runtime.executeBox(ctx, box, ExecSpec{Command: "sh", Args: []string{"-lc", script}, Cwd: "/"}, nil)
	if err != nil {
		t.Fatalf("boxlite guest path assertion command returned error: %v", err)
	}
	if !result.Success {
		t.Fatalf("boxlite guest path assertion failed: exit=%d stdout=%q stderr=%q", result.ExitCode, result.Stdout, result.Stderr)
	}
}
