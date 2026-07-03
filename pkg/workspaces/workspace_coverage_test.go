package workspaces

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"os"
	"path/filepath"
	"strings"
	"testing"

	appconfig "agent-compose/pkg/config"
	domain "agent-compose/pkg/model"
)

func TestWorkspaceFileAndPathWorkflows(t *testing.T) {
	testWorkspaceFileAndPathWorkflows(t)
}

func TestIntegrationWorkspaceFileAndPathWorkflows(t *testing.T) {
	testWorkspaceFileAndPathWorkflows(t)
}

func TestE2EWorkspaceFileAndPathWorkflows(t *testing.T) {
	testWorkspaceFileAndPathWorkflows(t)
}

func testWorkspaceFileAndPathWorkflows(t *testing.T) {
	t.Helper()
	config := &appconfig.Config{DataRoot: t.TempDir()}
	workspaceID := "ws-file"
	contentRoot := mustDefaultFileWorkspaceContentRoot(t, config, workspaceID)
	if err := os.MkdirAll(filepath.Join(contentRoot, "docs"), 0o755); err != nil {
		t.Fatalf("mkdir content root: %v", err)
	}
	if err := os.WriteFile(filepath.Join(contentRoot, "README.md"), []byte("workspace\n"), 0o644); err != nil {
		t.Fatalf("write README.md: %v", err)
	}
	if err := os.WriteFile(filepath.Join(contentRoot, "docs", "guide.md"), []byte("guide\n"), 0o644); err != nil {
		t.Fatalf("write guide.md: %v", err)
	}
	session := &domain.Session{Summary: domain.SessionSummary{ID: "session-1", WorkspacePath: t.TempDir()}}
	workspace := domain.WorkspaceConfig{
		ID:         workspaceID,
		Name:       "File Workspace",
		Type:       "file",
		ConfigJSON: encodeFileWorkspaceConfigForTest(t, contentRoot),
	}
	if err := PrepareFileWorkspace(config, session, workspace); err != nil {
		t.Fatalf("PrepareFileWorkspace returned error: %v", err)
	}
	assertFileContent(t, filepath.Join(session.Summary.WorkspacePath, "README.md"), "workspace\n")
	assertFileContent(t, filepath.Join(session.Summary.WorkspacePath, "docs", "guide.md"), "guide\n")

	if err := os.WriteFile(filepath.Join(contentRoot, "README.md"), []byte("updated\n"), 0o644); err != nil {
		t.Fatalf("overwrite README.md: %v", err)
	}
	if err := PrepareSessionWorkspace(context.Background(), config, nil, &domain.Session{
		Summary:     domain.SessionSummary{ID: "session-snapshot", WorkspacePath: session.Summary.WorkspacePath},
		WorkspaceID: workspaceID,
		Workspace: &domain.SessionWorkspace{
			ID:         workspaceID,
			Name:       "Snapshot File Workspace",
			Type:       "file",
			ConfigJSON: DefaultFileConfigJSON(config, workspaceID),
		},
	}); err != nil {
		t.Fatalf("PrepareSessionWorkspace returned error: %v", err)
	}
	assertFileContent(t, filepath.Join(session.Summary.WorkspacePath, "README.md"), "updated\n")

	if _, err := FileWorkspaceContentRoot(config, domain.WorkspaceConfig{ID: "ws-file", Name: "File Workspace", Type: "file", ConfigJSON: encodeFileWorkspaceConfigForTest(t, t.TempDir())}); err == nil {
		t.Fatalf("expected outside data root to be rejected")
	}
	for _, workspaceID := range []string{"", ".", "..", "a/b", "/abs"} {
		if _, err := FileWorkspaceContentRelRoot(workspaceID); err == nil {
			t.Fatalf("FileWorkspaceContentRelRoot(%q) returned nil error", workspaceID)
		}
	}
	if _, err := ValidateFileWorkspaceConfig(config, "ws-ok", DefaultFileConfigJSON(config, "ws-ok")); err != nil {
		t.Fatalf("ValidateFileWorkspaceConfig returned error: %v", err)
	}

	dataRoot, err := OpenFileWorkspaceDataRoot(config)
	if err != nil {
		t.Fatalf("OpenFileWorkspaceDataRoot returned error: %v", err)
	}
	defer func() { _ = dataRoot.Close() }()
	if err := EnsureRootParentDir(dataRoot, "nested/file.txt"); err != nil {
		t.Fatalf("EnsureRootParentDir returned error: %v", err)
	}
	if err := EnsureRootDir(dataRoot, "nested"); err != nil {
		t.Fatalf("EnsureRootDir returned error: %v", err)
	}
	for _, raw := range []string{"", ".", "/abs", "../x", "x/../../y"} {
		if _, err := CleanRelativePath(raw, false); err == nil {
			t.Fatalf("CleanRelativePath(%q) returned nil error", raw)
		}
	}
	if got, err := CleanRelativePath(" a/../b ", false); err != nil || got != "b" {
		t.Fatalf("CleanRelativePath clean = %q/%v", got, err)
	}

	files, err := ListFiles(dataRoot)
	if err != nil {
		t.Fatalf("ListFiles returned error: %v", err)
	}
	if len(files) == 0 {
		t.Fatalf("ListFiles returned no entries")
	}
}

