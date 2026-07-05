package driver

import (
	appconfig "agent-compose/pkg/config"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testRuntimeMountConfig() *appconfig.Config {
	return &appconfig.Config{
		GuestWorkspacePath: "/workspace",
		GuestHomePath:      "/root",
		GuestStateRoot:     "/data/state",
		GuestRuntimeRoot:   "/data/runtime",
		GuestLogRoot:       "/data/logs",
	}
}

func testRuntimeMountSession(root string) *Session {
	return &Session{Summary: SessionSummary{
		ID:            "session-1",
		WorkspacePath: filepath.Join(root, "workspace"),
	}}
}

func TestRuntimeMountEntriesDefineSharedLogicalMountList(t *testing.T) {
	config := testRuntimeMountConfig()
	entries := runtimeMountEntries(config)
	got := map[string]logicalRuntimeMountEntry{}
	for _, entry := range entries {
		if got[entry.guestPath].guestPath != "" {
			t.Fatalf("duplicate logical guest path %s", entry.guestPath)
		}
		got[entry.guestPath] = entry
	}
	want := map[string]struct {
		sessionPath string
		isFile      bool
		exposure    directoryOnlyExposure
	}{
		"/workspace":                {sessionPath: "workspace", exposure: directoryOnlyExposureSymlink},
		"/data/state":               {sessionPath: "state", exposure: directoryOnlyExposureAlreadyInData},
		"/data/runtime":             {sessionPath: "runtime", exposure: directoryOnlyExposureAlreadyInData},
		"/data/logs":                {sessionPath: "logs", exposure: directoryOnlyExposureAlreadyInData},
		"/root/.codex":              {sessionPath: "home/.codex", exposure: directoryOnlyExposureSymlink},
		"/root/.claude":             {sessionPath: "home/.claude", exposure: directoryOnlyExposureSymlink},
		"/root/.opencode":           {sessionPath: "home/.opencode", exposure: directoryOnlyExposureSymlink},
		"/root/.claude.json":        {sessionPath: "home/.claude.json", isFile: true, exposure: directoryOnlyExposureSymlink},
		"/root/.gitconfig":          {sessionPath: "home/.gitconfig", isFile: true, exposure: directoryOnlyExposureSymlink},
		"/root/.gemini":             {sessionPath: "home/.gemini", exposure: directoryOnlyExposureSymlink},
		"/root/.config/claude":      {sessionPath: "home/.config/claude", exposure: directoryOnlyExposureSymlink},
		"/root/.config/Claude":      {sessionPath: "home/.config/Claude", exposure: directoryOnlyExposureSymlink},
		"/root/.config/gemini":      {sessionPath: "home/.config/gemini", exposure: directoryOnlyExposureSymlink},
		"/root/.config/opencode":    {sessionPath: "home/.config/opencode", exposure: directoryOnlyExposureSymlink},
		"/root/.local/share/gemini": {sessionPath: "home/.local/share/gemini", exposure: directoryOnlyExposureSymlink},
	}
	if len(got) != len(want) {
		t.Fatalf("logical mount count = %d, want %d: %#v", len(got), len(want), got)
	}
	for guestPath, wantEntry := range want {
		entry := got[guestPath]
		if entry.sessionPath != wantEntry.sessionPath || entry.isFile != wantEntry.isFile || entry.directoryOnlyExposure != wantEntry.exposure {
			t.Fatalf("logical entry %s = %#v, want sessionPath=%s isFile=%v exposure=%s", guestPath, entry, wantEntry.sessionPath, wantEntry.isFile, wantEntry.exposure)
		}
	}
}

func TestPrepareRuntimeMountManifestForDockerIncludesRequiredMountsOnly(t *testing.T) {
	root := t.TempDir()
	session := testRuntimeMountSession(root)
	manifest, err := prepareRuntimeMountManifest(testRuntimeMountConfig(), session, RuntimeDriverDocker)
	if err != nil {
		t.Fatalf("prepareRuntimeMountManifest returned error: %v", err)
	}
	if manifest.Version != runtimeMountManifestVersion {
		t.Fatalf("manifest version = %d, want %d", manifest.Version, runtimeMountManifestVersion)
	}
	if manifest.Driver != RuntimeDriverDocker {
		t.Fatalf("manifest driver = %q, want %q", manifest.Driver, RuntimeDriverDocker)
	}
	got := map[string]string{}
	for _, mount := range manifest.Mounts {
		if mount.Type != "bind" || mount.ReadOnly {
			t.Fatalf("mount = %+v, want writable bind", mount)
		}
		got[mount.GuestPath] = mount.HostPath
	}
	want := map[string]string{}
	for _, entry := range runtimeMountEntries(testRuntimeMountConfig()) {
		want[entry.guestPath] = filepath.Join(root, filepath.FromSlash(entry.sessionPath))
	}
	if len(got) != len(want) {
		t.Fatalf("manifest mount count = %d, want %d: %+v", len(got), len(want), got)
	}
	for guestPath, wantHostPath := range want {
		if got[guestPath] != wantHostPath {
			t.Fatalf("mount %s host = %q, want %q", guestPath, got[guestPath], wantHostPath)
		}
	}
	for _, forbidden := range []string{"context", "vm", "proxy", "metadata.json"} {
		for _, mount := range manifest.Mounts {
			if filepath.Base(mount.HostPath) == forbidden {
				t.Fatalf("manifest exposed forbidden host path %q", mount.HostPath)
			}
		}
	}
}

func TestPrepareRuntimeMountManifestIgnoresCustomGuestHomePath(t *testing.T) {
	root := t.TempDir()
	session := testRuntimeMountSession(root)
	config := testRuntimeMountConfig()
	config.GuestHomePath = "/home/ignored"
	manifest, err := prepareRuntimeMountManifest(config, session, RuntimeDriverDocker)
	if err != nil {
		t.Fatalf("prepareRuntimeMountManifest returned error: %v", err)
	}
	got := map[string]string{}
	for _, mount := range manifest.Mounts {
		got[mount.GuestPath] = mount.HostPath
	}
	for _, guestPath := range []string{
		"/root/.codex",
		"/root/.claude",
		"/root/.opencode",
		"/root/.claude.json",
		"/root/.gitconfig",
		"/root/.gemini",
		"/root/.config/claude",
		"/root/.config/Claude",
		"/root/.config/gemini",
		"/root/.config/opencode",
		"/root/.local/share/gemini",
	} {
		if got[guestPath] == "" {
			t.Fatalf("manifest missing fixed home mount %s: %#v", guestPath, got)
		}
	}
	for guestPath := range got {
		if strings.HasPrefix(guestPath, "/home/ignored/") {
			t.Fatalf("manifest used custom guest home path %s", guestPath)
		}
	}
}

func TestPrepareRuntimeMountManifestCreatesSourcesAndWritesFile(t *testing.T) {
	root := t.TempDir()
	session := testRuntimeMountSession(root)
	manifest, err := prepareRuntimeMountManifest(testRuntimeMountConfig(), session, RuntimeDriverDocker)
	if err != nil {
		t.Fatalf("prepareRuntimeMountManifest returned error: %v", err)
	}
	for _, mount := range manifest.Mounts {
		info, err := os.Stat(mount.HostPath)
		if err != nil {
			t.Fatalf("mount source %s was not created: %v", mount.HostPath, err)
		}
		switch filepath.Base(mount.HostPath) {
		case ".claude.json", ".gitconfig":
			if info.IsDir() {
				t.Fatalf("file mount source %s is a directory", mount.HostPath)
			}
		default:
			if !info.IsDir() {
				t.Fatalf("directory mount source %s is a file", mount.HostPath)
			}
		}
	}
	path := runtimeMountManifestPath(session)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read manifest file: %v", err)
	}
	var decoded RuntimeMountManifest
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("manifest file is not valid json: %v", err)
	}
	if decoded.Driver != RuntimeDriverDocker {
		t.Fatalf("manifest file driver = %q, want %q", decoded.Driver, RuntimeDriverDocker)
	}
	if len(decoded.Mounts) != len(manifest.Mounts) {
		t.Fatalf("manifest file mount count = %d, want %d", len(decoded.Mounts), len(manifest.Mounts))
	}
}

