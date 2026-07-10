package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

type composeEnvContractFile struct {
	Services map[string]composeEnvContractService `yaml:"services"`
}

type composeEnvContractService struct {
	Image       string            `yaml:"image"`
	Environment map[string]string `yaml:"environment"`
	Volumes     []string          `yaml:"volumes"`
	WorkingDir  string            `yaml:"working_dir"`
}

func TestE2EDockerComposeSandboxEnvContract(t *testing.T) {
	root := repoRootForComposeEnvTest(t)
	composeData := readRepoFileForComposeEnvTest(t, root, "docker-compose.yml")
	envExample := string(readRepoFileForComposeEnvTest(t, root, ".env.example"))
	dockerfile := string(readRepoFileForComposeEnvTest(t, root, "Dockerfile"))

	var composeFile composeEnvContractFile
	if err := yaml.Unmarshal(composeData, &composeFile); err != nil {
		t.Fatalf("parse docker-compose.yml: %v", err)
	}
	service, ok := composeFile.Services["agent-compose"]
	if !ok {
		t.Fatalf("docker-compose.yml missing agent-compose service")
	}
	if !strings.Contains(service.Image, "AGENT_COMPOSE_IMAGE") || !strings.Contains(service.Image, "ghcr.io/chaitin/agent-compose:latest") {
		t.Fatalf("agent-compose image should stay deployable from published image, got %q", service.Image)
	}
	if hostRoot, ok := service.Environment["DOCKER_HOST_SANDBOX_ROOT"]; ok {
		t.Fatalf("compose must leave DOCKER_HOST_SANDBOX_ROOT unset for mount auto-detection, got %q", hostRoot)
	}
	for key := range service.Environment {
		if strings.Contains(key, "SESSION_ROOT") {
			t.Fatalf("compose service still exposes legacy session root env %q", key)
		}
	}
	for _, volume := range []string{"./data:/data", "./.env:/data/work/.env:ro"} {
		if !stringSliceContains(service.Volumes, volume) {
			t.Fatalf("agent-compose volumes = %#v, want %q", service.Volumes, volume)
		}
	}
	if service.WorkingDir != "/data/work" {
		t.Fatalf("agent-compose working_dir = %q, want /data/work", service.WorkingDir)
	}

	for _, want := range []string{
		"# SANDBOX_ROOT=/data/sandboxes",
		"# DOCKER_HOST_SANDBOX_ROOT=/absolute/host/path/to/data/sandboxes",
		"SESSION_ROOT, DOCKER_HOST_SESSION_ROOT, SESSION_START_TIMEOUT, and",
	} {
		if !strings.Contains(envExample, want) {
			t.Fatalf(".env.example missing %q", want)
		}
	}
	legacyAssignment := regexp.MustCompile(`(?m)^\s*(SESSION_ROOT|DOCKER_HOST_SESSION_ROOT|SESSION_START_TIMEOUT|SESSION_STOP_TIMEOUT)=`)
	if legacyAssignment.MatchString(envExample) {
		t.Fatalf(".env.example must not provide copyable legacy session env defaults")
	}
	for _, legacy := range []string{"SESSION_ROOT", "DOCKER_HOST_SESSION_ROOT"} {
		if strings.Contains(string(composeData), legacy) {
			t.Fatalf("docker-compose.yml contains legacy env %s", legacy)
		}
	}
	if strings.Contains(dockerfile, "ENV SANDBOX_ROOT=") {
		t.Fatalf("Dockerfile must leave SANDBOX_ROOT unset for legacy root detection")
	}
	if strings.Contains(dockerfile, "ENV SESSION_ROOT=") {
		t.Fatalf("Dockerfile still defines legacy SESSION_ROOT")
	}
}

func repoRootForComposeEnvTest(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(wd, "go.mod")); err == nil {
			return wd
		}
		parent := filepath.Dir(wd)
		if parent == wd {
			t.Fatalf("could not locate repo root from test working directory")
		}
		wd = parent
	}
}

func readRepoFileForComposeEnvTest(t *testing.T, root, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return data
}
