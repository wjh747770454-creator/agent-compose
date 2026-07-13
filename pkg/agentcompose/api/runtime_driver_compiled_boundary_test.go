package api

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"connectrpc.com/connect"

	appconfig "agent-compose/pkg/config"
	driverpkg "agent-compose/pkg/driver"
	"agent-compose/pkg/execution"
	"agent-compose/pkg/identity"
	domain "agent-compose/pkg/model"
	agentcomposev1 "agent-compose/proto/agentcompose/v1"
	agentcomposev2 "agent-compose/proto/agentcompose/v2"
)

func TestRuntimeDriverNotCompiledConnectBoundaries(t *testing.T) {
	unsupported := domain.ClassifyError(domain.ErrUnsupported, "", driverpkg.ErrRuntimeDriverNotCompiled)
	mapped := ConnectErrorForDomain(unsupported)
	if connect.CodeOf(mapped) != connect.CodeUnimplemented || !errors.Is(mapped, domain.ErrUnsupported) || !errors.Is(mapped, driverpkg.ErrRuntimeDriverNotCompiled) {
		t.Fatalf("ConnectErrorForDomain error = %v, code=%v; want unimplemented with both sentinels", mapped, connect.CodeOf(mapped))
	}

	t.Run("sandbox remove", func(t *testing.T) {
		sandboxID := identity.NewID(identity.ResourceSandbox, "runtime-capability", "remove")
		store := &characterizationSandboxStore{session: &domain.Sandbox{Summary: domain.SandboxSummary{
			ID: sandboxID, Driver: driverpkg.RuntimeDriverMicrosandbox, VMStatus: domain.VMStatusStopped, RuntimeRef: "original-runtime-ref",
		}}}
		remover := &characterizationSandboxRemover{err: unsupported}
		handler := NewSandboxHandler(&characterizationSessionDelegate{}, store, remover, nil)

		_, err := handler.RemoveSandbox(context.Background(), connect.NewRequest(&agentcomposev2.RemoveSandboxRequest{SandboxId: sandboxID}))
		if connect.CodeOf(err) != connect.CodeUnimplemented || !errors.Is(err, driverpkg.ErrRuntimeDriverNotCompiled) {
			t.Fatalf("RemoveSandbox error = %v, code=%v; want unimplemented typed error", err, connect.CodeOf(err))
		}
		if store.removeID != "" || store.session.Summary.Driver != driverpkg.RuntimeDriverMicrosandbox || store.session.Summary.RuntimeRef != "original-runtime-ref" {
			t.Fatalf("unsupported remove mutated metadata: store=%#v session=%#v", store, store.session)
		}
	})

	t.Run("exec unary and attach", func(t *testing.T) {
		root := t.TempDir()
		sandbox := &domain.Sandbox{Summary: domain.SandboxSummary{
			ID: "sandbox-history", Driver: driverpkg.RuntimeDriverMicrosandbox, VMStatus: domain.VMStatusRunning,
			RuntimeRef: "original-runtime-ref", WorkspacePath: filepath.Join(root, "workspace"),
		}}
		vmState := domain.VMState{Driver: driverpkg.RuntimeDriverMicrosandbox, BoxID: "original-box"}
		store := &apiExecSandboxStore{sandbox: sandbox, vm: vmState}
		handler := NewExecHandler(&appconfig.Config{}, store, apiExecProjectStore{}, func(*domain.Sandbox) (ExecRuntime, error) {
			return nil, unsupported
		})
		req := &agentcomposev2.ExecRequest{
			Target:  &agentcomposev2.ExecRequest_SandboxId{SandboxId: sandbox.Summary.ID},
			Command: &agentcomposev2.ExecCommand{Command: "echo", Args: []string{"history"}},
		}

		_, err := handler.Exec(context.Background(), connect.NewRequest(req))
		if connect.CodeOf(err) != connect.CodeUnimplemented || !errors.Is(err, driverpkg.ErrRuntimeDriverNotCompiled) {
			t.Fatalf("Exec error = %v, code=%v; want unimplemented typed error", err, connect.CodeOf(err))
		}
		_, err = handler.prepareExecAttach(context.Background(), &agentcomposev2.ExecAttachStart{Request: req})
		if connect.CodeOf(err) != connect.CodeUnimplemented || !errors.Is(err, driverpkg.ErrRuntimeDriverNotCompiled) {
			t.Fatalf("prepareExecAttach error = %v, code=%v; want unimplemented typed error", err, connect.CodeOf(err))
		}
		if sandbox.Summary.Driver != driverpkg.RuntimeDriverMicrosandbox || sandbox.Summary.RuntimeRef != "original-runtime-ref" || store.vm != vmState {
			t.Fatalf("unsupported exec mutated state: sandbox=%#v vm=%#v", sandbox, store.vm)
		}
		if _, err := os.Stat(filepath.Join(root, "state", "exec")); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("unsupported exec created artifacts or stat failed: %v", err)
		}
	})

	t.Run("kernel and agent", func(t *testing.T) {
		session := &domain.Sandbox{Summary: domain.SandboxSummary{ID: "sandbox-history", VMStatus: domain.VMStatusRunning}}
		store := unsupportedRuntimeAPIStore{session: session}
		kernel := NewKernelHandler(store, unsupportedCellExecutor{err: unsupported}, nil)
		_, err := kernel.ExecuteCell(context.Background(), connect.NewRequest(&agentcomposev1.ExecuteCellRequest{
			SessionId: session.Summary.ID,
			Type:      agentcomposev1.CellType_CELL_TYPE_SHELL,
			Source:    "echo history",
		}))
		if connect.CodeOf(err) != connect.CodeUnimplemented {
			t.Fatalf("ExecuteCell error = %v, code=%v; want unimplemented", err, connect.CodeOf(err))
		}
		agent := NewAgentHandler(store, nil, unsupportedAgentExecutor{err: unsupported}, nil)
		_, err = agent.SendAgentMessage(context.Background(), connect.NewRequest(&agentcomposev1.SendAgentMessageRequest{
			SessionId: session.Summary.ID,
			Message:   "hello",
		}))
		if connect.CodeOf(err) != connect.CodeUnimplemented {
			t.Fatalf("SendAgentMessage error = %v, code=%v; want unimplemented", err, connect.CodeOf(err))
		}
	})
}

