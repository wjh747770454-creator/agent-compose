package agentcompose

import "agent-compose/pkg/agentcompose/domain"

const (
	TopicEventSourceWebhook = domain.TopicEventSourceWebhook
	TopicEventSourceLoader  = domain.TopicEventSourceLoader
	TopicEventSourceSystem  = domain.TopicEventSourceSystem

	TopicEventDispatchPending        = domain.TopicEventDispatchPending
	TopicEventDispatchPublishing     = domain.TopicEventDispatchPublishing
	TopicEventDispatchPublishedToBus = domain.TopicEventDispatchPublishedToBus
	TopicEventDispatchNoSubscriber   = domain.TopicEventDispatchNoSubscriber
	TopicEventDispatchRetrying       = domain.TopicEventDispatchRetrying
	TopicEventDispatchDeadLetter     = domain.TopicEventDispatchDeadLetter

	EventDeliveryStatusMatched      = domain.EventDeliveryStatusMatched
	EventDeliveryStatusRunStarted   = domain.EventDeliveryStatusRunStarted
	EventDeliveryStatusRunSucceeded = domain.EventDeliveryStatusRunSucceeded
	EventDeliveryStatusRunFailed    = domain.EventDeliveryStatusRunFailed
	EventDeliveryStatusSkipped      = domain.EventDeliveryStatusSkipped
)

type (
	TopicEventRecord      = domain.TopicEventRecord
	TopicEventFilter      = domain.TopicEventFilter
	WebhookSource         = domain.WebhookSource
	EventDelivery         = domain.EventDelivery
	EventSessionLink      = domain.EventSessionLink
	EventSessionTraceItem = domain.EventSessionTraceItem
)

func validateTopicEventName(topic string) error {
	return domain.ValidateTopicEventName(topic)
}

func normalizeTopicEventSource(source string) string {
	return domain.NormalizeTopicEventSource(source)
}

func normalizeTopicEventDispatchStatus(status string) string {
	return domain.NormalizeTopicEventDispatchStatus(status)
}

func normalizeEventDeliveryStatus(status string) string {
	return domain.NormalizeEventDeliveryStatus(status)
}

func topicEventPayloadSHA256(payloadJSON string) string {
	return domain.TopicEventPayloadSHA256(payloadJSON)
}
