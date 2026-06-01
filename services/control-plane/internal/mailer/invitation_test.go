package mailer

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
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

func TestQueuedInvitationMailerEnqueuesAndRetries(t *testing.T) {
	inner := &flakyInvitationSender{failures: 1}
	mailer := NewQueuedInvitationMailer(inner, QueueConfig{
		Size:              2,
		Workers:           1,
		RetryAttempts:     2,
		RetryInitialDelay: time.Millisecond,
	}, slog.New(slog.NewTextHandler(testWriter{t: t}, nil)))
	defer mailer.Close()

	if err := mailer.SendInvitation(context.Background(), protocol.Invitation{
		ID:             "invitation-a",
		Email:          "invited@example.test",
		Token:          "token",
		OrganizationID: "org-a",
		Role:           "viewer",
	}); err != nil {
		t.Fatalf("enqueue invitation: %v", err)
	}

	deadline := time.After(time.Second)
	for {
		if inner.calls.Load() == 2 && inner.delivered.Load() == 1 {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("expected retry delivery, calls=%d delivered=%d", inner.calls.Load(), inner.delivered.Load())
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}
}

func TestQueuedInvitationMailerReturnsQueueFull(t *testing.T) {
	inner := &blockingInvitationSender{ready: make(chan struct{}), release: make(chan struct{})}
	mailer := NewQueuedInvitationMailer(inner, QueueConfig{Size: 1, Workers: 1, RetryAttempts: 1}, slog.New(slog.NewTextHandler(testWriter{t: t}, nil)))
	defer mailer.Close()

	if err := mailer.SendInvitation(context.Background(), protocol.Invitation{ID: "a", Email: "invited@example.test", Token: "token"}); err != nil {
		t.Fatalf("enqueue first invitation: %v", err)
	}
	<-inner.ready
	if err := mailer.SendInvitation(context.Background(), protocol.Invitation{ID: "b", Email: "invited@example.test", Token: "token"}); err != nil {
		t.Fatalf("enqueue second invitation: %v", err)
	}
	if err := mailer.SendInvitation(context.Background(), protocol.Invitation{ID: "c", Email: "invited@example.test", Token: "token"}); !errors.Is(err, ErrInvitationQueueFull) {
		t.Fatalf("expected queue full, got %v", err)
	}
	close(inner.release)
}

type flakyInvitationSender struct {
	failures  int
	calls     atomic.Int32
	delivered atomic.Int32
}

func (s *flakyInvitationSender) SendInvitation(context.Context, protocol.Invitation) error {
	call := int(s.calls.Add(1))
	if call <= s.failures {
		return errors.New("temporary smtp failure")
	}
	s.delivered.Add(1)
	return nil
}

type blockingInvitationSender struct {
	once    sync.Once
	ready   chan struct{}
	release chan struct{}
}

func (s *blockingInvitationSender) SendInvitation(context.Context, protocol.Invitation) error {
	s.once.Do(func() { close(s.ready) })
	<-s.release
	return nil
}

type testWriter struct {
	t *testing.T
}

func (w testWriter) Write(p []byte) (int, error) {
	w.t.Log(strings.TrimSpace(string(p)))
	return len(p), nil
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
