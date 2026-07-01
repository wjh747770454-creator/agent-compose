package agentcompose

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

	"agent-compose/pkg/agentcompose/webhooks"
)

type (
	webhookAcceptedResponse   = webhooks.AcceptedResponse
	topicEventResponse        = webhooks.TopicEventResponse
	topicEventListResponse    = webhooks.TopicEventListResponse
	eventSessionsResponse     = webhooks.EventSessionsResponse
	eventRunsResponse         = webhooks.EventRunsResponse
	webhookSourceRequest      = webhooks.SourceRequest
	webhookSourceJSON         = webhooks.SourceJSON
	webhookSourceListResponse = webhooks.SourceListResponse
	webhookSourceResponse     = webhooks.SourceResponse
	topicEventJSON            = webhooks.TopicEventJSON
)

func registerWebhookRoutes(app *echo.Echo, service *Service) {
	app.POST("/api/webhooks/:topic", service.handleWebhook)
	app.GET("/api/webhook-sources", service.handleListWebhookSources)
	app.PUT("/api/webhook-sources/:source_id", service.handlePutWebhookSource)
	app.DELETE("/api/webhook-sources/:source_id", service.handleDeleteWebhookSource)
	app.GET("/api/events", service.handleListEvents)
	app.GET("/api/events/:event_id/sessions", service.handleGetEventSessions)
	app.GET("/api/events/:event_id/runs", service.handleGetEventRuns)
	app.GET("/api/events/:event_id", service.handleGetEvent)
}

func (s *Service) handleWebhook(c echo.Context) error {
	topic := strings.TrimSpace(c.Param("topic"))
	if err := validateExternalWebhookTopic(topic); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
	}
	source, bodyLimit, handled, err := s.authorizeWebhookRequest(c, topic)
	if handled {
		return err
	}
	if !requestContentTypeIsJSON(c.Request()) {
		return c.JSON(http.StatusUnsupportedMediaType, map[string]string{"error": "content-type must be application/json"})
	}
	rawBody, err := readWebhookBody(c.Request(), bodyLimit)
	if err != nil {
		if errors.Is(err, ErrBodyTooLarge) {
			return c.JSON(http.StatusRequestEntityTooLarge, map[string]string{"error": "request body is too large"})
		}
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "failed to read request body"})
	}
	body, compactBody, err := decodeWebhookJSONObject(rawBody)
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
	}
	idempotencyKey := extractIdempotencyKey(c.Request())
	if existing, ok, err := s.configDB.FindEventByIdempotencyKey(c.Request().Context(), topic, idempotencyKey); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to load webhook event"})
	} else if ok {
		if existingWebhookBodyHash(existing.PayloadJSON) != topicEventPayloadSHA256(compactBody) {
			return c.JSON(http.StatusConflict, map[string]string{"error": "idempotency key conflicts with existing payload"})
		}
		return c.JSON(http.StatusAccepted, webhookAcceptedResponse{
			Accepted:      true,
			Topic:         existing.Topic,
			EventID:       existing.ID,
			Sequence:      existing.Sequence,
			CorrelationID: existing.CorrelationID,
		})
	}

	eventID := "evt_" + newUUIDString()
	correlationID := extractCorrelationID(c.Request(), body)
	if correlationID == "" {
		correlationID = eventID
	}
	payload := buildWebhookPayload(c, eventID, 0, topic, correlationID, idempotencyKey, source, body)
	payloadJSON, err := marshalJSONCompact(payload)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to encode webhook payload"})
	}
	payloadHash := topicEventPayloadSHA256(payloadJSON)
	created, err := s.configDB.CreateEvent(c.Request().Context(), TopicEventRecord{
		ID:             eventID,
		Topic:          topic,
		Source:         TopicEventSourceWebhook,
		Provider:       firstNonEmpty(source.Provider, providerFromWebhookTopic(topic)),
		Intent:         intentFromWebhookBody(body),
		CorrelationID:  correlationID,
		IdempotencyKey: idempotencyKey,
		DeliveryID:     extractDeliveryID(c.Request()),
		PayloadHash:    payloadHash,
		PayloadJSON:    payloadJSON,
		DispatchStatus: TopicEventDispatchPending,
		PublisherType:  TopicEventSourceWebhook,
	})
	if err != nil {
		if errors.Is(err, ErrConflict) {
			return c.JSON(http.StatusConflict, map[string]string{"error": "idempotency key conflicts with existing payload"})
		}
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to store webhook event"})
	}
	if created.ID != eventID {
		return c.JSON(http.StatusAccepted, webhookAcceptedResponse{
			Accepted:      true,
			Topic:         created.Topic,
			EventID:       created.ID,
			Sequence:      created.Sequence,
			CorrelationID: created.CorrelationID,
		})
	}
	if created.Sequence != 0 {
		payload = buildWebhookPayload(c, created.ID, created.Sequence, topic, created.CorrelationID, created.IdempotencyKey, source, body)
		payloadJSON, err = marshalJSONCompact(payload)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to encode webhook payload"})
		}
		if err := s.configDB.UpdateEventPayload(c.Request().Context(), created.ID, payloadJSON); err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to store webhook event payload"})
		}
	}
	return c.JSON(http.StatusAccepted, webhookAcceptedResponse{
		Accepted:      true,
		Topic:         created.Topic,
		EventID:       created.ID,
		Sequence:      created.Sequence,
		CorrelationID: created.CorrelationID,
	})
}

