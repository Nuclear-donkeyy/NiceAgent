package protocol

import "time"

type User struct {
	ID        string    `json:"id"`
	Email     string    `json:"email"`
	Name      string    `json:"name"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
}

type UserIdentity struct {
	ID        string    `json:"id"`
	UserID    string    `json:"user_id"`
	Provider  string    `json:"provider"`
	Issuer    string    `json:"issuer"`
	Subject   string    `json:"subject"`
	Email     string    `json:"email,omitempty"`
	Name      string    `json:"name,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type Organization struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
}

type Project struct {
	ID             string    `json:"id"`
	OrganizationID string    `json:"organization_id"`
	Name           string    `json:"name"`
	CreatedAt      time.Time `json:"created_at"`
}

type OrganizationMember struct {
	ID             string    `json:"id"`
	UserID         string    `json:"user_id"`
	OrganizationID string    `json:"organization_id"`
	Role           string    `json:"role"`
	Email          string    `json:"email,omitempty"`
	Name           string    `json:"name,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

type OrganizationMemberInput struct {
	UserID string `json:"user_id"`
	Email  string `json:"email,omitempty"`
	Name   string `json:"name,omitempty"`
	Role   string `json:"role"`
}

type OrganizationMembersResponse struct {
	Members []OrganizationMember `json:"members"`
}

type OrganizationMemberResponse struct {
	Member OrganizationMember `json:"member"`
}

type InvitationStatus string

const (
	InvitationPending  InvitationStatus = "pending"
	InvitationAccepted InvitationStatus = "accepted"
	InvitationExpired  InvitationStatus = "expired"
)

type Invitation struct {
	ID               string           `json:"id"`
	Token            string           `json:"token,omitempty"`
	OrganizationID   string           `json:"organization_id"`
	ProjectID        string           `json:"project_id,omitempty"`
	Email            string           `json:"email"`
	Role             string           `json:"role"`
	Status           InvitationStatus `json:"status"`
	InvitedByUserID  string           `json:"invited_by_user_id"`
	AcceptedByUserID string           `json:"accepted_by_user_id,omitempty"`
	CreatedAt        time.Time        `json:"created_at"`
	ExpiresAt        time.Time        `json:"expires_at"`
	AcceptedAt       *time.Time       `json:"accepted_at,omitempty"`
}

type InvitationInput struct {
	Email          string `json:"email"`
	Role           string `json:"role"`
	ProjectID      string `json:"project_id,omitempty"`
	ExpiresInHours int    `json:"expires_in_hours,omitempty"`
}

type InvitationAcceptInput struct {
	Name string `json:"name,omitempty"`
}

type InvitationsResponse struct {
	Invitations []Invitation `json:"invitations"`
}

type InvitationResponse struct {
	Invitation Invitation `json:"invitation"`
}

type ProjectMember struct {
	ID        string    `json:"id"`
	UserID    string    `json:"user_id"`
	ProjectID string    `json:"project_id"`
	Role      string    `json:"role"`
	Email     string    `json:"email,omitempty"`
	Name      string    `json:"name,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type ProjectMemberInput struct {
	UserID string `json:"user_id"`
	Email  string `json:"email,omitempty"`
	Name   string `json:"name,omitempty"`
	Role   string `json:"role"`
}

type ProjectMembersResponse struct {
	Members []ProjectMember `json:"members"`
}

type ProjectMemberResponse struct {
	Member ProjectMember `json:"member"`
}
