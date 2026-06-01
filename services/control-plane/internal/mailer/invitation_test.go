package mailer

import (
	"strings"
	"testing"
	"time"

	"niceagent/common/protocol"
)

func TestSMTPInvitationMailerBuildsAcceptLink(t *testing.T) {
	m := NewSMTPInvitationMailer(SMTPConfig{
		Host:          "smtp.example.test",
		Port:          587,
		From:          "noreply@example.test",
		PublicBaseURL: "https://app.example.test/",
	})
	message := string(m.message(protocol.Invitation{
		Token:          "invite-token",
		Email:          "invited@example.test",
		OrganizationID: "org-a",
		ProjectID:      "project-a",
		Role:           "viewer",
		ExpiresAt:      time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC),
	}))

	for _, want := range []string{
		"To: invited@example.test",
		"Subject: NiceAgent invitation",
		"https://app.example.test/?invitation_token=invite-token",
		"Role: viewer",
		"Project: project-a",
	} {
		if !strings.Contains(message, want) {
			t.Fatalf("message missing %q:\n%s", want, message)
		}
	}
}