func (s *Service) handleGetEvent(c echo.Context) error {
	eventID := strings.TrimSpace(c.Param("event_id"))
	item, err := s.configDB.GetEvent(c.Request().Context(), eventID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return c.JSON(http.StatusNotFound, map[string]string{"error": "event not found"})
		}
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to load event"})
	}
	return c.JSON(http.StatusOK, topicEventResponse{Event: toTopicEventJSON(item)})
}

func (s *Service) handleListEvents(c echo.Context) error {
	topic := strings.TrimSpace(c.QueryParam("topic"))
	if topic != "" {
		if err := validateTopicEventName(topic); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		}
	}
	correlationID := strings.TrimSpace(c.QueryParam("correlation_id"))
	if topic == "" && correlationID == "" {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "topic or correlation_id is required"})
	}
	afterSequence, err := parseOptionalInt64Query(c, "after_sequence")
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "after_sequence is invalid"})
	}
	limit, err := parseLimitQuery(c, 100, 500)
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "limit is invalid"})
	}
	items, err := s.configDB.ListEvents(c.Request().Context(), TopicEventFilter{
		Topic:         topic,
		CorrelationID: correlationID,
		AfterSequence: afterSequence,
		Limit:         limit,
	})
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to list events"})
	}
	resp := topicEventListResponse{Items: make([]topicEventJSON, 0, len(items))}
	for _, item := range items {
		resp.Items = append(resp.Items, toTopicEventJSON(item))
		if item.Sequence > resp.NextAfterSequence {
			resp.NextAfterSequence = item.Sequence
		}
	}
	return c.JSON(http.StatusOK, resp)
}

func (s *Service) handleGetEventSessions(c echo.Context) error {
	eventID := strings.TrimSpace(c.Param("event_id"))
	item, err := s.configDB.GetEvent(c.Request().Context(), eventID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return c.JSON(http.StatusNotFound, map[string]string{"error": "event not found"})
		}
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to load event"})
	}
	eventIDs, err := s.configDB.ListDescendantEventIDs(c.Request().Context(), eventID, 1000)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to trace event descendants"})
	}
	links, err := s.configDB.ListEventSessionLinks(c.Request().Context(), eventIDs)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to list event sessions"})
	}
	return c.JSON(http.StatusOK, eventSessionsResponse(webhooks.EventSessionsResponseFor(item, links)))
}

func (s *Service) handleGetEventRuns(c echo.Context) error {
	eventID := strings.TrimSpace(c.Param("event_id"))
	item, err := s.configDB.GetEvent(c.Request().Context(), eventID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return c.JSON(http.StatusNotFound, map[string]string{"error": "event not found"})
		}
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to load event"})
	}
	eventIDs, err := s.configDB.ListDescendantEventIDs(c.Request().Context(), eventID, 1000)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to trace event descendants"})
	}
	deliveries, err := s.configDB.ListEventDeliveries(c.Request().Context(), eventIDs)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to list event runs"})
	}
	return c.JSON(http.StatusOK, eventRunsResponse(webhooks.EventRunsResponseFor(item, deliveries)))
}

func (s *Service) handleListWebhookSources(c echo.Context) error {
	items, err := s.configDB.ListWebhookSources(c.Request().Context())
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to list webhook sources"})
	}
	resp := webhookSourceListResponse{Items: make([]webhookSourceJSON, 0, len(items))}
	for _, item := range items {
		resp.Items = append(resp.Items, toWebhookSourceJSON(item))
	}
	return c.JSON(http.StatusOK, resp)
}

func (s *Service) handlePutWebhookSource(c echo.Context) error {
	sourceID := strings.TrimSpace(c.Param("source_id"))
	if sourceID == "" {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "source_id is required"})
	}
	var req webhookSourceRequest
	if err := json.NewDecoder(c.Request().Body).Decode(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "body must be valid JSON"})
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	tokenHash := strings.TrimSpace(req.TokenHash)
	if existing, ok, err := s.configDB.GetWebhookSource(c.Request().Context(), sourceID); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to load webhook source"})
	} else if ok {
		tokenHash = existing.TokenHash
		if strings.TrimSpace(req.SignatureType) == "" {
			req.SignatureType = existing.SignatureType
		}
		if strings.TrimSpace(req.SignatureSecret) == "" {
			req.SignatureSecret = existing.SignatureSecret
		}
	}
	if req.ClearToken {
		tokenHash = ""
	}
	if strings.TrimSpace(req.Token) != "" {
		tokenHash = webhookTokenHash(req.Token)
	}
	if req.ClearSignature {
		req.SignatureSecret = ""
	}
	source, err := s.configDB.UpsertWebhookSource(c.Request().Context(), WebhookSource{
		ID:              sourceID,
		Name:            req.Name,
		Enabled:         enabled,
		Provider:        req.Provider,
		TopicPrefix:     req.TopicPrefix,
		TokenHash:       tokenHash,
		SignatureType:   req.SignatureType,
		SignatureSecret: req.SignatureSecret,
		BodyLimitBytes:  req.BodyLimitBytes,
	})
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
	}
	return c.JSON(http.StatusOK, webhookSourceResponse{Source: toWebhookSourceJSON(source)})
}

