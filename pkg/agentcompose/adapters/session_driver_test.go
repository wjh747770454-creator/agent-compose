package adapters

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	appconfig "agent-compose/pkg/config"
	driverpkg "agent-compose/pkg/driver"
	domain "agent-compose/pkg/model"
	"agent-compose/pkg/storage/sessionstore"
)

type fakeSessionRuntime struct {
	info domain.SessionVMInfo
}

func (r fakeSessionRuntime) EnsureSession(context.Context, *domain.Session, domain.VMState, domain.ProxyState) (domain.SessionVMInfo, error) {
	return r.info, nil
}

func (r fakeSessionRuntime) StopSession(context.Context, *domain.Session, domain.VMState) (bool, error) {
	return false, nil
}

func (r fakeSessionRuntime) Exec(context.Context, *domain.Session, domain.VMState, domain.ExecSpec) (domain.ExecResult, error) {
	return domain.ExecResult{}, nil
}

func (r fakeSessionRuntime) ExecStream(context.Context, *domain.Session, domain.VMState, domain.ExecSpec, domain.ExecStreamWriter) (domain.ExecResult, error) {
	return domain.ExecResult{}, nil
}

type fakeStopDeadlineRuntime struct {
	remaining time.Duration
}

func (r *fakeStopDeadlineRuntime) EnsureSession(context.Context, *domain.Session, domain.VMState, domain.ProxyState) (domain.SessionVMInfo, error) {
	return domain.SessionVMInfo{}, nil
}

func (r *fakeStopDeadlineRuntime) StopSession(ctx context.Context, _ *domain.Session, _ domain.VMState) (bool, error) {
	deadline, ok := ctx.Deadline()
	if ok {
		r.remaining = time.Until(deadline)
	}
	return false, nil
}

func (r *fakeStopDeadlineRuntime) Exec(context.Context, *domain.Session, domain.VMState, domain.ExecSpec) (domain.ExecResult, error) {
	return domain.ExecResult{}, nil
}

func (r *fakeStopDeadlineRuntime) ExecStream(context.Context, *domain.Session, domain.VMState, domain.ExecSpec, domain.ExecStreamWriter) (domain.ExecResult, error) {
	return domain.ExecResult{}, nil
}

type fakeDriverRuntime struct {
	alive bool
}

func (r fakeDriverRuntime) EnsureSession(context.Context, *driverpkg.Session, driverpkg.VMState, driverpkg.ProxyState) (driverpkg.SessionVMInfo, error) {
	return driverpkg.SessionVMInfo{}, nil
}

func (r fakeDriverRuntime) StopSession(context.Context, *driverpkg.Session, driverpkg.VMState) (bool, error) {
	return false, nil
}

func (r fakeDriverRuntime) Exec(context.Context, *driverpkg.Session, driverpkg.VMState, driverpkg.ExecSpec) (driverpkg.ExecResult, error) {
	return driverpkg.ExecResult{}, nil
}

func (r fakeDriverRuntime) ExecStream(context.Context, *driverpkg.Session, driverpkg.VMState, driverpkg.ExecSpec, driverpkg.ExecStreamWriter) (driverpkg.ExecResult, error) {
	return driverpkg.ExecResult{}, nil
}

func (r fakeDriverRuntime) IsSessionAlive(context.Context, *driverpkg.Session, driverpkg.VMState) (bool, error) {
	return r.alive, nil
}

type fakeRuntimeProvider struct {
	runtime BoxRuntime
}

func (p fakeRuntimeProvider) ForDriver(string) (BoxRuntime, error) {
	return p.runtime, nil
}

func (p fakeRuntimeProvider) ForSession(*domain.Session) (BoxRuntime, error) {
	return p.runtime, nil
}