func TestPrepareRuntimeMountManifestForDirectoryOnlyDriversMountsSingleSessionDirectory(t *testing.T) {
	for _, driver := range []string{RuntimeDriverBoxlite, RuntimeDriverMicrosandbox} {
		t.Run(driver, func(t *testing.T) {
			root := t.TempDir()
			session := testRuntimeMountSession(root)
			manifest, err := prepareRuntimeMountManifest(testRuntimeMountConfig(), session, driver)
			if err != nil {
				t.Fatalf("prepareRuntimeMountManifest returned error: %v", err)
			}
			if manifest.Driver != driver {
				t.Fatalf("manifest driver = %q, want %q", manifest.Driver, driver)
			}
			got := map[string]string{}
			for _, mount := range manifest.Mounts {
				got[mount.GuestPath] = mount.HostPath
				info, err := os.Stat(mount.HostPath)
				if err != nil {
					t.Fatalf("stat mount source %s: %v", mount.HostPath, err)
				}
				if !info.IsDir() {
					t.Fatalf("directory-only manifest contains file source %s", mount.HostPath)
				}
			}
			want := map[string]string{directoryOnlyGuestSessionPath: root}
			if len(got) != len(want) {
				t.Fatalf("manifest mount count = %d, want %d: %+v", len(got), len(want), got)
			}
			for guestPath, wantHostPath := range want {
				if got[guestPath] != wantHostPath {
					t.Fatalf("mount %s host = %q, want %q", guestPath, got[guestPath], wantHostPath)
				}
			}
			for _, fileMount := range []string{"/root/.claude.json", "/root/.gitconfig"} {
				if got[fileMount] != "" {
					t.Fatalf("directory-only manifest contains file mount %s -> %s", fileMount, got[fileMount])
				}
			}
			for _, requiredDir := range []string{"workspace", "state", "runtime", "logs", "home"} {
				info, err := os.Stat(filepath.Join(root, requiredDir))
				if err != nil {
					t.Fatalf("expected session dir %s to exist: %v", requiredDir, err)
				}
				if !info.IsDir() {
					t.Fatalf("session path %s is not a directory", requiredDir)
				}
			}
			for _, entry := range runtimeMountEntries(testRuntimeMountConfig()) {
				if entry.isFile {
					continue
				}
				info, err := os.Stat(filepath.Join(root, filepath.FromSlash(entry.sessionPath)))
				if err != nil {
					t.Fatalf("expected logical directory source %s to exist: %v", entry.sessionPath, err)
				}
				if !info.IsDir() {
					t.Fatalf("logical source %s is not a directory", entry.sessionPath)
				}
			}
			for _, requiredFile := range []string{".claude.json", ".gitconfig"} {
				info, err := os.Stat(filepath.Join(root, "home", requiredFile))
				if err != nil {
					t.Fatalf("expected home default %s to exist: %v", requiredFile, err)
				}
				if info.IsDir() {
					t.Fatalf("home default %s is a directory", requiredFile)
				}
			}
		})
	}
}

