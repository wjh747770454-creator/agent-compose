package core

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestEnvFileUsesAndUpdatesLastActiveAssignment(t *testing.T) {
	file := parseEnvFile([]byte("KEY=old\n# KEY=ignored\nexport KEY =current\n"))
	if got, ok := file.Get("KEY"); !ok || got != "current" {
		t.Fatalf("Get(KEY) = %q, %v", got, ok)
	}
	if err := file.Set("KEY", "next"); err != nil {
		t.Fatal(err)
	}
	got := string(file.Bytes())
	if !strings.Contains(got, "KEY=old\n") || !strings.Contains(got, "export KEY =next\n") {
		t.Fatalf("unexpected environment output:\n%s", got)
	}
}

func TestOptionsValidation(t *testing.T) {
	options := DefaultOptions()
	if err := options.Validate(OperationInstall); err != nil {
		t.Fatal(err)
	}
	options.Port = 65536
	if err := options.Validate(OperationInstall); err == nil {
		t.Fatal("expected invalid port")
	}
	if _, err := ParsePort("0"); err == nil {
		t.Fatal("expected ParsePort to reject zero")
	}
}

func TestValidateInstallPathRejectsFilesystemRoot(t *testing.T) {
	if _, err := validateInstallPath(string(filepath.Separator)); err == nil {
		t.Fatal("expected filesystem root to be rejected")
	}
}
