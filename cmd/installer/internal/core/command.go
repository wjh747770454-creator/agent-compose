package core

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

type CommandRunner interface {
	Run(context.Context, string, string, ...string) error
}

type ExecRunner struct {
	Output io.Writer
}

func (r ExecRunner) Run(ctx context.Context, dir, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.Stdout = r.Output
	cmd.Stderr = r.Output
	cmd.Env = filteredComposeEnvironment(os.Environ())
	runErr := cmd.Run()
	if flusher, ok := r.Output.(interface{ Flush() }); ok {
		flusher.Flush()
	}
	if runErr != nil {
		return fmt.Errorf("run %s %s: %w", name, strings.Join(args, " "), runErr)
	}
	return nil
}

func filteredComposeEnvironment(environment []string) []string {
	blocked := map[string]bool{
		"COMPOSE_FILE": true, "COMPOSE_PATH_SEPARATOR": true,
		"COMPOSE_ENV_FILES": true, "COMPOSE_DISABLE_ENV_FILE": true,
		"COMPOSE_PROFILES": true, "COMPOSE_PROJECT_NAME": true,
	}
	result := make([]string, 0, len(environment))
	for _, item := range environment {
		key, _, _ := strings.Cut(item, "=")
		if !blocked[key] {
			result = append(result, item)
		}
	}
	return result
}