func TestDirectoryOnlyGuestSessionBootstrapUsesDataMountRoot(t *testing.T) {
	command := directoryOnlyGuestSessionBootstrapCommand(testRuntimeMountConfig())
	for _, required := range []string{
		"test -d '/data/workspace'",
		"test -d '/data/home'",
		"ln -s '/data/workspace' '/workspace'",
		"if [ -L '/root' ]; then rm -f '/root'; mkdir -p '/root';",
		"if [ ! -d '/root' ]; then echo \"refusing to replace non-directory /root\" >&2; exit 1; fi;",
		"if [ ! -e '/root.image' ]; then mv '/root' '/root.image'; mkdir -p '/root'; fi;",
		"mount --bind '/data/home' '/root'",
		"mountpoint -q '/root'",
		"test ! -L '/root'",
		"[ \"$(stat -c '%d:%i' '/root')\" = \"$(stat -c '%d:%i' '/data/home')\" ]",
		"mv '/root' '/root.image'",
		"refusing to replace unknown mount point /root",
	} {
		if !strings.Contains(command, required) {
			t.Fatalf("bootstrap command missing %q: %s", required, command)
		}
	}
	for _, forbidden := range []string{
		"ln -s '/data/home' '/root'",
		"ln -s '/data/state' '/data/state'",
		"ln -s '/data/runtime' '/data/runtime'",
		"ln -s '/data/logs' '/data/logs'",
	} {
		if strings.Contains(command, forbidden) {
			t.Fatalf("bootstrap command contains self symlink %q: %s", forbidden, command)
		}
	}
	assertSubstringOrder(t, command, "test -d '/data/home'", "rm -f '/root'")
	assertSubstringOrder(t, command, "test -d '/data/home'", "mv '/root' '/root.image'")
	assertSubstringOrder(t, command, "mount --bind '/data/home' '/root'", "test ! -L '/root'")
	assertSubstringOrder(t, command, "mount --bind '/data/home' '/root'", "mountpoint -q '/root' ||")
}

func assertSubstringOrder(t *testing.T, text, before, after string) {
	t.Helper()
	beforeIndex := strings.Index(text, before)
	if beforeIndex < 0 {
		t.Fatalf("text missing %q: %s", before, text)
	}
	afterIndex := strings.Index(text, after)
	if afterIndex < 0 {
		t.Fatalf("text missing %q: %s", after, text)
	}
	if beforeIndex >= afterIndex {
		t.Fatalf("expected %q before %q in: %s", before, after, text)
	}
}

