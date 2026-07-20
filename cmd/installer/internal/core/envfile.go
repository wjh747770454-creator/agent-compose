package core

import (
	"fmt"
	"strings"
)

type envFile struct {
	lines []string
}

func parseEnvFile(data []byte) *envFile {
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	text = strings.TrimSuffix(text, "\n")
	if text == "" {
		return &envFile{}
	}
	return &envFile{lines: strings.Split(text, "\n")}
}

func (f *envFile) Get(key string) (string, bool) {
	var result string
	found := false
	for _, line := range f.lines {
		candidate, value, ok := parseAssignment(line)
		if ok && candidate == key {
			result, found = value, true
		}
	}
	return result, found
}

func (f *envFile) Set(key, value string) error {
	if strings.ContainsAny(key+value, "\r\n") || key == "" || strings.Contains(key, "=") {
		return fmt.Errorf("invalid environment assignment for %q", key)
	}
	last := -1
	for i, line := range f.lines {
		candidate, _, ok := parseAssignment(line)
		if ok && candidate == key {
			last = i
		}
	}
	if last < 0 {
		f.lines = append(f.lines, key+"="+value)
		return nil
	}
	equals := strings.Index(f.lines[last], "=")
	f.lines[last] = f.lines[last][:equals+1] + value
	return nil
}

func (f *envFile) Bytes() []byte {
	return []byte(strings.Join(f.lines, "\n") + "\n")
}

func parseAssignment(line string) (string, string, bool) {
	trimmed := strings.TrimLeft(line, " \t")
	if strings.HasPrefix(trimmed, "#") || trimmed == "" {
		return "", "", false
	}
	if strings.HasPrefix(trimmed, "export ") || strings.HasPrefix(trimmed, "export\t") {
		trimmed = strings.TrimLeft(strings.TrimPrefix(trimmed, "export"), " \t")
	}
	equals := strings.Index(trimmed, "=")
	if equals <= 0 {
		return "", "", false
	}
	key := strings.TrimSpace(trimmed[:equals])
	if key == "" || strings.ContainsAny(key, " \t") {
		return "", "", false
	}
	return key, strings.TrimSuffix(trimmed[equals+1:], "\r"), true
}
