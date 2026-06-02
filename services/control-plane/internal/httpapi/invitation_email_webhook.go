package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"niceagent/common/protocol"
)

var errNoInvitationEmailEvent = errors.New("no invitation email event found")

func normalizeInvitationEmailWebhookInputs(body []byte) ([]protocol.InvitationEmailEventInput, error) {
	var neutral protocol.InvitationEmailEventInput
	if err := json.Unmarshal(body, &neutral); err == nil &&
		strings.TrimSpace(neutral.Type) != "" &&
		(strings.TrimSpace(neutral.InvitationID) != "" || strings.TrimSpace(neutral.DeliveryID) != "") {
		return []protocol.InvitationEmailEventInput{neutral}, nil
	}

	var raw any
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, err
	}
	events := normalizeInvitationEmailWebhookValue(raw, raw)
	if len(events) == 0 {
		return nil, errNoInvitationEmailEvent
	}
	return events, nil
}

func normalizeInvitationEmailWebhookValue(value any, fullPayload any) []protocol.InvitationEmailEventInput {
	switch typed := value.(type) {
	case []any:
		events := make([]protocol.InvitationEmailEventInput, 0, len(typed))
		for _, item := range typed {
			events = append(events, normalizeInvitationEmailWebhookValue(item, item)...)
		}
		return events
	case map[string]any:
		if snsMessage := strings.TrimSpace(stringFromPath(typed, "Message")); snsMessage != "" && strings.HasPrefix(snsMessage, "{") {
			var nested any
			if err := json.Unmarshal([]byte(snsMessage), &nested); err == nil {
				return normalizeInvitationEmailWebhookValue(nested, typed)
			}
		}
		if event, ok := normalizeSendGridInvitationEmailEvent(typed, fullPayload); ok {
			return []protocol.InvitationEmailEventInput{event}
		}
		if event, ok := normalizeSESEmailEvent(typed, fullPayload); ok {
			return []protocol.InvitationEmailEventInput{event}
		}
		if event, ok := normalizeMailgunInvitationEmailEvent(typed, fullPayload); ok {
			return []protocol.InvitationEmailEventInput{event}
		}
	}
	return nil
}

func normalizeSendGridInvitationEmailEvent(payload map[string]any, fullPayload any) (protocol.InvitationEmailEventInput, bool) {
	rawType := strings.ToLower(strings.TrimSpace(stringFromPath(payload, "event")))
	eventType := mapProviderEmailEventType(rawType)
	if eventType == "" {
		return protocol.InvitationEmailEventInput{}, false
	}
	input := baseInvitationEmailEventInput("sendgrid", payload, fullPayload)
	input.Type = string(eventType)
	input.ProviderMessageID = firstNonEmptyHTTPAPI(
		stringFromPath(payload, "sg_message_id"),
		stringFromPath(payload, "smtp-id"),
		stringFromPath(payload, "message_id"),
	)
	input.Reason = firstNonEmptyHTTPAPI(
		stringFromPath(payload, "reason"),
		stringFromPath(payload, "response"),
		stringFromPath(payload, "status"),
	)
	input.OccurredAt = unixTimeFromPath(payload, "timestamp")
	input.InvitationID = firstNonEmptyHTTPAPI(input.InvitationID, metadataValue(payload, "invitation_id"))
	input.DeliveryID = firstNonEmptyHTTPAPI(input.DeliveryID, metadataValue(payload, "delivery_id"))
	return input, true
}

