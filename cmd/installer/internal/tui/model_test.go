package tui

import (
	"strings"
	"testing"

	"agent-compose/cmd/installer/internal/core"
	tea "github.com/charmbracelet/bubbletea"
)

func TestModelSelectsLanguageAndInstallFlow(t *testing.T) {
	m := newModel(core.Service{}, core.DefaultOptions(), "/tmp/installer")
	press(t, m, "down")
	press(t, m, "enter")
	if m.language != english || m.screen != screenAction {
		t.Fatalf("language screen = %q, %d", m.language, m.screen)
	}
	press(t, m, "enter")
	if m.operation != core.OperationInstall || m.screen != screenForm || len(m.inputs) != 4 {
		t.Fatalf("install form = %q, %d, %d inputs", m.operation, m.screen, len(m.inputs))
	}
	m.inputs[0].SetValue("relative")
	press(t, m, "enter")
	if m.err == nil || !strings.Contains(m.View(), "absolute") {
		t.Fatalf("expected path validation in view: %v\n%s", m.err, m.View())
	}
	m.inputs[0].SetValue("/opt/agent-compose")
	press(t, m, "enter")
	if m.screen != screenConfirm {
		t.Fatalf("screen = %d, want confirmation", m.screen)
	}
	if !strings.Contains(m.View(), "/opt/agent-compose") {
		t.Fatalf("confirmation missing directory:\n%s", m.View())
	}
}

func TestModelUninstallPurgeChoice(t *testing.T) {
	m := newModel(core.Service{}, core.DefaultOptions(), "/tmp/installer")
	press(t, m, "enter")
	press(t, m, "down")
	press(t, m, "down")
	press(t, m, "enter")
	if m.operation != core.OperationUninstall || len(m.inputs) != 1 {
		t.Fatalf("uninstall form = %q, %d inputs", m.operation, len(m.inputs))
	}
	press(t, m, "enter")
	if m.screen != screenPurge {
		t.Fatalf("screen = %d, want purge", m.screen)
	}
	press(t, m, "down")
	press(t, m, "enter")
	if !m.options.Purge || m.screen != screenConfirm {
		t.Fatalf("purge = %t, screen = %d", m.options.Purge, m.screen)
	}
}

func TestModelRendersProgressAndResults(t *testing.T) {
	m := newModel(core.Service{}, core.DefaultOptions(), "/tmp/installer")
	t.Cleanup(m.cancel)
	m.screen = screenRunning
	updated, _ := m.Update(eventMessage(core.Event{Message: "Pulling images"}))
	m = updated.(*model)
	if !strings.Contains(m.View(), "Pulling images") {
		t.Fatalf("progress missing:\n%s", m.View())
	}
	updated, _ = m.Update(operationResult{result: core.Result{URL: "http://localhost:80", Username: "admin", GeneratedPassword: "secret"}})
	m = updated.(*model)
	if !strings.Contains(m.View(), "http://localhost:80") || !strings.Contains(m.View(), "secret") {
		t.Fatalf("result missing:\n%s", m.View())
	}
}

func TestModelCancelsRunningOperationBeforeExit(t *testing.T) {
	m := newModel(core.Service{}, core.DefaultOptions(), "/tmp/installer")
	m.screen = screenRunning
	updated, command := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if updated != m || command != nil {
		t.Fatal("running cancellation unexpectedly quit the TUI")
	}
	select {
	case <-m.ctx.Done():
	default:
		t.Fatal("running operation context was not cancelled")
	}
	if !strings.Contains(m.View(), "回滚") {
		t.Fatalf("cancellation state missing from view:\n%s", m.View())
	}
}

func press(t *testing.T, m *model, key string) {
	t.Helper()
	keyType := tea.KeyRunes
	runes := []rune(key)
	switch key {
	case "enter":
		keyType, runes = tea.KeyEnter, nil
	case "up":
		keyType, runes = tea.KeyUp, nil
	case "down":
		keyType, runes = tea.KeyDown, nil
	}
	updated, _ := m.Update(tea.KeyMsg{Type: keyType, Runes: runes})
	if updated != m {
		t.Fatal("model pointer changed unexpectedly")
	}
}
