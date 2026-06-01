package mailer

import (
	"context"
	"fmt"
	"net/mail"
	"net/smtp"
	"net/url"
	"strings"

	"niceagent/common/protocol"
)

type SMTPConfig struct {
	Host          string
	Port          int
	Username      string
	Password      string
	From          string
	PublicBaseURL string
}

type SMTPInvitationMailer struct {
	cfg SMTPConfig
}

func NewSMTPInvitationMailer(cfg SMTPConfig) *SMTPInvitationMailer {
	cfg.PublicBaseURL = strings.TrimRight(cfg.PublicBaseURL, "/")
	return &SMTPInvitationMailer{cfg: cfg}
}

func (m *SMTPInvitationMailer) SendInvitation(ctx context.Context, invitation protocol.Invitation) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if invitation.Email == "" || invitation.Token == "" {
		return fmt.Errorf("invitation email and token are required")
	}
	addr := fmt.Sprintf("%s:%d", m.cfg.Host, m.cfg.Port)
	var auth smtp.Auth
	if m.cfg.Username != "" || m.cfg.Password != "" {
		auth = smtp.PlainAuth("", m.cfg.Username, m.cfg.Password, m.cfg.Host)
	}
	from := m.cfg.From
	if parsed, err := mail.ParseAddress(m.cfg.From); err == nil {
		from = parsed.Address
	}
	return smtp.SendMail(addr, auth, from, []string{invitation.Email}, m.message(invitation))
}

func (m *SMTPInvitationMailer) message(invitation protocol.Invitation) []byte {
	acceptURL := fmt.Sprintf("%s/?invitation_token=%s", m.cfg.PublicBaseURL, url.QueryEscape(invitation.Token))
	subject := "NiceAgent invitation"
	body := fmt.Sprintf(
		"You have been invited to NiceAgent.\n\nRole: %s\nOrganization: %s\nProject: %s\nAccept: %s\n\nThis link expires at %s.\n",
		invitation.Role,
		invitation.OrganizationID,
		emptyAsDash(invitation.ProjectID),
		acceptURL,
		invitation.ExpiresAt.Format("2006-01-02 15:04:05 MST"),
	)
	headers := []string{
		"From: " + m.cfg.From,
		"To: " + invitation.Email,
		"Subject: " + subject,
		"MIME-Version: 1.0",
		"Content-Type: text/plain; charset=UTF-8",
	}
	return []byte(strings.Join(headers, "\r\n") + "\r\n\r\n" + body)
}

func emptyAsDash(value string) string {
	if strings.TrimSpace(value) == "" {
		return "-"
	}
	return value
}