func TestJupyterLaunchCommandDoesNotRunDirectoryOnlyBootstrapByDefault(t *testing.T) {
	config := testRuntimeMountConfig()
	proxyState := ProxyState{Enabled: true, GuestPort: 8888, Token: "test-token"}

	dockerCommand := jupyterLaunchCommand(config, proxyState, false)
	if strings.Contains(dockerCommand, "mount --bind '/data/home' '/root'") {
		t.Fatalf("default jupyter command unexpectedly contains directory-only bootstrap: %s", dockerCommand)
	}

	directoryOnlyCommand := directoryOnlyJupyterLaunchCommand(config, proxyState, false)
	if !strings.Contains(directoryOnlyCommand, "mount --bind '/data/home' '/root'") {
		t.Fatalf("directory-only jupyter command missing bootstrap: %s", directoryOnlyCommand)
	}
}

func TestPrepareRuntimeMountManifestRegeneratesForDriverSwitch(t *testing.T) {
	root := t.TempDir()
	session := testRuntimeMountSession(root)
	config := testRuntimeMountConfig()
	if _, err := prepareRuntimeMountManifest(config, session, RuntimeDriverDocker); err != nil {
		t.Fatalf("prepare docker manifest: %v", err)
	}
	manifest, err := prepareRuntimeMountManifest(config, session, RuntimeDriverBoxlite)
	if err != nil {
		t.Fatalf("prepare boxlite manifest: %v", err)
	}
	if manifest.Driver != RuntimeDriverBoxlite {
		t.Fatalf("manifest driver = %q, want %q", manifest.Driver, RuntimeDriverBoxlite)
	}
	for _, mount := range manifest.Mounts {
		if filepath.Base(mount.HostPath) == ".claude.json" || filepath.Base(mount.HostPath) == ".gitconfig" {
			t.Fatalf("boxlite manifest reused docker file mount source %s", mount.HostPath)
		}
	}
	loaded, err := loadDirectoryRuntimeMountManifest(session, RuntimeDriverBoxlite)
	if err != nil {
		t.Fatalf("loadDirectoryRuntimeMountManifest returned error: %v", err)
	}
	if len(loaded.Mounts) != len(manifest.Mounts) {
		t.Fatalf("loaded mount count = %d, want %d", len(loaded.Mounts), len(manifest.Mounts))
	}
}

func TestLoadRuntimeMountManifestValidatesExpectedDriver(t *testing.T) {
	root := t.TempDir()
	session := testRuntimeMountSession(root)
	if _, err := prepareRuntimeMountManifest(testRuntimeMountConfig(), session, RuntimeDriverDocker); err != nil {
		t.Fatalf("prepareRuntimeMountManifest returned error: %v", err)
	}
	if _, err := loadRuntimeMountManifest(session, RuntimeDriverBoxlite); err == nil {
		t.Fatalf("loadRuntimeMountManifest accepted manifest for wrong driver")
	}
}

func TestLoadDirectoryRuntimeMountManifestRejectsFileSource(t *testing.T) {
	root := t.TempDir()
	session := testRuntimeMountSession(root)
	if _, err := prepareRuntimeMountManifest(testRuntimeMountConfig(), session, RuntimeDriverDocker); err != nil {
		t.Fatalf("prepareRuntimeMountManifest returned error: %v", err)
	}
	if _, err := loadDirectoryRuntimeMountManifest(session, RuntimeDriverDocker); err == nil {
		t.Fatalf("loadDirectoryRuntimeMountManifest accepted docker file source")
	}
}

func TestRuntimeMountManifestDriverSpecificStartPreparationWorkflow(t *testing.T) {
	testRuntimeMountManifestDriverSpecificStartPreparationWorkflow(t)
}

