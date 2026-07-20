package tui

import (
	"strings"
	"sync"
	"testing"
)

func TestCommandOutputWriterNormalizesTerminalOutput(t *testing.T) {
	var mu sync.Mutex
	var lines []string
	writer := newCommandOutputWriter(func(line string) {
		mu.Lock()
		defer mu.Unlock()
		lines = append(lines, line)
	})

	chunks := []string{"Pull", "ing 10%\rPulling 20%\r", "\x1b[32mDone\x1b[0m\npartial"}
	for _, chunk := range chunks {
		if _, err := writer.Write([]byte(chunk)); err != nil {
			t.Fatal(err)
		}
	}
	writer.Flush()

	want := []string{"Pulling 10%", "Pulling 20%", "Done", "partial"}
	if strings.Join(lines, "|") != strings.Join(want, "|") {
		t.Fatalf("lines = %#v, want %#v", lines, want)
	}
}

func TestCommandOutputWriterFlushesBoundedPartialLine(t *testing.T) {
	var lines []string
	writer := newCommandOutputWriter(func(line string) { lines = append(lines, line) })
	if _, err := writer.Write([]byte(strings.Repeat("x", maxPendingCommandOutput))); err != nil {
		t.Fatal(err)
	}
	if len(lines) != 1 || len(lines[0]) != maxPendingCommandOutput {
		t.Fatalf("bounded output lines = %#v", lines)
	}
}