func normalizeSESEmailEvent(payload map[string]any, fullPayload any) (protocol.InvitationEmailEventInput, bool) {
	rawType := firstNonEmptyHTTPAPI(
		stringFromPath(payload, "notificationType"),
		stringFromPath(payload, "eventType"),
	)
	eventType := mapProviderEmailEventType(rawType)
	if eventType == "" {
		return protocol.InvitationEmailEventInput{}, false
	}
	input := baseInvitationEmailEventInput("ses", payload, fullPayload)
	input.Type = string(eventType)
	input.ProviderMessageID = firstNonEmptyHTTPAPI(
		stringFromPath(payload, "mail.messageId"),
		stringFromPath(payload, "mail.commonHeaders.messageId"),
	)
	input.OccurredAt = timeFromAny(firstNonEmptyHTTPAPI(
		stringFromPath(payload, "bounce.timestamp"),
		stringFromPath(payload, "complaint.timestamp"),
		stringFromPath(payload, "delivery.timestamp"),
		stringFromPath(payload, "mail.timestamp"),
	))
	input.Reason = firstNonEmptyHTTPAPI(
		stringFromPath(payload, "bounce.bounceType"),
		stringFromPath(payload, "complaint.complaintFeedbackType"),
		stringFromPath(payload, "delivery.smtpResponse"),
	)
	if diagnostic := firstRecipientField(payload, "bounce.bouncedRecipients", "diagnosticCode"); diagnostic != "" {
		input.Reason = firstNonEmptyHTTPAPI(diagnostic, input.Reason)
	}
	input.InvitationID = firstNonEmptyHTTPAPI(input.InvitationID, sesTagValue(payload, "niceagent_invitation_id"), sesTagValue(payload, "invitation_id"))
	input.DeliveryID = firstNonEmptyHTTPAPI(input.DeliveryID, sesTagValue(payload, "niceagent_delivery_id"), sesTagValue(payload, "delivery_id"))
	return input, true
}

func normalizeMailgunInvitationEmailEvent(payload map[string]any, fullPayload any) (protocol.InvitationEmailEventInput, bool) {
	eventData, _ := mapFromPath(payload, "event-data")
	if eventData == nil {
		eventData = payload
	}
	rawType := firstNonEmptyHTTPAPI(
		stringFromPath(eventData, "event"),
		stringFromPath(payload, "event"),
	)
	eventType := mapProviderEmailEventType(rawType)
	if eventType == "" {
		return protocol.InvitationEmailEventInput{}, false
	}
	input := baseInvitationEmailEventInput("mailgun", eventData, fullPayload)
	input.Type = string(eventType)
	input.ProviderMessageID = firstNonEmptyHTTPAPI(
		stringFromPath(eventData, "message.headers.message-id"),
		stringFromPath(eventData, "message-id"),
		stringFromPath(payload, "Message-Id"),
	)
	input.Reason = firstNonEmptyHTTPAPI(
		stringFromPath(eventData, "reason"),
		stringFromPath(eventData, "delivery-status.message"),
		stringFromPath(eventData, "severity"),
	)
	input.OccurredAt = unixTimeFromPath(eventData, "timestamp")
	input.InvitationID = firstNonEmptyHTTPAPI(input.InvitationID, metadataValue(eventData, "invitation_id"))
	input.DeliveryID = firstNonEmptyHTTPAPI(input.DeliveryID, metadataValue(eventData, "delivery_id"))
	return input, true
}

func baseInvitationEmailEventInput(provider string, eventPayload map[string]any, fullPayload any) protocol.InvitationEmailEventInput {
	payload := map[string]any{
		"provider_payload": fullPayload,
	}
	return protocol.InvitationEmailEventInput{
		InvitationID: strings.TrimSpace(firstNonEmptyHTTPAPI(
			stringFromPath(eventPayload, "invitation_id"),
			metadataValue(eventPayload, "invitation_id"),
		)),
		DeliveryID: strings.TrimSpace(firstNonEmptyHTTPAPI(
			stringFromPath(eventPayload, "delivery_id"),
			metadataValue(eventPayload, "delivery_id"),
		)),
		Provider: strings.TrimSpace(firstNonEmptyHTTPAPI(
			stringFromPath(eventPayload, "provider"),
			provider,
		)),
		Reason:  strings.TrimSpace(stringFromPath(eventPayload, "reason")),
		Payload: payload,
	}
}

