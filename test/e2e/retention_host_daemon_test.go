package e2e

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"

	"agent-compose/pkg/imagecache"
	domain "agent-compose/pkg/model"
	agentcomposev2 "agent-compose/proto/agentcompose/v2"
	"agent-compose/proto/agentcompose/v2/agentcomposev2connect"
)

const dockerRetentionE2EImageEnv = "AGENT_COMPOSE_E2E_RETENTION_IMAGE"

func TestE2EDockerDaemonRetentionCleanup(t *testing.T) {
	image := strings.TrimSpace(os.Getenv(dockerRetentionE2EImageEnv))
	if image == "" {
		t.Skipf("set %s to a local Docker guest image", dockerRetentionE2EImageEnv)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	repoRoot := e2eRepoRoot(t)
	testRoot, err := os.MkdirTemp(repoRoot, ".docker-retention-e2e-")
	if err != nil {
		t.Fatalf("create Docker-visible retention root: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(testRoot) })
	dockerClient := newE2EDockerClient(t, ctx, image)
	binary := e2eDaemonBinary(t, ctx, repoRoot, testRoot)

	cacheRoot := filepath.Join(testRoot, "images")
	cache, materializedPath := prepareExpiredE2EImageCache(t, cacheRoot)
	listenAddress := unusedLoopbackAddress(t)
	baseURL := "http://" + listenAddress
	daemon := startE2EDaemonWithEnv(t, binary, repoRoot, testRoot, listenAddress, image, map[string]string{
		"CLEANUP_INTERVAL":        "100ms",
		"IMAGE_CACHE_CLEANUP_TTL": "1h",
		"IMAGE_CACHE_ROOT":        cacheRoot,
		"WORKSPACE_CLEANUP_TTL":   "500ms",
	})
	waitForE2EDaemon(t, ctx, daemon, baseURL)
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("retention daemon log:\n%s", daemon.logs.String())
		}
	})

	waitForE2ECondition(t, 10*time.Second, func() bool {
		metadata, loadErr := cache.LoadMetadata()
		_, statErr := os.Stat(materializedPath)
		return loadErr == nil && len(metadata.Images) == 0 && os.IsNotExist(statErr)
	}, "expired image cache was not removed")

	httpClient := newE2EHTTPClient()
	defer httpClient.CloseIdleConnections()
	projectClient := agentcomposev2connect.NewProjectServiceClient(httpClient, baseURL)
	runClient := agentcomposev2connect.NewRunServiceClient(httpClient, baseURL)
	sandboxClient := agentcomposev2connect.NewSandboxServiceClient(httpClient, baseURL)
	projectID := applyE2ERetentionProject(t, ctx, projectClient, testRoot, image)
	sandbox := runE2EWorkspaceSandbox(t, ctx, runClient, sandboxClient, projectID, "retention-workspace")
	sandboxID := sandbox.GetSandboxId()
	removed := false
	t.Cleanup(func() {
		cleanupE2EWorkspaceSandbox(t, dockerClient, sandboxClient, sandboxID, removed)
	})
	workspacePath := sandbox.GetWorkspacePath()

	stopResp, err := sandboxClient.StopSandbox(ctx, connect.NewRequest(&agentcomposev2.StopSandboxRequest{SandboxId: sandboxID}))
	if err != nil || stopResp.Msg.GetSandbox().GetStatus() != domain.VMStatusStopped {
		t.Fatalf("StopSandbox = %#v, error %v", stopResp, err)
	}
	waitForE2ECondition(t, 15*time.Second, func() bool {
		response, getErr := sandboxClient.GetSandbox(ctx, connect.NewRequest(&agentcomposev2.GetSandboxRequest{SandboxId: sandboxID}))
		return getErr == nil && response.Msg.GetSandbox().GetWorkspaceReclamationState() == domain.SandboxWorkspaceReclamationStateReclaimed
	}, "stopped sandbox workspace was not reclaimed")
	if _, err := os.Stat(workspacePath); !os.IsNotExist(err) {
		t.Fatalf("reclaimed workspace still exists: %v", err)
	}
	if _, err := sandboxClient.ResumeSandbox(ctx, connect.NewRequest(&agentcomposev2.ResumeSandboxRequest{SandboxId: sandboxID})); err == nil {
		t.Fatal("ResumeSandbox succeeded after workspace reclamation")
	}

	removeResp, err := sandboxClient.RemoveSandbox(ctx, connect.NewRequest(&agentcomposev2.RemoveSandboxRequest{SandboxId: sandboxID, Force: true}))
	if err != nil || removeResp.Msg.GetSandboxId() != sandboxID || !removeResp.Msg.GetRemoved() {
		t.Fatalf("RemoveSandbox reclaimed sandbox = %#v, error %v", removeResp, err)
	}
	removed = true
	removeE2EDockerSandboxFallback(t, ctx, dockerClient, sandboxID)
	assertE2EDockerSandboxContainerCount(t, ctx, dockerClient, sandboxID, 0)
	if _, err := projectClient.RemoveProject(ctx, connect.NewRequest(&agentcomposev2.RemoveProjectRequest{
		Project: &agentcomposev2.ProjectRef{ProjectId: projectID},
	})); err != nil {
		t.Fatalf("RemoveProject returned error: %v", err)
	}
	daemon.stop(t)
	assertE2EDaemonReleased(t, daemon, filepath.Join(testRoot, "agent-compose.sock"), listenAddress)
}

func prepareExpiredE2EImageCache(t *testing.T, root string) (*imagecache.Cache, string) {
	t.Helper()
	cache, err := imagecache.New(imagecache.Config{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	digest := "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	materializedPath := cache.MaterializedImageDir(digest)
	if err := os.MkdirAll(filepath.Join(materializedPath, "rootfs"), 0o755); err != nil {
		t.Fatal(err)
	}
	old := time.Now().UTC().Add(-2 * time.Hour)
	if err := cache.SaveMetadata(imagecache.MetadataFile{Images: []imagecache.ImageMetadata{{
		RequestedRef: "retention.invalid/expired:latest", NormalizedRef: "retention.invalid/expired:latest",
		ConfigDigest: digest, PulledAt: old, LastUsedAt: old,
	}}}); err != nil {
		t.Fatal(err)
	}
	return cache, materializedPath
}

func applyE2ERetentionProject(t *testing.T, ctx context.Context, client agentcomposev2connect.ProjectServiceClient, root, image string) string {
	t.Helper()
	response, err := client.ApplyProject(ctx, connect.NewRequest(&agentcomposev2.ApplyProjectRequest{
		Spec: &agentcomposev2.ProjectSpec{
			Name: "docker-retention-e2e",
			Agents: []*agentcomposev2.AgentSpec{{
				Name: "worker", Provider: "codex", Image: image,
				Driver: &agentcomposev2.DriverSpec{Name: "docker", Docker: &agentcomposev2.DockerDriverSpec{}},
			}},
		},
		Source: &agentcomposev2.ProjectSource{ComposePath: filepath.Join(root, "agent-compose.yml"), ProjectDir: root},
	}))
	if err != nil {
		t.Fatalf("ApplyProject returned error: %v", err)
	}
	return response.Msg.GetProject().GetSummary().GetProjectId()
}

func waitForE2ECondition(t *testing.T, timeout time.Duration, condition func() bool, failure string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal(failure)
}
