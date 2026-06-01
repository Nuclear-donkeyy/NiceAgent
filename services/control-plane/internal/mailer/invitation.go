package mailer

import (
	"context"
	"fmt"
	"net/mail"
	"net/smtp"
	"net/url"
	"strings"
	"text/template"

	"niceagent/common/protocol"
)

type SMTPConfig struct {
	Host            string
	Port            int
	Username        string
	Password        string
	From            string
	PublicBaseURL   string
	SubjectTemplate string
	BodyTemplate    string
}

type SMTPInvitationMailer struct {
	cfg SMTPConfig
}

func NewSMTPInvitationMailer(cfg SMTPConfig) *SMTPInvitationMailer {
	cfg.PublicBaseURL = strings.TrimRight(cfg.PublicBaseURL, "/")
	cfg.SubjectTemplate = strings.TrimSpace(firstNonEmpty(cfg.SubjectTemplate, DefaultInvitationSubjectTemplate))
	cfg.BodyTemplate = strings.TrimSpace(firstNonEmpty(cfg.BodyTemplate, DefaultInvitationBodyTemplate))
	return &SMTPInvitationMailer{cfg: cfg}
}

const (
	DefaultInvitationSubjectTemplate = "NiceAgent invitation"
	DefaultInvitationBodyTemplate    = "You have been invited to NiceAgent.\n\nRole: {{.Role}}\nOrganization: {{.OrganizationID}}\nProject: {{.ProjectIDOrDash}}\nAccept: {{.AcceptURL}}\n\nThis link expires at {{.ExpiresAt}}.\n"
)

type InvitationTemplateData struct {
	Email           string
	Role            string
	OrganizationID  string
	ProjectID       string
	ProjectIDOrDash string
	AcceptURL       string
	ExpiresAt       string
}

func ValidateTemplates(subjectTemplate, bodyTemplate string) error {
	sample := InvitationTemplateData{
		Email:           "invited@example.test",
		Role:            "viewer",
		OrganizationID:  "org",
		ProjectID:       "project",
		ProjectIDOrDash: "project",
		AcceptURL:       "https://app.example.test/?invitation_token=token",
		ExpiresAt:       "2026-06-01 12:00:00 UTC",
	}
	if _, err := executeTemplate("invitation_subject", firstNonEmpty(subjectTemplate, DefaultInvitationSubjectTemplate), sample); err != nil {
		return fmt.Errorf("invalid invitation subject template: %w", err)
	}
	if _, err := executeTemplate("invitation_body", firstNonEmpty(bodyTemplate, DefaultInvitationBodyTemplate), sample); err != nil {
		return fmt.Errorf("invalid invitation body template: %w", err)
	}
	return nil
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
	data := InvitationTemplateData{
		Email:           invitation.Email,
		Role:            invitation.Role,
		OrganizationID:  invitation.OrganizationID,
		ProjectID:       invitation.ProjectID,
		ProjectIDOrDash: emptyAsDash(invitation.ProjectID),
		AcceptURL:       acceptURL,
		ExpiresAt:       invitation.ExpiresAt.Format("2006-01-02 15:04:05 MST"),
	}
	subject := sanitizeHeaderValue(renderTemplate("invitation_subject", m.cfg.SubjectTemplate, data))
	body := renderTemplate("invitation_body", m.cfg.BodyTemplate, data)
	headers := []string{
		"From: " + m.cfg.From,
		"To: " + invitation.Email,
		"Subject: " + subject,
		"MIME-Version: 1.0",
		"Content-Type: text/plain; charset=UTF-8",
	}
	return []byte(strings.Join(headers, "\r\n") + "\r\n\r\n" + body)
}

func renderTemplate(name, source string, data InvitationTemplateData) string {
	rendered, err := executeTemplate(name, source, data)
	if err != nil {
		return source
	}
	return rendered
}

func executeTemplate(name, source string, data InvitationTemplateData) (string, error) {
	parsed, err := parseTemplate(name, source)
	if err != nil {
		return "", err
	}
	var builder strings.Builder
	if err := parsed.Execute(&builder, data); err != nil {
		return "", err
	}
	return builder.String(), nil
}

func parseTemplate(name, source string) (*template.Template, error) {
	return template.New(name).Option("missingkey=error").Parse(source)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func emptyAsDash(value string) string {
	if strings.TrimSpace(value) == "" {
		return "-"
	}
	return value
}

func sanitizeHeaderValue(value string) string {
	value = strings.ReplaceAll(value, "\r", " ")
	value = strings.ReplaceAll(value, "\n", " ")
	return strings.TrimSpace(value)
}