func mapProviderEmailEventType(raw string) protocol.InvitationEmailEventType {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "delivered", "delivery", "processed":
		return protocol.InvitationEmailEventDelivered
	case "bounce", "bounced", "blocked", "deferred", "failed", "failure", "permanent_fail", "reject", "rendering failure":
		return protocol.InvitationEmailEventBounced
	case "complaint", "complained", "spamreport", "spam_report":
		return protocol.InvitationEmailEventComplaint
	case "dropped", "drop", "suppressed", "unsubscribe", "group_unsubscribe", "group-resubscribe":
		return protocol.InvitationEmailEventDropped
	default:
		return ""
	}
}

func metadataValue(payload map[string]any, key string) string {
	for _, path := range []string{
		key,
		"custom_args." + key,
		"unique_args." + key,
		"metadata." + key,
		"user-variables." + key,
		"user_variables." + key,
		"variables." + key,
		"v:" + key,
	} {
		if value := stringFromPath(payload, path); value != "" {
			return value
		}
	}
	return ""
}

func sesTagValue(payload map[string]any, key string) string {
	value := valueFromPath(payload, "mail.tags."+key)
	switch typed := value.(type) {
	case []any:
		if len(typed) > 0 {
			return valueToString(typed[0])
		}
	case []string:
		if len(typed) > 0 {
			return strings.TrimSpace(typed[0])
		}
	default:
		return valueToString(typed)
	}
	return ""
}

func firstRecipientField(payload map[string]any, path, field string) string {
	value := valueFromPath(payload, path)
	recipients, ok := value.([]any)
	if !ok || len(recipients) == 0 {
		return ""
	}
	recipient, ok := recipients[0].(map[string]any)
	if !ok {
		return ""
	}
	return stringFromPath(recipient, field)
}

func mapFromPath(payload map[string]any, path string) (map[string]any, bool) {
	value := valueFromPath(payload, path)
	mapped, ok := value.(map[string]any)
	return mapped, ok
}

func stringFromPath(payload map[string]any, path string) string {
	return valueToString(valueFromPath(payload, path))
}

func valueFromPath(payload map[string]any, path string) any {
	var current any = payload
	for _, part := range strings.Split(path, ".") {
		mapped, ok := current.(map[string]any)
		if !ok {
			return nil
		}
		current = mapped[part]
	}
	return current
}

func valueToString(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(typed)
	case json.Number:
		return strings.TrimSpace(typed.String())
	case float64:
		return strings.TrimSpace(strconv.FormatFloat(typed, 'f', -1, 64))
	case int:
		return strconv.Itoa(typed)
	case int64:
		return strconv.FormatInt(typed, 10)
	case bool:
		if typed {
			return "true"
		}
		return "false"
	default:
		return strings.TrimSpace(fmt.Sprint(typed))
	}
}

func unixTimeFromPath(payload map[string]any, path string) *time.Time {
	value := valueFromPath(payload, path)
	switch typed := value.(type) {
	case float64:
		return unixTime(typed)
	case json.Number:
		parsed, err := strconv.ParseFloat(typed.String(), 64)
		if err == nil {
			return unixTime(parsed)
		}
	case string:
		if parsed, err := strconv.ParseFloat(strings.TrimSpace(typed), 64); err == nil {
			return unixTime(parsed)
		}
		return timeFromAny(typed)
	}
	return nil
}

func unixTime(seconds float64) *time.Time {
	if seconds <= 0 {
		return nil
	}
	sec := int64(seconds)
	nsec := int64((seconds - float64(sec)) * 1e9)
	occurredAt := time.Unix(sec, nsec).UTC()
	return &occurredAt
}

func timeFromAny(raw string) *time.Time {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
		parsed, err := time.Parse(layout, raw)
		if err == nil {
			occurredAt := parsed.UTC()
			return &occurredAt
		}
	}
	return nil
}

func firstNonEmptyHTTPAPI(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
