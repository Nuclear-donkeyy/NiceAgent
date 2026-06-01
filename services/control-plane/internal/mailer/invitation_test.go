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

func TestSMTPInvitationMailerUsesCustomTemplates(t *testing.T) {
	m := NewSMTPInvitationMailer(SMTPConfig{
		Host:            "smtp.example.test",
		Port:            587,
		From:            "noreply@example.test",
		PublicBaseURL:   "https://app.example.test",
		SubjectTemplate: "Join {{.OrganizationID}} as {{.Role}}",
		BodyTemplate:    "Hello {{.Email}}, accept {{.AcceptURL}} before {{.ExpiresAt}}.",
	})
	message := string(m.message(protocol.Invitation{
		Token:          "invite token",
		Email:          "invited@example.test",
		OrganizationID: "org-a",
		Role:           "admin",
		ExpiresAt:      time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC),
	}))

	for _, want := range []string{
		"Subject: Join org-a as admin",
		"Hello invited@example.test",
		"https://app.example.test/?invitation_token=invite+token",
	} {
		if !strings.Contains(message, want) {
			t.Fatalf("message missing %q:\n%s", want, message)
		}
	}
}

func TestValidateTemplatesRejectsUnknownFields(t *testing.T) {
	if err := ValidateTemplates("Join {{.Missing}}", "Body"); err == nil {
		t.Fatal("expected invalid subject template to fail")
	}
	if err := ValidateTemplates("Subject", "Body {{.Missing}}"); err == nil {
		t.Fatal("expected invalid body template to fail")
	}
}

func TestSMTPInvitationMailerSanitizesSubjectHeader(t *testing.T) {
	m := NewSMTPInvitationMailer(SMTPConfig{
		Host:            "smtp.example.test",
		Port:            587,
		From:            "noreply@example.test",
		PublicBaseURL:   "https://app.example.test",
		SubjectTemplate: "Join\r\nBcc: attacker@example.test",
	})
	message := string(m.message(protocol.Invitation{
		Token:          "invite-token",
		Email:          "invited@example.test",
		OrganizationID: "org-a",
		Role:           "admin",
		ExpiresAt:      time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC),
	}))

	if strings.Contains(message, "\r\nBcc: attacker@example.test") {
		t.Fatalf("subject header injection was not sanitized:\n%s", message)
	}
	if !strings.Contains(message, "Subject: Join  Bcc: attacker@example.test") {
		t.Fatalf("sanitized subject not found:\n%s", message)
	}
}
