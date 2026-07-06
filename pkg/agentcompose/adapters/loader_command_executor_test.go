package adapters

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	appconfig "agent-compose/pkg/config"
	driverpkg "agent-compose/pkg/driver"
	"agent-compose/pkg/execution"
	domain "agent-compose/pkg/model"
	"agent-compose/pkg/sessions"
	"agent-compose/pkg/storage/sessionstore"
)

type fakeLoaderCommandRuntime struct{}

func (r fakeLoaderCommandRuntime) EnsureSession(context.Context, *domain.Session, domain.VMState, domain.ProxyState) (domain.SessionVMInfo, error) {
	return domain.SessionVMInfo{}, nil
}

func (r fakeLoaderCommandRuntime) StopSession(context.Context, *domain.Session, domain.VMState) (bool, error) {
	return false, nil
}

func (r fakeLoaderCommandRuntime) Exec(context.Context, *domain.Session, domain.VMState, domain.ExecSpec) (domain.ExecResult, error) {
	return domain.ExecResult{}, nil
}

func (r fakeLoaderCommandRuntime) ExecStream(_ context.Context, _ *domain.Session, _ domain.VMState, _ domain.ExecSpec, stream domain.ExecStreamWriter) (domain.ExecResult, error) {
	commandResult := domain.RuntimeCommandResult{
		Stdout:   "loader stdout\n",
		Stderr:   "loader stderr\n",
		Output:   "loader stdout\nloader stderr\n",
		ExitCode: 0,
		Success:  true,
	}
	payloadBytes, _ := json.Marshal(commandResult)
	payload := execution.CommandResultPrefix + string(payloadBytes) + "\n"
	if stream != nil {
		stream(domain.ExecChunk{Text: "loader stdout\n"})
		stream(domain.ExecChunk{Text: "loader stderr\n", Stream: domain.StdioStderr})
		stream(domain.ExecChunk{Text: payload})
	}
	return domain.ExecResult{
		Stdout:   "loader stdout\n" + payload,
		Stderr:   "loader stderr\n",
		Output:   "loader stdout\nloader stderr\n" + payload,
		ExitCode: 0,
		Success:  true,
	}, nil
}

func TestLoaderCommandExecutorFiltersCommandPayloadFromStreamingCellOutput(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	config := &appconfig.Config{
		DataRoot:             root,
		SessionRoot:          filepath.Join(root, "sessions"),
		RuntimeDriver:        driverpkg.RuntimeDriverBoxlite,
		DefaultImage:         "guest:latest",
		GuestWorkspacePath:   "/workspace",
		GuestStateRoot:       "/data/state",
		GuestHomePath:        "/root",
		JupyterProxyBasePath: "/agent-compose/session",
		SessionStartTimeout:  2 * time.Second,
	}
	store, err := sessionstore.NewWithConfig(config)
	if err != nil {
		t.Fatalf("NewWithConfig returned error: %v", err)
	}
	session, err := store.CreateSession(ctx, "loader command session", "", driverpkg.RuntimeDriverBoxlite, "guest:latest", "", domain.SessionTypeScript, nil, nil, nil)
	if err != nil {
		t.Fatalf("CreateSession returned error: %v", err)
	}
	session.Summary.VMStatus = domain.VMStatusRunning
	if err := store.UpdateSession(ctx, session); err != nil {
		t.Fatalf("UpdateSession returned error: %v", err)
	}
	streams := sessions.NewStreamBrokerForTest()
	ch, unsubscribe := streams.Subscribe(session.Summary.ID)
	defer unsubscribe()
	executor := NewLoaderCommandExecutor(config, store, nil, fakeRuntimeProvider{runtime: fakeLoaderCommandRuntime{}}, streams)

	result, err := executor.ExecuteLoaderCommand(ctx, session, domain.LoaderCommandRequest{
		Mode:   "shell",
		Script: "echo loader",
	})
	if err != nil {
		t.Fatalf("ExecuteLoaderCommand returned error: %v", err)
	}
	if !result.Success || result.Stdout != "loader stdout\n" || result.Stderr != "loader stderr\n" {
		t.Fatalf("loader result = %#v", result)
	}

	var outputText strings.Builder
	for {
		select {
		case event := <-ch:
			if event.EventType == sessions.WatchEventTypeCellOutput {
				outputText.WriteString(event.Chunk)
				if strings.Contains(event.Chunk, execution.CommandResultPrefix) {
					t.Fatalf("stream event leaked command payload: %#v", event)
				}
			}
		default:
			goto drained
		}
	}

drained:
	if got := outputText.String(); !strings.Contains(got, "loader stdout\n") || !strings.Contains(got, "loader stderr\n") {
		t.Fatalf("stream output = %q", got)
	}
	cells, err := store.ListCells(ctx, session.Summary.ID)
	if err != nil {
		t.Fatalf("ListCells returned error: %v", err)
	}
	if len(cells) == 0 {
		t.Fatalf("no cells stored")
	}
	for _, cell := range cells {
		if strings.Contains(cell.Output, execution.CommandResultPrefix) {
			t.Fatalf("cell leaked command payload: %#v", cell)
		}
	}
}
