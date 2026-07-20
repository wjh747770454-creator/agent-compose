package tui

import (
	"strings"
	"sync"

	"github.com/charmbracelet/x/ansi"
)

const maxPendingCommandOutput = 4096

type commandOutputMessage string

type commandOutputWriter struct {
	mu      sync.Mutex
	pending string
	send    func(string)
}

func newCommandOutputWriter(send func(string)) *commandOutputWriter {
	return &commandOutputWriter{send: send}
}

func (w *commandOutputWriter) Write(data []byte) (int, error) {
	w.mu.Lock()
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	w.pending += text
	parts := strings.Split(w.pending, "\n")
	w.pending = parts[len(parts)-1]
	complete := append([]string(nil), parts[:len(parts)-1]...)
	if len(w.pending) >= maxPendingCommandOutput {
		complete = append(complete, w.pending)
		w.pending = ""
	}
	w.mu.Unlock()
	w.emit(complete)
	return len(data), nil
}

func (w *commandOutputWriter) Flush() {
	w.mu.Lock()
	pending := w.pending
	w.pending = ""
	w.mu.Unlock()
	w.emit([]string{pending})
}

func (w *commandOutputWriter) emit(lines []string) {
	for _, line := range lines {
		line = strings.TrimSpace(ansi.Strip(line))
		if line != "" && w.send != nil {
			w.send(line)
		}
	}
}