func TestSessionDriverStartSessionVMSavesRuntimeState(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	config := &appconfig.Config{
		DataRoot:             root,
		SessionRoot:          filepath.Join(root, "sessions"),
		RuntimeDriver:        driverpkg.RuntimeDriverBoxlite,
		BoxliteHome:          filepath.Join(root, "boxlite"),
		DefaultImage:         "guest:latest",
		GuestWorkspacePath:   "/workspace",
		JupyterGuestPort:     8888,
		JupyterProxyBasePath: "/agent-compose/session",
		SessionStartTimeout:  2 * time.Second,
	}
	store, err := sessionstore.NewWithConfig(config)
	if err != nil {
		t.Fatalf("NewWithConfig returned error: %v", err)
	}
	session, err := store.CreateSession(ctx, "adapter session", "", driverpkg.RuntimeDriverBoxlite, "guest:latest", "", domain.SessionTypeManual, nil, nil, nil)
	if err != nil {
		t.Fatalf("CreateSession returned error: %v", err)
	}
	updatedProxyState := domain.ProxyState{
		ProxyPath:  session.Summary.ProxyPath,
		GuestHost:  "agent-compose-session-1",
		HostPort:   39000,
		GuestPort:  8888,
		JupyterURL: "http://127.0.0.1:39000/lab?token=secret",
		Token:      "secret",
	}
	driver := NewSessionDriver(config, store, nil, fakeRuntimeProvider{runtime: fakeSessionRuntime{info: domain.SessionVMInfo{
		BoxID:      "container-1",
		JupyterURL: updatedProxyState.JupyterURL,
		ProxyState: &updatedProxyState,
	}}})

	if err := driver.StartSessionVM(ctx, session); err != nil {
		t.Fatalf("StartSessionVM returned error: %v", err)
	}
	savedProxyState, err := store.GetProxyState(session.Summary.ID)
	if err != nil {
		t.Fatalf("GetProxyState returned error: %v", err)
	}
	if savedProxyState.GuestHost != "agent-compose-session-1" || savedProxyState.GuestPort != 8888 {
		t.Fatalf("saved proxy target = %s:%d, want agent-compose-session-1:8888", savedProxyState.GuestHost, savedProxyState.GuestPort)
	}
	vmState, err := store.GetVMState(session.Summary.ID)
	if err != nil {
		t.Fatalf("GetVMState returned error: %v", err)
	}
	if vmState.BoxID != "container-1" || vmState.BootstrapRef != updatedProxyState.JupyterURL {
		t.Fatalf("vm state = %+v, want box id and bootstrap ref from runtime", vmState)
	}
}

func TestSessionDriverStopSessionVMAddsDockerStopContextMargin(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	config := &appconfig.Config{
		DataRoot:            root,
		SessionRoot:         filepath.Join(root, "sessions"),
		RuntimeDriver:       driverpkg.RuntimeDriverDocker,
		DefaultImage:        "guest:latest",
		GuestWorkspacePath:  "/workspace",
		SessionStartTimeout: 2 * time.Second,
		SessionStopTimeout:  2 * time.Second,
	}
	store, err := sessionstore.NewWithConfig(config)
	if err != nil {
		t.Fatalf("NewWithConfig returned error: %v", err)
	}
	session, err := store.CreateSession(ctx, "adapter session", "", driverpkg.RuntimeDriverDocker, "guest:latest", "", domain.SessionTypeManual, nil, nil, nil)
	if err != nil {
		t.Fatalf("CreateSession returned error: %v", err)
	}
	runtime := &fakeStopDeadlineRuntime{}
	driver := NewSessionDriver(config, store, nil, fakeRuntimeProvider{runtime: runtime})

	if err := driver.StopSessionVM(ctx, session); err != nil {
		t.Fatalf("StopSessionVM returned error: %v", err)
	}
	if runtime.remaining <= config.SessionStopTimeout+4*time.Second {
		t.Fatalf("StopSessionVM context remaining = %s, want docker stop timeout plus API margin", runtime.remaining)
	}
	vmState, err := store.GetVMState(session.Summary.ID)
	if err != nil {
		t.Fatalf("GetVMState returned error: %v", err)
	}
	if vmState.StoppedAt.IsZero() || vmState.LastError != "" {
		t.Fatalf("vm state after stop = %+v", vmState)
	}
}