func testRuntimeMountManifestDriverSpecificStartPreparationWorkflow(t *testing.T) {
	t.Helper()
	root := t.TempDir()
	config := &appconfig.Config{
		GuestWorkspacePath: "/workspace",
		GuestHomePath:      "/root",
		GuestStateRoot:     "/data/state",
		GuestRuntimeRoot:   "/data/runtime",
		GuestLogRoot:       "/data/logs",
	}
	session := testRuntimeMountSession(root)
	if _, err := prepareRuntimeMountManifest(config, session, RuntimeDriverDocker); err != nil {
		t.Fatalf("prepare docker manifest returned error: %v", err)
	}
	dockerManifest, err := loadRuntimeMountManifest(session, RuntimeDriverDocker)
	if err != nil {
		t.Fatalf("load docker manifest: %v", err)
	}
	dockerMounts := map[string]string{}
	for _, mount := range dockerManifest.Mounts {
		dockerMounts[mount.GuestPath] = mount.HostPath
	}
	for _, guestPath := range []string{"/root/.claude.json", "/root/.gitconfig"} {
		if dockerMounts[guestPath] == "" {
			t.Fatalf("docker manifest missing file mount %s: %#v", guestPath, dockerMounts)
		}
	}
	if _, err := prepareRuntimeMountManifest(config, session, RuntimeDriverBoxlite); err != nil {
		t.Fatalf("prepare boxlite manifest returned error: %v", err)
	}
	boxliteManifest, err := loadDirectoryRuntimeMountManifest(session, RuntimeDriverBoxlite)
	if err != nil {
		t.Fatalf("load boxlite directory manifest: %v", err)
	}
	boxliteMounts := map[string]string{}
	for _, mount := range boxliteManifest.Mounts {
		boxliteMounts[mount.GuestPath] = mount.HostPath
	}
	if boxliteMounts[directoryOnlyGuestSessionPath] != hostSessionDir(session) {
		t.Fatalf("boxlite session source = %q, want %q", boxliteMounts[directoryOnlyGuestSessionPath], hostSessionDir(session))
	}
	for _, guestPath := range []string{"/root/.claude.json", "/root/.gitconfig"} {
		if boxliteMounts[guestPath] != "" {
			t.Fatalf("boxlite manifest retained file mount %s -> %s", guestPath, boxliteMounts[guestPath])
		}
	}
}

func TestInitializeSessionHomeDefaultsCreatesWritableCodexConfig(t *testing.T) {
	root := t.TempDir()
	session := testRuntimeMountSession(root)
	if err := initializeSessionHomeDefaults(session); err != nil {
		t.Fatalf("initializeSessionHomeDefaults returned error: %v", err)
	}
	codexConfig := filepath.Join(root, "home", ".codex", "config.toml")
	info, err := os.Stat(codexConfig)
	if err != nil {
		t.Fatalf("Stat(%s) returned error: %v", codexConfig, err)
	}
	if info.Mode().Perm()&0o200 == 0 {
		t.Fatalf("codex config mode = %v, want owner-writable", info.Mode().Perm())
	}
	if err := os.WriteFile(codexConfig, []byte("model = \"test\"\n"), 0o644); err != nil {
		t.Fatalf("codex config should be writable after defaults initialization: %v", err)
	}
}

func TestInitializeSessionHomeDefaultsDoesNotOverwriteExistingTargets(t *testing.T) {
	root := t.TempDir()
	session := testRuntimeMountSession(root)
	codexConfig := filepath.Join(root, "home", ".codex", "config.toml")
	if err := os.MkdirAll(filepath.Dir(codexConfig), 0o755); err != nil {
		t.Fatalf("MkdirAll returned error: %v", err)
	}
	if err := os.WriteFile(codexConfig, []byte("custom = true\n"), 0o644); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}
	gitconfig := filepath.Join(root, "home", ".gitconfig")
	if err := os.WriteFile(gitconfig, []byte("[user]\n\tname = Custom\n"), 0o644); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}
	if err := initializeSessionHomeDefaults(session); err != nil {
		t.Fatalf("initializeSessionHomeDefaults returned error: %v", err)
	}
	assertFileContent(t, codexConfig, "custom = true\n")
	assertFileContent(t, gitconfig, "[user]\n\tname = Custom\n")
	if _, err := os.Stat(filepath.Join(root, "home", ".claude", "settings.json")); err != nil {
		t.Fatalf("expected missing claude defaults to be initialized: %v", err)
	}
}

func TestLoadRuntimeMountManifestValidatesContent(t *testing.T) {
	root := t.TempDir()
	session := testRuntimeMountSession(root)
	path := runtimeMountManifestPath(session)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll returned error: %v", err)
	}
	if err := os.WriteFile(path, []byte(`{"version":1,"driver":"docker","mounts":[{"hostPath":"relative","guestPath":"/workspace","type":"bind"}]}`), 0o644); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}
	if _, err := loadRuntimeMountManifest(session, RuntimeDriverDocker); err == nil {
		t.Fatalf("loadRuntimeMountManifest accepted relative host path")
	}
}
