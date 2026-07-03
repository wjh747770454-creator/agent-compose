package driver

import "testing"

func TestRuntimeDriverWorkflow(t *testing.T) {
	TestDockerRuntimeSessionProxyStateUsesContainerNameAndGuestPort(t)
	testRuntimeMountManifestDriverSpecificStartPreparationWorkflow(t)
	testDockerImageRefMatchingInternals(t)
	testConsumeDockerPullStream(t)
	testExecOutputFilterWorkflows(t)
}

func TestIntegrationRuntimeDriverWorkflow(t *testing.T) {
	TestDockerRuntimeSessionProxyStateUsesContainerNameAndGuestPort(t)
	testRuntimeMountManifestDriverSpecificStartPreparationWorkflow(t)
	testDockerImageRefMatchingInternals(t)
	testConsumeDockerPullStream(t)
	testExecOutputFilterWorkflows(t)
}

func TestE2ERuntimeDriverWorkflow(t *testing.T) {
	TestDockerRuntimeSessionProxyStateUsesContainerNameAndGuestPort(t)
	testRuntimeMountManifestDriverSpecificStartPreparationWorkflow(t)
	testDockerImageRefMatchingInternals(t)
	testConsumeDockerPullStream(t)
	testExecOutputFilterWorkflows(t)
}