func TestWorkspaceArchiveAndUploadWorkflows(t *testing.T) {
	testWorkspaceArchiveAndUploadWorkflows(t)
}

func TestIntegrationWorkspaceArchiveAndUploadWorkflows(t *testing.T) {
	testWorkspaceArchiveAndUploadWorkflows(t)
}

func TestE2EWorkspaceArchiveAndUploadWorkflows(t *testing.T) {
	testWorkspaceArchiveAndUploadWorkflows(t)
}

func TestIntegrationWorkspaceGitHelperWorkflow(t *testing.T) {
	TestWorkspaceGitHelpers(t)
}

func TestE2EWorkspaceGitHelperWorkflow(t *testing.T) {
	TestWorkspaceGitHelpers(t)
}

func testWorkspaceArchiveAndUploadWorkflows(t *testing.T) {
	t.Helper()
	contentRoot := t.TempDir()
	root, err := os.OpenRoot(contentRoot)
	if err != nil {
		t.Fatalf("OpenRoot contentRoot: %v", err)
	}
	defer func() { _ = root.Close() }()

	var archive bytes.Buffer
	tw := tar.NewWriter(&archive)
	writeTarDir(t, tw, "dir")
	writeTarFile(t, tw, "dir/file.txt", "from archive\n")
	if err := tw.Close(); err != nil {
		t.Fatalf("close tar: %v", err)
	}
	if err := ExtractWorkspaceTarArchive(&archive, root); err != nil {
		t.Fatalf("ExtractWorkspaceTarArchive returned error: %v", err)
	}
	assertFileContent(t, filepath.Join(contentRoot, "dir", "file.txt"), "from archive\n")

	var badArchive bytes.Buffer
	badTW := tar.NewWriter(&badArchive)
	if err := badTW.WriteHeader(&tar.Header{Name: "../escape.txt", Mode: 0o644, Size: 1}); err != nil {
		t.Fatalf("WriteHeader bad: %v", err)
	}
	if _, err := badTW.Write([]byte("x")); err != nil {
		t.Fatalf("write bad body: %v", err)
	}
	if err := badTW.Close(); err != nil {
		t.Fatalf("close bad tar: %v", err)
	}
	if err := ExtractWorkspaceTarArchive(&badArchive, root); err == nil {
		t.Fatalf("ExtractWorkspaceTarArchive accepted escaping path")
	}

	header := multipartFileHeader(t, "upload.txt", "uploaded\n")
	if err := StoreUploadedFile(header, root, "uploads/upload.txt"); err != nil {
		t.Fatalf("StoreUploadedFile returned error: %v", err)
	}
	assertFileContent(t, filepath.Join(contentRoot, "uploads", "upload.txt"), "uploaded\n")

	archiveHeader := multipartArchiveHeader(t, "archive.tar")
	if err := ExtractUploadedArchive(archiveHeader, root); err != nil {
		t.Fatalf("ExtractUploadedArchive returned error: %v", err)
	}
	assertFileContent(t, filepath.Join(contentRoot, "uploaded-archive.txt"), "archive body\n")
}