func (s *Service) handleDeleteWebhookSource(c echo.Context) error {
	sourceID := strings.TrimSpace(c.Param("source_id"))
	if sourceID == "" {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "source_id is required"})
	}
	if err := s.configDB.DeleteWebhookSource(c.Request().Context(), sourceID); err != nil {
		if errors.Is(err, ErrNotFound) {
			return c.JSON(http.StatusNotFound, map[string]string{"error": "webhook source not found"})
		}
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to delete webhook source"})
	}
	return c.NoContent(http.StatusNoContent)
}

func toWebhookSourceJSON(source WebhookSource) webhookSourceJSON {
	return webhooks.SourceToJSON(source)
}

func webhookTokenHash(token string) string {
	return webhooks.TokenHash(token)
}

func validWebhookTokenHash(r *http.Request, hash string) bool {
	return webhooks.ValidTokenHash(r, hash)
}

func (s *Service) authorizeWebhookRequest(c echo.Context, topic string) (WebhookSource, int64, bool, error) {
	defaultLimit := s.config.WebhookBodyLimitBytes
	sources, err := s.configDB.ListEnabledWebhookSourcesForTopic(c.Request().Context(), topic)
	if err != nil {
		return WebhookSource{}, 0, true, c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to load webhook sources"})
	}
	if len(sources) == 0 {
		return WebhookSource{}, 0, true, c.JSON(http.StatusNotFound, map[string]string{"error": "webhook source not found"})
	}
	matches := make([]WebhookSource, 0, 1)
	for _, source := range sources {
		if source.TokenHash != "" && validWebhookTokenHash(c.Request(), source.TokenHash) {
			matches = append(matches, source)
		}
	}
	if len(matches) == 0 {
		return WebhookSource{}, 0, true, c.JSON(http.StatusUnauthorized, map[string]string{"error": "invalid webhook source token"})
	}
	if len(matches) > 1 {
		return WebhookSource{}, 0, true, c.JSON(http.StatusConflict, map[string]string{"error": "webhook source is ambiguous"})
	}
	limit := matches[0].BodyLimitBytes
	if limit <= 0 {
		limit = defaultLimit
	}
	return matches[0], limit, false, nil
}

func readWebhookBody(r *http.Request, limit int64) ([]byte, error) {
	return webhooks.ReadBody(r, limit)
}

func requestContentTypeIsJSON(r *http.Request) bool {
	return webhooks.RequestContentTypeIsJSON(r)
}

func decodeWebhookJSONObject(raw []byte) (map[string]any, string, error) {
	return webhooks.DecodeJSONObject(raw)
}

func existingWebhookBodyHash(payloadJSON string) string {
	return webhooks.ExistingBodyHash(payloadJSON)
}

func validateExternalWebhookTopic(topic string) error {
	return webhooks.ValidateExternalTopic(topic)
}

func providerFromWebhookTopic(topic string) string {
	return webhooks.ProviderFromTopic(topic)
}

func intentFromWebhookBody(body map[string]any) string {
	return webhooks.IntentFromBody(body)
}

func extractCorrelationID(r *http.Request, body map[string]any) string {
	return webhooks.ExtractCorrelationID(r, body)
}

func extractIdempotencyKey(r *http.Request) string {
	return webhooks.ExtractIdempotencyKey(r)
}

func extractDeliveryID(r *http.Request) string {
	return webhooks.ExtractDeliveryID(r)
}

func buildWebhookPayload(c echo.Context, eventID string, sequence int64, topic, correlationID, idempotencyKey string, source WebhookSource, body map[string]any) map[string]any {
	return webhooks.BuildPayload(c.Request(), eventID, sequence, topic, correlationID, idempotencyKey, source, body)
}

func parseOptionalInt64Query(c echo.Context, name string) (int64, error) {
	raw := strings.TrimSpace(c.QueryParam(name))
	if raw == "" {
		return 0, nil
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value < 0 {
		return 0, fmt.Errorf("%s is invalid", name)
	}
	return value, nil
}

func parseLimitQuery(c echo.Context, defaultValue, maxValue int) (int, error) {
	raw := strings.TrimSpace(c.QueryParam("limit"))
	if raw == "" {
		return defaultValue, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value <= 0 || value > maxValue {
		return 0, fmt.Errorf("limit is invalid")
	}
	return value, nil
}

func toTopicEventJSON(item TopicEventRecord) topicEventJSON {
	return webhooks.TopicEventToJSON(item)
}

func newUUIDString() string {
	return uuid.NewString()
}
