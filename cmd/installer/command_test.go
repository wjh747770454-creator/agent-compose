package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestRootHelpDocumentsOperationsAndDefaultDirectory(t *testing.T) {
	var output bytes.Buffer
	command := newRootCommand(&output, &output)
	command.SetArgs([]string{"--help"})
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"install", "upgrade", "uninstall", "/opt/agent-compose"} {
		if !strings.Contains(output.String(), expected) {
			t.Fatalf("help missing %q:\n%s", expected, output.String())
		}
	}
}

func TestRootRejectsInteractiveModeWithoutTTY(t *testing.T) {
	command := newRootCommand(&bytes.Buffer{}, &bytes.Buffer{})
	command.SetArgs(nil)
	if err := command.Execute(); err == nil || !strings.Contains(err.Error(), "requires a TTY") {
		t.Fatalf("Execute error = %v", err)
	}
}

func TestTruthy(t *testing.T) {
	if !truthy("true") || !truthy("1") || !truthy("yes") || truthy("") {
		t.Fatal("unexpected truthy parsing")
	}
}