func TestWorkspaceGitHelpers(t *testing.T) {
	for _, tc := range []struct {
		raw     string
		want    string
		wantErr bool
	}{
		{raw: "", want: "."},
		{raw: "repo/subdir", want: filepath.Clean("repo/subdir")},
		{raw: "repo/../src", want: "src"},
		{raw: "/tmp/repo", wantErr: true},
		{raw: "../repo", wantErr: true},
		{raw: "repo/../../escape", wantErr: true},
	} {
		got, err := NormalizeGitCloneTarget("ws-1", tc.raw)
		if tc.wantErr {
			if err == nil {
				t.Fatalf("NormalizeGitCloneTarget(%q) returned nil error", tc.raw)
			}
			continue
		}
		if err != nil || got != tc.want {
			t.Fatalf("NormalizeGitCloneTarget(%q) = %q/%v, want %q", tc.raw, got, err, tc.want)
		}
	}
	if got := GitCloneArgs("https://example.test/repo.git", GitWorkspaceConfig{Branch: "main"}, "/tmp/workspace"); strings.Join(got, "\x00") != strings.Join([]string{"clone", "--depth", "1", "--branch", "main", "https://example.test/repo.git", "/tmp/workspace"}, "\x00") {
		t.Fatalf("GitCloneArgs = %#v", got)
	}
	if got := GitCommitFetchArgs("e413509"); strings.Join(got, "\x00") != strings.Join([]string{"fetch", "--depth", "1", "origin", "e413509"}, "\x00") {
		t.Fatalf("GitCommitFetchArgs = %#v", got)
	}
	if !strings.Contains(strings.Join(GitDeepenFetchArgs(true), " "), "--unshallow") || strings.Contains(strings.Join(GitDeepenFetchArgs(false), " "), "--unshallow") {
		t.Fatalf("GitDeepenFetchArgs returned unexpected values")
	}
	if got := ApplyGitCredentials("https://example.test/repo.git", GitWorkspaceConfig{Username: "u ser", Password: "p@ss"}); !strings.Contains(got, "u+ser:p%40ss@") {
		t.Fatalf("ApplyGitCredentials username/password = %q", got)
	}
	if got := ApplyGitCredentials("ssh://example.test/repo.git", GitWorkspaceConfig{Credential: "token"}); got != "ssh://example.test/repo.git" {
		t.Fatalf("ApplyGitCredentials ssh = %q", got)
	}

	workspaceRoot := t.TempDir()
	if err := os.Mkdir(filepath.Join(workspaceRoot, ".agent-compose"), 0o755); err != nil {
		t.Fatalf("mkdir .agent-compose: %v", err)
	}
	if err := os.Mkdir(filepath.Join(workspaceRoot, GitWorkspaceTempDirName), 0o755); err != nil {
		t.Fatalf("mkdir temp dir: %v", err)
	}
	initialized, err := HostWorkspaceInitialized(workspaceRoot)
	if err != nil || initialized {
		t.Fatalf("HostWorkspaceInitialized internal-only = %v/%v", initialized, err)
	}
	if err := os.WriteFile(filepath.Join(workspaceRoot, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("write README.md: %v", err)
	}
	initialized, err = HostWorkspaceInitialized(workspaceRoot)
	if err != nil || !initialized {
		t.Fatalf("HostWorkspaceInitialized after file = %v/%v", initialized, err)
	}

	src := filepath.Join(t.TempDir(), "src")
	dst := filepath.Join(t.TempDir(), "dst")
	if err := os.MkdirAll(filepath.Join(src, "nested"), 0o755); err != nil {
		t.Fatalf("mkdir src: %v", err)
	}
	if err := os.MkdirAll(dst, 0o755); err != nil {
		t.Fatalf("mkdir dst: %v", err)
	}
	if err := os.WriteFile(filepath.Join(src, "nested", "file.txt"), []byte("moved\n"), 0o644); err != nil {
		t.Fatalf("write src file: %v", err)
	}
	if err := MoveWorkspaceEntry(src, dst); err != nil {
		t.Fatalf("MoveWorkspaceEntry merge returned error: %v", err)
	}
	assertFileContent(t, filepath.Join(dst, "nested", "file.txt"), "moved\n")
}

func encodeFileWorkspaceConfigForTest(t *testing.T, root string) string {
	t.Helper()
	data, err := json.Marshal(FileWorkspaceConfig{Root: root})
	if err != nil {
		t.Fatalf("marshal file workspace config: %v", err)
	}
	return string(data)
}

func mustDefaultFileWorkspaceContentRoot(t *testing.T, config *appconfig.Config, workspaceID string) string {
	t.Helper()
	root, err := DefaultFileWorkspaceContentRoot(config, workspaceID)
	if err != nil {
		t.Fatalf("DefaultFileWorkspaceContentRoot returned error: %v", err)
	}
	return root
}

func assertFileContent(t *testing.T, path, want string) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%s): %v", path, err)
	}
	if string(got) != want {
		t.Fatalf("ReadFile(%s) = %q, want %q", path, string(got), want)
	}
}

func writeTarDir(t *testing.T, tw *tar.Writer, name string) {
	t.Helper()
	if err := tw.WriteHeader(&tar.Header{Name: name, Typeflag: tar.TypeDir, Mode: 0o755}); err != nil {
		t.Fatalf("WriteHeader dir %s: %v", name, err)
	}
}

func writeTarFile(t *testing.T, tw *tar.Writer, name, body string) {
	t.Helper()
	if err := tw.WriteHeader(&tar.Header{Name: name, Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(len(body))}); err != nil {
		t.Fatalf("WriteHeader file %s: %v", name, err)
	}
	if _, err := tw.Write([]byte(body)); err != nil {
		t.Fatalf("write file %s: %v", name, err)
	}
}

func multipartFileHeader(t *testing.T, filename, body string) *multipart.FileHeader {
	t.Helper()
	var payload bytes.Buffer
	writer := multipart.NewWriter(&payload)
	part, err := writer.CreateFormFile("file", filename)
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	if _, err := part.Write([]byte(body)); err != nil {
		t.Fatalf("write multipart body: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}
	reader := multipart.NewReader(&payload, writer.Boundary())
	form, err := reader.ReadForm(1 << 20)
	if err != nil {
		t.Fatalf("ReadForm: %v", err)
	}
	return form.File["file"][0]
}

func multipartArchiveHeader(t *testing.T, filename string) *multipart.FileHeader {
	t.Helper()
	var archive bytes.Buffer
	tw := tar.NewWriter(&archive)
	writeTarFile(t, tw, "uploaded-archive.txt", "archive body\n")
	if err := tw.Close(); err != nil {
		t.Fatalf("close archive tar: %v", err)
	}
	return multipartFileHeader(t, filename, archive.String())
}