type unsupportedRuntimeAPIStore struct {
	session *domain.Sandbox
}

func (s unsupportedRuntimeAPIStore) GetSandbox(context.Context, string) (*domain.Sandbox, error) {
	return s.session, nil
}

func (unsupportedRuntimeAPIStore) ListCells(context.Context, string) ([]domain.NotebookCell, error) {
	return nil, nil
}

func (unsupportedRuntimeAPIStore) ListEvents(context.Context, string) ([]domain.SandboxEvent, error) {
	return nil, nil
}

type unsupportedCellExecutor struct {
	err error
}

func (e unsupportedCellExecutor) ExecuteCell(context.Context, *domain.Sandbox, string, string) (domain.NotebookCell, error) {
	return domain.NotebookCell{}, e.err
}

func (e unsupportedCellExecutor) ExecuteCellStream(context.Context, *domain.Sandbox, string, string, execution.CellExecutionStream) (domain.NotebookCell, error) {
	return domain.NotebookCell{}, e.err
}

type unsupportedAgentExecutor struct {
	err error
}

func (e unsupportedAgentExecutor) ExecuteAgentRequest(context.Context, *domain.Sandbox, execution.ExecuteAgentRequest) (domain.NotebookCell, domain.SandboxEvent, domain.SandboxEvent, error) {
	return domain.NotebookCell{}, domain.SandboxEvent{}, domain.SandboxEvent{}, e.err
}
