package agentcompose

import (
	"time"

	"agent-compose/pkg/agentcompose/domain"
)

const (
	LoaderRuntimeScheduler = domain.LoaderRuntimeScheduler

	LoaderTriggerKindInterval = domain.LoaderTriggerKindInterval
	LoaderTriggerKindEvent    = domain.LoaderTriggerKindEvent
	LoaderTriggerKindTimeout  = domain.LoaderTriggerKindTimeout
	LoaderTriggerKindCron     = domain.LoaderTriggerKindCron

	LoaderSessionPolicySticky = domain.LoaderSessionPolicySticky
	LoaderSessionPolicyNew    = domain.LoaderSessionPolicyNew
	LoaderSessionPolicyReuse  = domain.LoaderSessionPolicyReuse

	LoaderConcurrencyPolicySkip     = domain.LoaderConcurrencyPolicySkip
	LoaderConcurrencyPolicyParallel = domain.LoaderConcurrencyPolicyParallel

	LoaderRunStatusRunning   = domain.LoaderRunStatusRunning
	LoaderRunStatusSucceeded = domain.LoaderRunStatusSucceeded
	LoaderRunStatusFailed    = domain.LoaderRunStatusFailed
	LoaderRunStatusSkipped   = domain.LoaderRunStatusSkipped
)

type (
	LoaderSummary        = domain.LoaderSummary
	Loader               = domain.Loader
	LoaderTrigger        = domain.LoaderTrigger
	LoaderRunSummary     = domain.LoaderRunSummary
	LoaderEvent          = domain.LoaderEvent
	LoaderBinding        = domain.LoaderBinding
	LoaderAgentRequest   = domain.LoaderAgentRequest
	LoaderAgentResult    = domain.LoaderAgentResult
	LoaderCommandRequest = domain.LoaderCommandRequest
	LoaderCommandResult  = domain.LoaderCommandResult
	LoaderLLMRequest     = domain.LoaderLLMRequest
	LoaderLLMResult      = domain.LoaderLLMResult
	LoaderTopicEvent     = domain.LoaderTopicEvent
)

func normalizeLoaderRuntime(runtime string) (string, error) {
	return domain.NormalizeLoaderRuntime(runtime)
}

func normalizeLoaderTriggerKind(kind string) (string, error) {
	return domain.NormalizeLoaderTriggerKind(kind)
}

func normalizeLoaderSessionPolicy(policy string) string {
	return domain.NormalizeLoaderSessionPolicy(policy)
}

func normalizeLoaderConcurrencyPolicy(policy string) string {
	return domain.NormalizeLoaderConcurrencyPolicy(policy)
}

func normalizeLoaderRunStatus(status string) string {
	return domain.NormalizeLoaderRunStatus(status)
}

func loaderTriggerStableID(kind, topic string, intervalMs int64, callbackSource string, index int) string {
	return domain.LoaderTriggerStableID(kind, topic, intervalMs, callbackSource, index)
}

func loaderSourceSHA(script string) string {
	return domain.LoaderSourceSHA(script)
}

func loaderTriggerTopicMatches(pattern, topic string) bool {
	return domain.LoaderTriggerTopicMatches(pattern, topic)
}

func timeIsSet(value time.Time) bool {
	return domain.TimeIsSet(value)
}

func nonZeroTimeUnixMilli(value time.Time) int64 {
	return domain.NonZeroTimeUnixMilli(value)
}

func loaderTriggerUsesSchedule(kind string) bool {
	return domain.LoaderTriggerUsesSchedule(kind)
}

func loaderTriggerScheduledAt(now time.Time, delayMs int64) time.Time {
	return domain.LoaderTriggerScheduledAt(now, delayMs)
}

func defaultLoaderName(now time.Time) string {
	return domain.DefaultLoaderName(now)
}

func defaultLoaderScript() string {
	return domain.DefaultLoaderScript()
}
