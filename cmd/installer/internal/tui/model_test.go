package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/chaitin/agent-compose/cmd/installer/internal/core"
	tea "github.com/charmbracelet/bubbletea"
)

func TestModelSelectsLanguageAndInstallFlow(t *testing.T) {
	m := newModel(core.Service{}, core.DefaultOptions(), "/tmp/installer")
	if view := m.View(); !strings.Contains(view, "DECLARATIVE AGENT RUNTIME") || !strings.Contains(view, "|___/") {
		t.Fatalf("wide TUI is missing product banner:\n%s", view)
	}
	press(t, m, "down")
	press(t, m, "enter")
	if m.language != english || m.screen != screenAction {
		t.Fatalf("language screen = %q, %d", m.language, m.screen)
	}
	press(t, m, "enter")
	if m.operation != core.OperationInstall || m.screen != screenForm || len(m.inputs) != 3 {
		t.Fatalf("install form = %q, %d, %d inputs", m.operation, m.screen, len(m.inputs))
	}
	form := m.View()
	for _, expected := range []string{"Configure installation", "Install directory", "Application version", "Web UI port", "╭", "Tab / ↑↓ move"} {
		if !strings.Contains(form, expected) {
			t.Fatalf("install form missing %q:\n%s", expected, form)
		}
	}
	if strings.Contains(form, "Image prefix") {
		t.Fatalf("advanced image prefix rendered in TUI:\n%s", form)
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
	confirmation := m.View()
	for _, expected := range []string{"/opt/agent-compose", "latest", "80"} {
		if !strings.Contains(confirmation, expected) {
			t.Fatalf("confirmation missing %q:\n%s", expected, confirmation)
		}
	}
}

func TestModelUsesCompactBrandOnNarrowTerminal(t *testing.T) {
	m := newModel(core.Service{}, core.DefaultOptions(), "/tmp/installer")
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 60, Height: 24})
	m = updated.(*model)
	view := m.View()
	if !strings.Contains(view, "agent-compose :: installer") {
		t.Fatalf("compact brand missing:\n%s", view)
	}
	if strings.Contains(view, "DECLARATIVE AGENT RUNTIME") {
		t.Fatalf("wide tagline rendered on narrow terminal:\n%s", view)
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
	updated, _ = m.Update(commandOutputMessage("layer downloaded"))
	m = updated.(*model)
	if !strings.Contains(m.View(), "layer downloaded") {
		t.Fatalf("command output missing:\n%s", m.View())
	}
	updated, _ = m.Update(operationResult{result: core.Result{URL: "http://localhost:80", Username: "admin", GeneratedPassword: "secret"}})
	m = updated.(*model)
	if !strings.Contains(m.View(), "http://localhost:80") || !strings.Contains(m.View(), "secret") {
		t.Fatalf("result missing:\n%s", m.View())
	}
}

func TestModelBoundsVisibleCommandOutput(t *testing.T) {
	m := newModel(core.Service{}, core.DefaultOptions(), "/tmp/installer")
	m.screen = screenRunning
	m.width, m.height = 120, 20
	for i := range 20 {
		m.appendEvent(fmt.Sprintf("line-%02d", i))
	}
	view := m.View()
	if strings.Contains(view, "line-00") || !strings.Contains(view, "line-19") {
		t.Fatalf("visible output was not bounded to its tail:\n%s", view)
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

	// Cancellation is not instant, so impatient repeats must not stack lines.
	for range 5 {
		m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	}
	if count := strings.Count(m.View(), "正在取消并回滚"); count != 1 {
		t.Fatalf("cancellation notice rendered %d times:\n%s", count, m.View())
	}
}

func TestModelIgnoresKeysWhileOperationIsRunning(t *testing.T) {
	m := newModel(core.Service{}, core.DefaultOptions(), "/tmp/installer")
	m.screen = screenConfirm
	m.cursor = 0

	updated, command := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if updated != m || command == nil || m.screen != screenRunning {
		t.Fatalf("confirmation did not start operation: screen=%d command=%v", m.screen, command)
	}

	for _, key := range []tea.KeyMsg{
		{Type: tea.KeyEnter},
		{Type: tea.KeyUp},
		{Type: tea.KeyDown},
		{Type: tea.KeyRunes, Runes: []rune("x")},
	} {
		updated, command = m.Update(key)
		if updated != m || command != nil {
			t.Fatalf("running key %q started another command", key.String())
		}
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
