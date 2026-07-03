package capproxy

import "testing"

func TestIntegrationCapabilityProxyWorkflows(t *testing.T) {
	t.Run("metadata", TestProxyInjectsOctoBusMetadata)
	t.Run("guest instance", TestProxyForwardsGuestInstance)
	t.Run("missing instance", TestProxyRejectsMissingInstanceForBusinessCall)
	t.Run("capset denied", TestProxyRejectsCapsetOutsideAllowedSet)
	t.Run("missing token", TestProxyRejectsMissingSessionToken)
}

func TestE2ECapabilityProxyWorkflows(t *testing.T) {
	TestIntegrationCapabilityProxyWorkflows(t)
}
