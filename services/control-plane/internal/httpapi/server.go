package httpapi

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"niceagent/common/platform"
	"niceagent/common/protocol"
	"niceagent/common/skillmanifest"
	"niceagent/control-plane/internal/app"
	quotapkg "niceagent/control-plane/internal/quota"
)

type Server struct {
	repo                    app.Repository
	dispatcher              app.RunDispatcher
	log                     *slog.Logger
	metrics                 *platform.Metrics
	authMode                string
	oidcVerifier            *OIDCVerifier
	controlPlaneURL         string
	internalToken           string
	invitationWebhookSecret string
	invitationMailer        app.InvitationMailer
	runQuota                RunQuota
	quotaLimiter            quotapkg.Limiter
	tokenReservation        TokenReservationOptions
}

type ServerOptions struct {
	AuthMode                string
	ControlPlanePublicURL   string
	InternalAPIToken        string
	InvitationWebhookSecret string
	InvitationMailer        app.InvitationMailer
	RunQuota                RunQuota
	QuotaLimiter            quotapkg.Limiter
	TokenReservation        TokenReservationOptions
	OIDC                    OIDCConfig
}

const defaultAttemptLeaseSeconds = 600

type RunQuota struct {
	MaxConcurrentRuns       int
	MaxRunsPerHour          int
	MaxTokensPerDay         int
	MaxToolCallsPerDay      int
	MaxSandboxSecondsPerDay int
}

type TokenReservationOptions struct {
	Mode         string
	OutputBuffer int
	Model        string
}

func NewServer(repo app.Repository, dispatcher app.RunDispatcher, log *slog.Logger) *Server {
	return NewServerWithOptions(repo, dispatcher, log, ServerOptions{})
}

func NewServerWithOptions(repo app.Repository, dispatcher app.RunDispatcher, log *slog.Logger, opts ServerOptions) *Server {
	authMode := normalizeAuthMode(opts.AuthMode)
	var oidcVerifier *OIDCVerifier
	if authMode == "oidc" {
		verifier, err := NewOIDCVerifier(opts.OIDC)
		if err != nil {
			panic(err)
		}
		oidcVerifier = verifier
	}
	return &Server{
		repo:                    repo,
		dispatcher:              dispatcher,
		log:                     log,
		metrics:                 platform.NewMetrics("control_plane"),
		authMode:                authMode,
		oidcVerifier:            oidcVerifier,
		controlPlaneURL:         strings.TrimRight(opts.ControlPlanePublicURL, "/"),
		internalToken:           internalToken(opts.InternalAPIToken),
		invitationWebhookSecret: opts.InvitationWebhookSecret,
		invitationMailer:        opts.InvitationMailer,
		runQuota:                opts.RunQuota,
		quotaLimiter:            opts.QuotaLimiter,
		tokenReservation:        normalizeTokenReservationOptions(opts.TokenReservation),
	}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", platform.Method(http.MethodGet, s.health))
	mux.Handle("/metrics", s.metrics.Handler())
	mux.HandleFunc("/api/chats", s.chats)
	mux.HandleFunc("/api/chats/", s.chatSubroutes)
	mux.HandleFunc("/api/runs/", s.runSubroutes)
	mux.HandleFunc("/api/artifacts/", s.artifactSubroutes)
	mux.HandleFunc("/api/invitations/", s.invitationSubroutes)
	mux.HandleFunc("/api/organizations/", s.organizationSubroutes)
	mux.HandleFunc("/api/projects/", s.projectSubroutes)
	mux.HandleFunc("/api/skills", platform.Method(http.MethodGet, s.skills))
	mux.HandleFunc("/api/skills/", s.skillSubroutes)
	mux.HandleFunc("/api/audit/events", platform.Method(http.MethodGet, s.auditEvents))
	mux.HandleFunc("/webhooks/invitation-email-events", platform.Method(http.MethodPost, s.invitationEmailWebhook))
	mux.HandleFunc("/internal/runs/", s.internalRunSubroutes)
	mux.HandleFunc("/internal/artifacts/", s.internalArtifactSubroutes)
	mux.Handle("/", http.FileServer(http.Dir(staticDir())))
	handler := platform.WithTraceID(requestLogger(s.log, s.authMode, platform.MetricsMiddleware(s.metrics, s.withActor(mux))))
	return platform.WithRequestID(platform.OpenTelemetryMiddleware("control_plane", handler))
}

func staticDir() string {
	if dir := os.Getenv("WEB_DIST_DIR"); dir != "" {
		return dir
	}
	return "../../frontend/dist"
}

func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	platform.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) chats(w http.ResponseWriter, r *http.Request) {
	actor := actorFromRequest(r)
	switch r.Method {
	case http.MethodGet:
		opts := app.ChatListOptions{
			Query:           r.URL.Query().Get("q"),
			IncludeArchived: parseBool(r.URL.Query().Get("include_archived")),
		}
		platform.WriteJSON(w, http.StatusOK, map[string]any{"chats": s.repo.ListChats(actor.UserID, actor.ProjectID, opts)})
	case http.MethodPost:
		if !s.requireWriteRole(w, r, "chat.create", "chat", "", "") {
			return
		}
		var input struct {
			Title string `json:"title"`
		}
		_ = json.NewDecoder(r.Body).Decode(&input)
		chat, err := s.repo.CreateChat(actor.UserID, actor.ProjectID, input.Title)
		if err != nil {
			writeStoreErr(w, err)
			return
		}
		s.auditAllow(r, "chat.create", "chat", chat.ID, "", map[string]any{"title": chat.Title})
		platform.WriteJSON(w, http.StatusCreated, chat)
	default:
		platform.WriteError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Server) chatSubroutes(w http.ResponseWriter, r *http.Request) {
	actor := actorFromRequest(r)
	parts := splitPath(strings.TrimPrefix(r.URL.Path, "/api/chats/"))
	if len(parts) == 0 {
		platform.WriteError(w, http.StatusNotFound, "chat route not found")
		return
	}
	chatID := parts[0]
	if len(parts) == 1 && r.Method == http.MethodGet {
		chat, messages, err := s.repo.GetChat(chatID)
		if err != nil {
			writeStoreErr(w, err)
			return
		}
		if !actorCanReadChat(actor, chat) {
			s.auditDeny(r, "chat.read", "chat", chatID, "", "actor cannot read chat", nil)
			platform.WriteError(w, http.StatusNotFound, "not found")
			return
		}
		platform.WriteJSON(w, http.StatusOK, map[string]any{"chat": chat, "messages": messages})
		return
	}
	if len(parts) == 2 && parts[1] == "messages" && r.Method == http.MethodPost {
		s.createMessage(w, r, chatID)
		return
	}
	if len(parts) == 2 && parts[1] == "archive" && r.Method == http.MethodPost {
		if !s.actorCanReadChatID(actor, chatID) {
			s.auditDeny(r, "chat.archive", "chat", chatID, "", "actor cannot archive chat", nil)
			platform.WriteError(w, http.StatusNotFound, "not found")
			return
		}
		if !s.requireWriteRole(w, r, "chat.archive", "chat", chatID, "") {
			return
		}
		chat, err := s.repo.SetChatArchived(chatID, actor.UserID, true)
		if err != nil {
			writeStoreErr(w, err)
			return
		}
		s.auditAllow(r, "chat.archive", "chat", chat.ID, "", nil)
		platform.WriteJSON(w, http.StatusOK, chat)
		return
	}
	if len(parts) == 2 && parts[1] == "restore" && r.Method == http.MethodPost {
		if !s.actorCanReadChatID(actor, chatID) {
			s.auditDeny(r, "chat.restore", "chat", chatID, "", "actor cannot restore chat", nil)
			platform.WriteError(w, http.StatusNotFound, "not found")
			return
		}
		if !s.requireWriteRole(w, r, "chat.restore", "chat", chatID, "") {
			return
		}
		chat, err := s.repo.SetChatArchived(chatID, actor.UserID, false)
		if err != nil {
			writeStoreErr(w, err)
			return
		}
		s.auditAllow(r, "chat.restore", "chat", chat.ID, "", nil)
		platform.WriteJSON(w, http.StatusOK, chat)
		return
	}
	platform.WriteError(w, http.StatusNotFound, "chat route not found")
}

func (s *Server) createMessage(w http.ResponseWriter, r *http.Request, chatID string) {
	actor := actorFromRequest(r)
	var input struct {
		Content string `json:"content"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		platform.WriteError(w, http.StatusBadRequest, "invalid json body")
		return
	}
	input.Content = strings.TrimSpace(input.Content)
	if input.Content == "" {
		platform.WriteError(w, http.StatusBadRequest, "content is required")
		return
	}
	if !s.actorCanReadChatID(actor, chatID) {
		s.auditDeny(r, "message.create", "chat", chatID, "", "actor cannot write chat", nil)
		platform.WriteError(w, http.StatusNotFound, "not found")
		return
	}
	if !s.requireWriteRole(w, r, "message.create", "chat", chatID, "") {
		return
	}
	reservation, denied, quotaMessage, metadata, err := s.reserveRunQuota(r.Context(), actor, input.Content)
	if err != nil {
		s.auditDeny(r, "quota.run.create", "run", chatID, "", "quota backend unavailable", map[string]any{"error": err.Error()})
		platform.WriteError(w, http.StatusServiceUnavailable, "quota backend unavailable")
		return
	}
	if denied {
		s.metrics.IncCounter("niceagent_quota_denials_total", platform.Labels{"quota": fmt.Sprint(metadata["quota"])})
		s.auditDeny(r, "quota.run.create", "run", chatID, "", quotaMessage, metadata)
		platform.WriteError(w, http.StatusTooManyRequests, quotaMessage)
		return
	}
	rollbackReservation := true
	defer func() {
		if rollbackReservation && reservation != nil {
			_ = reservation.Rollback(context.Background())
		}
	}()
	message, run, err := s.repo.AddUserMessage(chatID, actor.UserID, input.Content)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	if reservation != nil {
		if err := reservation.Commit(r.Context(), run.ID); err != nil {
			_, _ = s.repo.UpdateRunStatus(run.ID, protocol.RunFailed, err.Error())
			_, _ = s.repo.AddEvent(run.ID, protocol.EventRunFailed, "Run quota reservation failed.", map[string]any{"error": err.Error()})
			s.auditDeny(r, "quota.run.create", "run", run.ID, run.ID, "quota reservation failed", map[string]any{"error": err.Error()})
			platform.WriteError(w, http.StatusServiceUnavailable, "quota reservation failed")
			return
		}
	}
	_, _ = s.repo.AddEvent(run.ID, protocol.EventRunQueued, "Run queued by control plane.", map[string]any{"message_id": message.ID})
	s.auditAllow(r, "run.create", "run", run.ID, run.ID, map[string]any{"chat_id": chatID, "message_id": message.ID})
	s.metrics.IncCounter("niceagent_runs_created_total", platform.Labels{"project_id": actor.ProjectID})
	if err := s.dispatcher.Dispatch(r.Context(), run, input.Content); err != nil {
		_, _ = s.repo.UpdateRunStatus(run.ID, protocol.RunFailed, err.Error())
		_, _ = s.repo.AddEvent(run.ID, protocol.EventRunFailed, "Run dispatch failed.", map[string]any{"error": err.Error()})
		s.auditDeny(r, "run.dispatch", "run", run.ID, run.ID, "run dispatch failed", map[string]any{"error_class": "dispatch_failed"})
		platform.WriteError(w, http.StatusServiceUnavailable, "run dispatch failed")
		return
	}
	rollbackReservation = false
	platform.WriteJSON(w, http.StatusAccepted, map[string]any{"message": message, "run": run})
}

func (s *Server) runSubroutes(w http.ResponseWriter, r *http.Request) {
	parts := splitPath(strings.TrimPrefix(r.URL.Path, "/api/runs/"))
	if len(parts) == 0 {
		platform.WriteError(w, http.StatusNotFound, "run route not found")
		return
	}
	runID := parts[0]
	if len(parts) == 1 && r.Method == http.MethodGet {
		if !s.actorCanReadRun(actorFromRequest(r), runID) {
			s.auditDeny(r, "run.read", "run", runID, runID, "actor cannot read run", nil)
			platform.WriteError(w, http.StatusNotFound, "not found")
			return
		}
		run, err := s.repo.GetRun(runID)
		if err != nil {
			writeStoreErr(w, err)
			return
		}
		platform.WriteJSON(w, http.StatusOK, run)
		return
	}
	if len(parts) == 2 && parts[1] == "events" && r.Method == http.MethodGet {
		if !s.actorCanReadRun(actorFromRequest(r), runID) {
			s.auditDeny(r, "run.events.read", "run", runID, runID, "actor cannot read run events", nil)
			platform.WriteError(w, http.StatusNotFound, "not found")
			return
		}
		s.runEvents(w, r, runID)
		return
	}
	if len(parts) == 2 && parts[1] == "artifacts" && r.Method == http.MethodGet {
		if !s.actorCanReadRun(actorFromRequest(r), runID) {
			s.auditDeny(r, "artifact.list", "run", runID, runID, "actor cannot list run artifacts", nil)
			platform.WriteError(w, http.StatusNotFound, "not found")
			return
		}
		platform.WriteJSON(w, http.StatusOK, protocol.ArtifactListResponse{Artifacts: s.repo.ListArtifacts(runID)})
		return
	}
	if len(parts) == 2 && parts[1] == "cancel" && r.Method == http.MethodPost {
		if !s.actorCanReadRun(actorFromRequest(r), runID) {
			s.auditDeny(r, "run.cancel", "run", runID, runID, "actor cannot cancel run", nil)
			platform.WriteError(w, http.StatusNotFound, "not found")
			return
		}
		if !s.requireWriteRole(w, r, "run.cancel", "run", runID, runID) {
			return
		}
		run, err := s.repo.UpdateRunStatus(runID, protocol.RunCanceled, "")
		if err != nil {
			writeStoreErr(w, err)
			return
		}
		_, _ = s.repo.AddEvent(runID, protocol.EventRunCanceled, "Run canceled by user.", nil)
		s.auditAllow(r, "run.cancel", "run", runID, runID, nil)
		platform.WriteJSON(w, http.StatusOK, run)
		return
	}
	platform.WriteError(w, http.StatusNotFound, "run route not found")
}

func (s *Server) artifactSubroutes(w http.ResponseWriter, r *http.Request) {
	parts := splitPath(strings.TrimPrefix(r.URL.Path, "/api/artifacts/"))
	if len(parts) == 0 {
		platform.WriteError(w, http.StatusNotFound, "artifact route not found")
		return
	}
	artifactID := parts[0]
	if len(parts) == 1 && r.Method == http.MethodGet {
		artifact, err := s.readAuthorizedArtifact(r, artifactID)
		if err != nil {
			writeStoreErr(w, err)
			return
		}
		platform.WriteJSON(w, http.StatusOK, artifact)
		return
	}
	if len(parts) == 2 && parts[1] == "download" && r.Method == http.MethodGet {
		s.downloadArtifact(w, r, artifactID)
		return
	}
	if len(parts) == 2 && parts[1] == "content" && r.Method == http.MethodGet {
		s.readArtifactContent(w, r, artifactID)
		return
	}
	platform.WriteError(w, http.StatusNotFound, "artifact route not found")
}

func (s *Server) readArtifactContent(w http.ResponseWriter, r *http.Request, artifactID string) {
	artifact, err := s.readAuthorizedArtifact(r, artifactID)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	maxBytes := parseIntBounded(r.URL.Query().Get("max_bytes"), 32*1024, 1, 128*1024)
	response, err := s.readArtifactTextResponse(artifact, maxBytes)
	if err != nil {
		platform.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.auditAllow(r, "artifact.content.read", "artifact", artifact.ID, artifact.RunID, map[string]any{"path": artifact.Path, "bytes_read": response.BytesRead})
	platform.WriteJSON(w, http.StatusOK, response)
}

func (s *Server) downloadArtifact(w http.ResponseWriter, r *http.Request, artifactID string) {
	artifact, err := s.readAuthorizedArtifact(r, artifactID)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	workspaceID := artifact.WorkspaceID
	if workspaceID == "" {
		if run, err := s.repo.GetRun(artifact.RunID); err == nil {
			workspaceID = run.WorkspaceID
		}
	}
	workspace, err := s.repo.GetWorkspace(workspaceID)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	filePath, err := resolveArtifactDownloadPath(workspace, artifact)
	if err != nil {
		platform.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	file, err := os.Open(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			platform.WriteError(w, http.StatusNotFound, "artifact file not found")
			return
		}
		platform.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer file.Close()
	stat, err := file.Stat()
	if err != nil {
		platform.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !stat.Mode().IsRegular() {
		platform.WriteError(w, http.StatusBadRequest, "artifact is not a regular file")
		return
	}
	s.auditAllow(r, "artifact.download", "artifact", artifact.ID, artifact.RunID, map[string]any{"path": artifact.Path})
	filename := artifact.Name
	if filename == "" {
		filename = filepath.Base(filePath)
	}
	contentType := artifact.MimeType
	if contentType == "" {
		contentType = mime.TypeByExtension(filepath.Ext(filename))
	}
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Length", strconv.FormatInt(stat.Size(), 10))
	disposition := "attachment"
	if strings.EqualFold(strings.TrimSpace(r.URL.Query().Get("disposition")), "inline") {
		disposition = "inline"
	}
	w.Header().Set("Content-Disposition", mime.FormatMediaType(disposition, map[string]string{"filename": filename}))
	http.ServeContent(w, r, filename, stat.ModTime(), file)
}

func (s *Server) readArtifactTextResponse(artifact protocol.Artifact, maxBytes int) (protocol.ArtifactTextResponse, error) {
	workspaceID := artifact.WorkspaceID
	if workspaceID == "" {
		if run, err := s.repo.GetRun(artifact.RunID); err == nil {
			workspaceID = run.WorkspaceID
		}
	}
	workspace, err := s.repo.GetWorkspace(workspaceID)
	if err != nil {
		return protocol.ArtifactTextResponse{}, err
	}
	filePath, err := resolveArtifactDownloadPath(workspace, artifact)
	if err != nil {
		return protocol.ArtifactTextResponse{}, err
	}
	if !isTextArtifact(artifact, filePath) {
		return protocol.ArtifactTextResponse{}, errors.New("artifact is not a supported text file")
	}
	file, err := os.Open(filePath)
	if err != nil {
		return protocol.ArtifactTextResponse{}, err
	}
	defer file.Close()
	stat, err := file.Stat()
	if err != nil {
		return protocol.ArtifactTextResponse{}, err
	}
	if !stat.Mode().IsRegular() {
		return protocol.ArtifactTextResponse{}, errors.New("artifact is not a regular file")
	}
	limit := int64(maxBytes) + 1
	data, err := io.ReadAll(io.LimitReader(file, limit))
	if err != nil {
		return protocol.ArtifactTextResponse{}, err
	}
	truncated := len(data) > maxBytes
	if truncated {
		data = data[:maxBytes]
	}
	return protocol.ArtifactTextResponse{
		Artifact:  artifact,
		Content:   string(data),
		Truncated: truncated,
		BytesRead: len(data),
	}, nil
}

func (s *Server) runEvents(w http.ResponseWriter, r *http.Request, runID string) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	afterSeq, _ := strconv.ParseInt(firstNonEmpty(r.URL.Query().Get("after"), r.Header.Get("Last-Event-ID")), 10, 64)
	var err error
	afterSeq, err = s.writeReplayEvents(w, runID, afterSeq)
	if err != nil {
		return
	}

	ch, cancel := s.repo.Subscribe(runID)
	defer cancel()
	afterSeq, err = s.writeReplayEvents(w, runID, afterSeq)
	if err != nil {
		return
	}
	ticker := time.NewTicker(25 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case event, ok := <-ch:
			if !ok {
				return
			}
			if event.Seq > afterSeq {
				if err := platform.WriteSSEWithID(w, strconv.FormatInt(event.Seq, 10), string(event.Type), event); err != nil {
					return
				}
				afterSeq = event.Seq
			}
		case <-ticker.C:
			if err := platform.WriteSSE(w, "ping", map[string]string{"status": "ok"}); err != nil {
				return
			}
		}
	}
}

func (s *Server) writeReplayEvents(w http.ResponseWriter, runID string, afterSeq int64) (int64, error) {
	for _, event := range s.repo.ListEvents(runID, afterSeq) {
		if err := platform.WriteSSEWithID(w, strconv.FormatInt(event.Seq, 10), string(event.Type), event); err != nil {
			return afterSeq, err
		}
		afterSeq = event.Seq
	}
	return afterSeq, nil
}

func (s *Server) skills(w http.ResponseWriter, r *http.Request) {
	actor := actorFromRequest(r)
	skills := s.repo.ListSkillsForUser(actor.UserID, actor.ProjectID)
	platform.WriteJSON(w, http.StatusOK, protocol.SkillsResponse{
		Skills: skills,
		Groups: groupSkills(skills),
	})
}

func (s *Server) auditEvents(w http.ResponseWriter, r *http.Request) {
	actor := actorFromRequest(r)
	opts := app.AuditEventListOptions{
		Limit:      parseLimit(r.URL.Query().Get("limit"), 100),
		RequestID:  strings.TrimSpace(r.URL.Query().Get("request_id")),
		RunID:      strings.TrimSpace(r.URL.Query().Get("run_id")),
		Action:     strings.TrimSpace(r.URL.Query().Get("action")),
		ResourceID: strings.TrimSpace(r.URL.Query().Get("resource_id")),
	}
	platform.WriteJSON(w, http.StatusOK, protocol.AuditEventsResponse{
		Events: s.repo.ListAuditEvents(actor, opts),
	})
}

func (s *Server) organizationSubroutes(w http.ResponseWriter, r *http.Request) {
	actor := actorFromRequest(r)
	parts := splitPath(strings.TrimPrefix(r.URL.Path, "/api/organizations/"))
	if len(parts) < 2 {
		platform.WriteError(w, http.StatusNotFound, "organization route not found")
		return
	}
	orgID, err := pathSegment(parts[0])
	if err != nil || orgID == "" {
		platform.WriteError(w, http.StatusBadRequest, "invalid organization id")
		return
	}
	if orgID != actor.OrgID {
		s.auditDeny(r, "organization.member.read", "organization", orgID, "", "actor cannot access organization", nil)
		platform.WriteError(w, http.StatusNotFound, "not found")
		return
	}
	if parts[1] == "invitations" {
		if len(parts) == 2 && r.Method == http.MethodGet {
			if !s.requireOrganizationAdminRole(w, r, "invitation.list", "organization", orgID, "") {
				return
			}
			platform.WriteJSON(w, http.StatusOK, protocol.InvitationsResponse{
				Invitations: s.repo.ListInvitations(orgID),
			})
			return
		}
		if len(parts) == 2 && r.Method == http.MethodPost {
			s.createInvitation(w, r, orgID)
			return
		}
		platform.WriteError(w, http.StatusNotFound, "organization route not found")
		return
	}
	if parts[1] == "invitation-email-events" {
		if len(parts) == 2 && r.Method == http.MethodGet {
			s.listInvitationEmailEvents(w, r, orgID)
			return
		}
		if len(parts) == 2 && r.Method == http.MethodPost {
			s.recordInvitationEmailEvent(w, r, orgID)
			return
		}
		platform.WriteError(w, http.StatusNotFound, "organization route not found")
		return
	}
	if parts[1] != "members" {
		platform.WriteError(w, http.StatusNotFound, "organization route not found")
		return
	}
	if len(parts) == 2 && r.Method == http.MethodGet {
		platform.WriteJSON(w, http.StatusOK, protocol.OrganizationMembersResponse{
			Members: s.repo.ListOrganizationMembers(orgID),
		})
		return
	}
	if len(parts) == 2 && r.Method == http.MethodPost {
		s.upsertOrganizationMember(w, r, orgID, "")
		return
	}
	if len(parts) == 3 && r.Method == http.MethodPatch {
		userID, err := pathSegment(parts[2])
		if err != nil || userID == "" {
			platform.WriteError(w, http.StatusBadRequest, "invalid user id")
			return
		}
		s.upsertOrganizationMember(w, r, orgID, userID)
		return
	}
	if len(parts) == 3 && r.Method == http.MethodDelete {
		userID, err := pathSegment(parts[2])
		if err != nil || userID == "" {
			platform.WriteError(w, http.StatusBadRequest, "invalid user id")
			return
		}
		s.removeOrganizationMember(w, r, orgID, userID)
		return
	}
	platform.WriteError(w, http.StatusNotFound, "organization route not found")
}

func (s *Server) projectSubroutes(w http.ResponseWriter, r *http.Request) {
	actor := actorFromRequest(r)
	parts := splitPath(strings.TrimPrefix(r.URL.Path, "/api/projects/"))
	if len(parts) < 2 {
		platform.WriteError(w, http.StatusNotFound, "project route not found")
		return
	}
	projectID, err := pathSegment(parts[0])
	if err != nil || projectID == "" {
		platform.WriteError(w, http.StatusBadRequest, "invalid project id")
		return
	}
	if projectID != actor.ProjectID {
		s.auditDeny(r, "project.member.read", "project", projectID, "", "actor cannot access project", nil)
		platform.WriteError(w, http.StatusNotFound, "not found")
		return
	}
	if parts[1] == "quota" {
		if len(parts) == 2 && r.Method == http.MethodGet {
			platform.WriteJSON(w, http.StatusOK, protocol.ProjectQuotaPolicyResponse{Policy: s.effectiveQuotaPolicy(projectID)})
			return
		}
		if len(parts) == 2 && r.Method == http.MethodPatch {
			s.updateProjectQuotaPolicy(w, r, projectID)
			return
		}
		platform.WriteError(w, http.StatusNotFound, "project route not found")
		return
	}
	if parts[1] == "usage" {
		if len(parts) == 2 && r.Method == http.MethodGet {
			s.projectUsage(w, r, projectID)
			return
		}
		platform.WriteError(w, http.StatusNotFound, "project route not found")
		return
	}
	if parts[1] != "members" {
		platform.WriteError(w, http.StatusNotFound, "project route not found")
		return
	}
	if len(parts) == 2 && r.Method == http.MethodGet {
		platform.WriteJSON(w, http.StatusOK, protocol.ProjectMembersResponse{
			Members: s.repo.ListProjectMembers(projectID),
		})
		return
	}
	if len(parts) == 2 && r.Method == http.MethodPost {
		s.upsertProjectMember(w, r, projectID, "")
		return
	}
	if len(parts) == 3 && r.Method == http.MethodPatch {
		userID, err := pathSegment(parts[2])
		if err != nil || userID == "" {
			platform.WriteError(w, http.StatusBadRequest, "invalid user id")
			return
		}
		s.upsertProjectMember(w, r, projectID, userID)
		return
	}
	if len(parts) == 3 && r.Method == http.MethodDelete {
		userID, err := pathSegment(parts[2])
		if err != nil || userID == "" {
			platform.WriteError(w, http.StatusBadRequest, "invalid user id")
			return
		}
		s.removeProjectMember(w, r, projectID, userID)
		return
	}
	platform.WriteError(w, http.StatusNotFound, "project route not found")
}

func (s *Server) projectUsage(w http.ResponseWriter, r *http.Request, projectID string) {
	if !s.requireProjectAdminRole(w, r, "project.usage.read", "project", projectID, "") {
		return
	}
	window, since, err := parseUsageWindow(r.URL.Query().Get("window"))
	if err != nil {
		platform.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	if sinceQuery := strings.TrimSpace(r.URL.Query().Get("since")); sinceQuery != "" {
		parsed, parseErr := time.Parse(time.RFC3339, sinceQuery)
		if parseErr != nil {
			platform.WriteError(w, http.StatusBadRequest, "since must be an RFC3339 timestamp")
			return
		}
		since = parsed.UTC()
		window = "custom"
	}
	buckets := s.repo.ListRunUsageBucketsSince(projectID, since)
	platform.WriteJSON(w, http.StatusOK, protocol.ProjectUsageResponse{
		ProjectID: projectID,
		Window:    window,
		Since:     since,
		Buckets:   buckets,
		Total:     totalUsageBucket(buckets),
	})
}

func (s *Server) invitationSubroutes(w http.ResponseWriter, r *http.Request) {
	parts := splitPath(strings.TrimPrefix(r.URL.Path, "/api/invitations/"))
	if len(parts) != 2 || parts[1] != "accept" || r.Method != http.MethodPost {
		platform.WriteError(w, http.StatusNotFound, "invitation route not found")
		return
	}
	token, err := pathSegment(parts[0])
	if err != nil || token == "" {
		platform.WriteError(w, http.StatusBadRequest, "invalid invitation token")
		return
	}
	actor := actorFromRequest(r)
	var input protocol.InvitationAcceptInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil && !errors.Is(err, io.EOF) {
		platform.WriteError(w, http.StatusBadRequest, "invalid json body")
		return
	}
	actorEmail := strings.ToLower(strings.TrimSpace(actor.Email))
	if actorEmail == "" {
		s.auditDeny(r, "invitation.accept", "invitation", "", "", "actor email is required to accept invitation", nil)
		platform.WriteError(w, http.StatusBadRequest, "接受邀请需要可信身份中的邮箱。")
		return
	}
	invitation, err := s.repo.AcceptInvitation(token, actor.UserID, actorEmail, firstNonEmpty(strings.TrimSpace(input.Name), actor.Name))
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	s.auditAllow(r, "invitation.accept", "invitation", invitation.ID, "", map[string]any{
		"organization_id": invitation.OrganizationID,
		"project_id":      invitation.ProjectID,
		"role":            invitation.Role,
	})
	platform.WriteJSON(w, http.StatusOK, protocol.InvitationResponse{Invitation: invitation})
}

func (s *Server) createInvitation(w http.ResponseWriter, r *http.Request, orgID string) {
	actor := actorFromRequest(r)
	if !s.requireOrganizationAdminRole(w, r, "invitation.create", "organization", orgID, "") {
		return
	}
	var input protocol.InvitationInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		platform.WriteError(w, http.StatusBadRequest, "invalid json body")
		return
	}
	invitation, err := s.repo.CreateInvitation(orgID, actor.UserID, input)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	s.auditAllow(r, "invitation.create", "invitation", invitation.ID, "", map[string]any{
		"organization_id": invitation.OrganizationID,
		"project_id":      invitation.ProjectID,
		"role":            invitation.Role,
	})
	if s.invitationMailer != nil {
		if err := s.invitationMailer.SendInvitation(r.Context(), invitation); err != nil {
			s.log.Warn("invitation email send failed", "invitation_id", invitation.ID, "organization_id", invitation.OrganizationID, "project_id", invitation.ProjectID, "error", err)
			s.auditDeny(r, "invitation.email.send", "invitation", invitation.ID, "", "email send failed", map[string]any{
				"organization_id": invitation.OrganizationID,
				"project_id":      invitation.ProjectID,
				"error_class":     "smtp_send_failed",
			})
		} else {
			s.auditAllow(r, "invitation.email.send", "invitation", invitation.ID, "", map[string]any{
				"organization_id": invitation.OrganizationID,
				"project_id":      invitation.ProjectID,
			})
		}
	}
	platform.WriteJSON(w, http.StatusCreated, protocol.InvitationResponse{Invitation: invitation})
}

func (s *Server) listInvitationEmailEvents(w http.ResponseWriter, r *http.Request, orgID string) {
	if !s.requireOrganizationAdminRole(w, r, "invitation.email_event.list", "organization", orgID, "") {
		return
	}
	opts := app.InvitationEmailEventListOptions{
		InvitationID: strings.TrimSpace(r.URL.Query().Get("invitation_id")),
		DeliveryID:   strings.TrimSpace(r.URL.Query().Get("delivery_id")),
		Limit:        parseLimit(r.URL.Query().Get("limit"), 100),
	}
	platform.WriteJSON(w, http.StatusOK, protocol.InvitationEmailEventsResponse{
		Events: s.repo.ListInvitationEmailEvents(orgID, opts),
	})
}

func (s *Server) recordInvitationEmailEvent(w http.ResponseWriter, r *http.Request, orgID string) {
	if !s.requireOrganizationAdminRole(w, r, "invitation.email_event.record", "organization", orgID, "") {
		return
	}
	var input protocol.InvitationEmailEventInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		platform.WriteError(w, http.StatusBadRequest, "invalid json body")
		return
	}
	input.InvitationID = strings.TrimSpace(input.InvitationID)
	if input.InvitationID == "" {
		platform.WriteError(w, http.StatusBadRequest, "invitation_id is required")
		return
	}
	if !invitationBelongsToOrg(s.repo, orgID, input.InvitationID) {
		s.auditDeny(r, "invitation.email_event.record", "invitation", input.InvitationID, "", "event invitation is outside actor organization", nil)
		platform.WriteError(w, http.StatusNotFound, "not found")
		return
	}
	event, err := s.repo.RecordInvitationEmailEvent(input)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	s.auditAllow(r, "invitation.email_event.record", "invitation", event.InvitationID, "", map[string]any{
		"delivery_id": event.DeliveryID,
		"type":        event.Type,
		"provider":    event.Provider,
	})
	platform.WriteJSON(w, http.StatusCreated, protocol.InvitationEmailEventResponse{Event: event})
}

func (s *Server) invitationEmailWebhook(w http.ResponseWriter, r *http.Request) {
	secret := strings.TrimSpace(s.invitationWebhookSecret)
	if secret == "" {
		platform.WriteError(w, http.StatusNotFound, "webhook not configured")
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		platform.WriteError(w, http.StatusBadRequest, "invalid webhook body")
		return
	}
	if !verifyWebhookSignature(secret, body, r.Header.Get("X-NiceAgent-Webhook-Signature")) {
		s.writeAuditEvent(r, app.ActorContext{}, "invitation.email_webhook.verify", "webhook", "invitation-email-events", "", protocol.AuditDecisionDeny, "invalid webhook signature", nil)
		platform.WriteError(w, http.StatusUnauthorized, "invalid webhook signature")
		return
	}
	var input protocol.InvitationEmailEventInput
	if err := json.Unmarshal(body, &input); err != nil {
		platform.WriteError(w, http.StatusBadRequest, "invalid json body")
		return
	}
	input.InvitationID = strings.TrimSpace(input.InvitationID)
	if input.InvitationID == "" {
		platform.WriteError(w, http.StatusBadRequest, "invitation_id is required")
		return
	}
	event, err := s.repo.RecordInvitationEmailEvent(input)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	s.writeAuditEvent(r, app.ActorContext{}, "invitation.email_webhook.record", "invitation", event.InvitationID, "", protocol.AuditDecisionAllow, "", map[string]any{
		"delivery_id": event.DeliveryID,
		"type":        event.Type,
		"provider":    event.Provider,
	})
	platform.WriteJSON(w, http.StatusAccepted, protocol.InvitationEmailEventResponse{Event: event})
}

func verifyWebhookSignature(secret string, body []byte, signatureHeader string) bool {
	secret = strings.TrimSpace(secret)
	signatureHeader = strings.TrimSpace(signatureHeader)
	if secret == "" || signatureHeader == "" {
		return false
	}
	signatureHeader = strings.TrimPrefix(signatureHeader, "sha256=")
	got, err := hex.DecodeString(signatureHeader)
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(body)
	return hmac.Equal(got, mac.Sum(nil))
}

func invitationBelongsToOrg(repo app.Repository, orgID, invitationID string) bool {
	for _, invitation := range repo.ListInvitations(orgID) {
		if invitation.ID == invitationID {
			return true
		}
	}
	return false
}

func (s *Server) upsertOrganizationMember(w http.ResponseWriter, r *http.Request, orgID, pathUserID string) {
	actor := actorFromRequest(r)
	if !s.requireOrganizationAdminRole(w, r, "organization.member.upsert", "organization", orgID, "") {
		return
	}
	var input protocol.OrganizationMemberInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		platform.WriteError(w, http.StatusBadRequest, "invalid json body")
		return
	}
	if pathUserID != "" {
		input.UserID = pathUserID
	}
	input.UserID = strings.TrimSpace(input.UserID)
	input.Role = normalizeMemberRole(input.Role)
	if input.UserID == "" || input.Role == "" {
		platform.WriteError(w, http.StatusBadRequest, "user_id and a supported role are required")
		return
	}
	if input.UserID == actor.UserID {
		s.auditDeny(r, "organization.member.upsert", "organization_member", input.UserID, "", "actor cannot modify own organization membership", nil)
		platform.WriteError(w, http.StatusBadRequest, "不能修改自己的组织成员角色。")
		return
	}
	member, err := s.repo.UpsertOrganizationMember(orgID, input)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	s.auditAllow(r, "organization.member.upsert", "organization_member", member.UserID, "", map[string]any{
		"organization_id": orgID,
		"role":            member.Role,
	})
	status := http.StatusOK
	if r.Method == http.MethodPost {
		status = http.StatusCreated
	}
	platform.WriteJSON(w, status, protocol.OrganizationMemberResponse{Member: member})
}

func (s *Server) removeOrganizationMember(w http.ResponseWriter, r *http.Request, orgID, userID string) {
	actor := actorFromRequest(r)
	if !s.requireOrganizationAdminRole(w, r, "organization.member.remove", "organization", orgID, "") {
		return
	}
	if userID == actor.UserID {
		s.auditDeny(r, "organization.member.remove", "organization_member", userID, "", "actor cannot remove own organization membership", nil)
		platform.WriteError(w, http.StatusBadRequest, "不能移除自己的组织成员关系。")
		return
	}
	member, err := s.repo.RemoveOrganizationMember(orgID, userID)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	s.auditAllow(r, "organization.member.remove", "organization_member", member.UserID, "", map[string]any{
		"organization_id": orgID,
		"role":            member.Role,
	})
	platform.WriteJSON(w, http.StatusOK, protocol.OrganizationMemberResponse{Member: member})
}

func (s *Server) updateProjectQuotaPolicy(w http.ResponseWriter, r *http.Request, projectID string) {
	if !s.requireProjectAdminRole(w, r, "project.quota.update", "project", projectID, "") {
		return
	}
	var input protocol.ProjectQuotaPolicyInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		platform.WriteError(w, http.StatusBadRequest, "invalid json body")
		return
	}
	if input.MaxConcurrentRuns < 0 || input.MaxRunsPerHour < 0 || input.MaxModelTokensPerDay < 0 {
		platform.WriteError(w, http.StatusBadRequest, "quota values must be non-negative")
		return
	}
	policy, err := s.repo.SetProjectQuotaPolicy(projectID, input)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	s.auditAllow(r, "project.quota.update", "project", projectID, "", map[string]any{
		"max_concurrent_runs":      policy.MaxConcurrentRuns,
		"max_runs_per_hour":        policy.MaxRunsPerHour,
		"max_model_tokens_per_day": policy.MaxModelTokensPerDay,
	})
	platform.WriteJSON(w, http.StatusOK, protocol.ProjectQuotaPolicyResponse{Policy: policy})
}

func (s *Server) upsertProjectMember(w http.ResponseWriter, r *http.Request, projectID, pathUserID string) {
	actor := actorFromRequest(r)
	if !s.requireProjectAdminRole(w, r, "project.member.upsert", "project", projectID, "") {
		return
	}
	var input protocol.ProjectMemberInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		platform.WriteError(w, http.StatusBadRequest, "invalid json body")
		return
	}
	if pathUserID != "" {
		input.UserID = pathUserID
	}
	input.UserID = strings.TrimSpace(input.UserID)
	input.Role = normalizeMemberRole(input.Role)
	if input.UserID == "" || input.Role == "" {
		platform.WriteError(w, http.StatusBadRequest, "user_id and a supported role are required")
		return
	}
	if input.UserID == actor.UserID {
		s.auditDeny(r, "project.member.upsert", "project_member", input.UserID, "", "actor cannot modify own project membership", nil)
		platform.WriteError(w, http.StatusBadRequest, "不能修改自己的项目成员角色。")
		return
	}
	member, err := s.repo.UpsertProjectMember(projectID, input)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	s.auditAllow(r, "project.member.upsert", "project_member", member.UserID, "", map[string]any{
		"project_id": projectID,
		"role":       member.Role,
	})
	status := http.StatusOK
	if r.Method == http.MethodPost {
		status = http.StatusCreated
	}
	platform.WriteJSON(w, status, protocol.ProjectMemberResponse{Member: member})
}

func (s *Server) removeProjectMember(w http.ResponseWriter, r *http.Request, projectID, userID string) {
	actor := actorFromRequest(r)
	if !s.requireProjectAdminRole(w, r, "project.member.remove", "project", projectID, "") {
		return
	}
	if userID == actor.UserID {
		s.auditDeny(r, "project.member.remove", "project_member", userID, "", "actor cannot remove own project membership", nil)
		platform.WriteError(w, http.StatusBadRequest, "不能移除自己的项目成员关系。")
		return
	}
	member, err := s.repo.RemoveProjectMember(projectID, userID)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	s.auditAllow(r, "project.member.remove", "project_member", member.UserID, "", map[string]any{
		"project_id": projectID,
		"role":       member.Role,
	})
	platform.WriteJSON(w, http.StatusOK, protocol.ProjectMemberResponse{Member: member})
}

func (s *Server) skillSubroutes(w http.ResponseWriter, r *http.Request) {
	actor := actorFromRequest(r)
	parts := splitPath(strings.TrimPrefix(r.URL.Path, "/api/skills/"))
	if len(parts) == 1 && parts[0] == "http" && r.Method == http.MethodPost {
		s.createHTTPSkill(w, r)
		return
	}
	if len(parts) == 3 && parts[0] == "import" && parts[1] == "openapi" && parts[2] == "preview" && r.Method == http.MethodPost {
		s.previewOpenAPIImport(w, r)
		return
	}
	if len(parts) == 1 && r.Method == http.MethodPatch {
		s.updateHTTPSkill(w, r, parts[0])
		return
	}
	if len(parts) == 2 && parts[1] == "enable" && r.Method == http.MethodPost {
		if !s.requireWriteRole(w, r, "skill.enable", "skill", parts[0], "") {
			return
		}
		skill, err := s.repo.SetSkillEnabled(actor.UserID, parts[0], true)
		if err != nil {
			writeStoreErr(w, err)
			return
		}
		s.auditAllow(r, "skill.enable", "skill", skill.ID, "", nil)
		platform.WriteJSON(w, http.StatusOK, skill)
		return
	}
	if len(parts) == 2 && parts[1] == "disable" && r.Method == http.MethodPost {
		if !s.requireWriteRole(w, r, "skill.disable", "skill", parts[0], "") {
			return
		}
		skill, err := s.repo.SetSkillEnabled(actor.UserID, parts[0], false)
		if err != nil {
			writeStoreErr(w, err)
			return
		}
		s.auditAllow(r, "skill.disable", "skill", skill.ID, "", nil)
		platform.WriteJSON(w, http.StatusOK, skill)
		return
	}
	if len(parts) == 2 && parts[1] == "approve" && r.Method == http.MethodPost {
		if !s.requireWriteRole(w, r, "skill.approve", "skill", parts[0], "") {
			return
		}
		platform.WriteJSON(w, http.StatusAccepted, map[string]any{
			"skill_id": parts[0],
			"status":   "approved",
		})
		return
	}
	platform.WriteError(w, http.StatusNotFound, "skill route not found")
}

func (s *Server) previewOpenAPIImport(w http.ResponseWriter, r *http.Request) {
	if !s.requireWriteRole(w, r, "skill.import.preview", "skill", "", "") {
		return
	}
	var input protocol.OpenAPIImportPreviewInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		platform.WriteError(w, http.StatusBadRequest, "invalid json body")
		return
	}
	candidates, err := skillmanifest.PreviewOpenAPIHTTPSkills(input.Document, input.BaseURL)
	if err != nil {
		platform.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.auditAllow(r, "skill.import.preview", "skill", "", "", map[string]any{
		"candidate_count": len(candidates),
	})
	platform.WriteJSON(w, http.StatusOK, protocol.OpenAPIImportPreviewResponse{Candidates: candidates})
}

func (s *Server) createHTTPSkill(w http.ResponseWriter, r *http.Request) {
	actor := actorFromRequest(r)
	if !s.requireWriteRole(w, r, "skill.create", "skill", "", "") {
		return
	}
	var input protocol.HTTPSkillInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		platform.WriteError(w, http.StatusBadRequest, "invalid json body")
		return
	}
	if err := validateHTTPSkillInput(input); err != nil {
		platform.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	skill, err := s.repo.CreateHTTPSkill(actor.UserID, actor.ProjectID, input)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	s.auditAllow(r, "skill.create", "skill", skill.ID, "", map[string]any{
		"kind":      string(skill.Kind),
		"auth_type": input.AuthType,
	})
	platform.WriteJSON(w, http.StatusCreated, skill)
}

func (s *Server) updateHTTPSkill(w http.ResponseWriter, r *http.Request, skillID string) {
	actor := actorFromRequest(r)
	if !s.requireWriteRole(w, r, "skill.update", "skill", skillID, "") {
		return
	}
	var input protocol.HTTPSkillInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		platform.WriteError(w, http.StatusBadRequest, "invalid json body")
		return
	}
	if err := validateHTTPSkillInput(input); err != nil {
		platform.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	skill, err := s.repo.UpdateHTTPSkill(actor.UserID, skillID, input)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	s.auditAllow(r, "skill.update", "skill", skill.ID, "", map[string]any{
		"kind":      string(skill.Kind),
		"auth_type": input.AuthType,
	})
	platform.WriteJSON(w, http.StatusOK, skill)
}

func validateHTTPSkillInput(input protocol.HTTPSkillInput) error {
	return skillmanifest.ValidateHTTPSkillInput(input)
}

func groupSkills(skills []protocol.Skill) protocol.SkillGroups {
	groups := protocol.SkillGroups{
		System: []protocol.Skill{},
		User:   []protocol.Skill{},
	}
	for _, skill := range skills {
		if skill.Scope == protocol.SkillScopeUser {
			groups.User = append(groups.User, skill)
		} else {
			groups.System = append(groups.System, skill)
		}
	}
	return groups
}

func (s *Server) internalRunSubroutes(w http.ResponseWriter, r *http.Request) {
	if !s.authorizeInternal(w, r) {
		return
	}
	parts := splitPath(strings.TrimPrefix(r.URL.Path, "/internal/runs/"))
	if len(parts) != 2 {
		platform.WriteError(w, http.StatusNotFound, "internal run route not found")
		return
	}
	runID, action := parts[0], parts[1]
	switch {
	case action == "artifacts" && r.Method == http.MethodGet:
		s.internalListRunArtifacts(w, r, runID)
	case action == "events" && r.Method == http.MethodPost:
		s.internalWriteRunEvent(w, r, runID)
	case action == "complete" && r.Method == http.MethodPost:
		s.internalCompleteRun(w, r, runID)
	case action == "fail" && r.Method == http.MethodPost:
		s.internalFailRun(w, r, runID)
	case action == "status" && r.Method == http.MethodGet:
		run, err := s.repo.GetRun(runID)
		if err != nil {
			writeStoreErr(w, err)
			return
		}
		platform.WriteJSON(w, http.StatusOK, protocol.RunStatusResponse{Run: run})
	case action == "quota-reserve" && r.Method == http.MethodPost:
		s.internalReserveToolQuota(w, r, runID)
	case action == "execution-context" && r.Method == http.MethodGet:
		s.internalRunExecutionContext(w, r, runID)
	case action == "claim" && r.Method == http.MethodPost:
		s.internalClaimRun(w, r, runID)
	default:
		platform.WriteError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Server) internalListRunArtifacts(w http.ResponseWriter, r *http.Request, runID string) {
	if _, err := s.repo.CheckRunAttempt(runID, r.URL.Query().Get("attempt_id")); err != nil {
		writeStoreErr(w, err)
		return
	}
	platform.WriteJSON(w, http.StatusOK, protocol.ArtifactListResponse{Artifacts: s.repo.ListArtifacts(runID)})
}

func (s *Server) internalArtifactSubroutes(w http.ResponseWriter, r *http.Request) {
	if !s.authorizeInternal(w, r) {
		return
	}
	parts := splitPath(strings.TrimPrefix(r.URL.Path, "/internal/artifacts/"))
	if len(parts) != 2 || parts[1] != "content" || r.Method != http.MethodGet {
		platform.WriteError(w, http.StatusNotFound, "internal artifact route not found")
		return
	}
	s.internalReadArtifactText(w, r, parts[0])
}

func (s *Server) internalReadArtifactText(w http.ResponseWriter, r *http.Request, artifactID string) {
	artifact, err := s.repo.GetArtifact(artifactID)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	runID := firstNonEmpty(r.URL.Query().Get("run_id"), artifact.RunID)
	if runID == "" || artifact.RunID != runID {
		platform.WriteError(w, http.StatusNotFound, "not found")
		return
	}
	if _, err := s.repo.CheckRunAttempt(runID, r.URL.Query().Get("attempt_id")); err != nil {
		writeStoreErr(w, err)
		return
	}
	maxBytes := parseIntBounded(r.URL.Query().Get("max_bytes"), 64*1024, 1, 256*1024)
	response, err := s.readArtifactTextResponse(artifact, maxBytes)
	if err != nil {
		platform.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	platform.WriteJSON(w, http.StatusOK, response)
}

func (s *Server) internalClaimRun(w http.ResponseWriter, r *http.Request, runID string) {
	var input protocol.RunClaimRequest
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		platform.WriteError(w, http.StatusBadRequest, "invalid json body")
		return
	}
	input.AttemptID = strings.TrimSpace(input.AttemptID)
	if input.AttemptID == "" {
		platform.WriteError(w, http.StatusBadRequest, "attempt_id is required")
		return
	}
	leaseSeconds := input.LeaseSeconds
	if leaseSeconds <= 0 {
		leaseSeconds = defaultAttemptLeaseSeconds
	}
	run, err := s.repo.ClaimRunAttempt(runID, input.AttemptID, input.ClaimedBy, time.Now().UTC().Add(time.Duration(leaseSeconds)*time.Second))
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	platform.WriteJSON(w, http.StatusOK, protocol.RunClaimResponse{Run: run})
}

func (s *Server) internalRunExecutionContext(w http.ResponseWriter, r *http.Request, runID string) {
	run, err := s.repo.GetRun(runID)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	_, messages, err := s.repo.GetChat(run.ChatID)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	userMessage, ok := userMessageForRun(messages, runID)
	if !ok {
		platform.WriteError(w, http.StatusNotFound, "user message not found")
		return
	}
	runtimeSkills := app.RuntimeSkillsForRun(s.repo, run)
	platform.WriteJSON(w, http.StatusOK, protocol.RunExecutionRequest{
		Request: protocol.RunRequest{
			RunID:       run.ID,
			ChatID:      run.ChatID,
			UserID:      run.UserID,
			WorkspaceID: run.WorkspaceID,
			AttemptID:   firstNonEmpty(r.URL.Query().Get("attempt_id"), run.AttemptID),
			SkillIDs:    app.SkillIDsFromRuntimeSkills(runtimeSkills),
			Skills:      runtimeSkills,
			ModelPolicy: "mock-default",
		},
		UserMessage:     userMessage.Content,
		ControlPlaneURL: s.publicControlPlaneURL(r),
	})
}

func (s *Server) internalReserveToolQuota(w http.ResponseWriter, r *http.Request, runID string) {
	var input protocol.ToolQuotaReserveRequest
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		platform.WriteError(w, http.StatusBadRequest, "invalid json body")
		return
	}
	if strings.TrimSpace(input.SkillID) == "" {
		platform.WriteError(w, http.StatusBadRequest, "skill_id is required")
		return
	}
	if input.ToolCalls <= 0 {
		input.ToolCalls = 1
	}
	if input.SandboxSeconds < 0 {
		input.SandboxSeconds = 0
	}
	run, err := s.repo.CheckRunAttempt(runID, input.AttemptID)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	chat, _, err := s.repo.GetChat(run.ChatID)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	quota := s.effectiveQuotaPolicy(chat.ProjectID)
	since := startOfUTCDay(time.Now().UTC())
	current := s.repo.SumRunUsageSince(run.UserID, chat.ProjectID, since)
	if quota.MaxToolCallsPerDay > 0 && current.ToolCalls+input.ToolCalls > quota.MaxToolCallsPerDay {
		platform.WriteJSON(w, http.StatusOK, protocol.ToolQuotaReserveResponse{
			Allowed: false,
			Message: "已达到当前项目每日工具调用上限，请明天再试。",
			Quota:   "tool_calls_per_day",
			Usage:   current,
		})
		return
	}
	reservedSandboxMillis := int64(input.SandboxSeconds) * int64(time.Second/time.Millisecond)
	if quota.MaxSandboxSecondsPerDay > 0 {
		currentSeconds := int((current.SandboxDurationMillis + 999) / 1000)
		if currentSeconds+input.SandboxSeconds > quota.MaxSandboxSecondsPerDay {
			platform.WriteJSON(w, http.StatusOK, protocol.ToolQuotaReserveResponse{
				Allowed: false,
				Message: "已达到当前项目每日 Sandbox 执行时长上限，请明天再试。",
				Quota:   "sandbox_seconds_per_day",
				Usage:   current,
			})
			return
		}
	}
	usage, err := s.repo.GetRunUsage(runID)
	if err != nil && !errors.Is(err, app.ErrNotFound) {
		writeStoreErr(w, err)
		return
	}
	usage = protocol.NormalizeRunUsage(usage)
	usage.ToolCalls += input.ToolCalls
	if input.SandboxSeconds > 0 {
		usage.SandboxCommands++
		usage.SandboxDurationMillis += reservedSandboxMillis
	}
	usage, err = s.repo.SaveRunUsage(runID, usage)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	platform.WriteJSON(w, http.StatusOK, protocol.ToolQuotaReserveResponse{
		Allowed: true,
		Usage:   usage,
	})
}

func userMessageForRun(messages []protocol.Message, runID string) (protocol.Message, bool) {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].RunID == runID && messages[i].Role == protocol.RoleUser {
			return messages[i], true
		}
	}
	return protocol.Message{}, false
}

func (s *Server) publicControlPlaneURL(r *http.Request) string {
	if s.controlPlaneURL != "" {
		return s.controlPlaneURL
	}
	if envURL := strings.TrimRight(os.Getenv("CONTROL_PLANE_PUBLIC_URL"), "/"); envURL != "" {
		return envURL
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

func (s *Server) internalWriteRunEvent(w http.ResponseWriter, r *http.Request, runID string) {
	var input protocol.RunEventWriteRequest
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		platform.WriteError(w, http.StatusBadRequest, "invalid json body")
		return
	}
	if input.Type == "" {
		platform.WriteError(w, http.StatusBadRequest, "type is required")
		return
	}
	if _, err := s.repo.CheckRunAttempt(runID, input.AttemptID); err != nil {
		writeStoreErr(w, err)
		return
	}
	event, err := s.repo.AddEvent(runID, input.Type, input.Message, input.Payload)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	if input.Type == protocol.EventRunStarted {
		_, _ = s.repo.UpdateRunStatus(runID, protocol.RunRunning, "")
	}
	if input.Type == protocol.EventApprovalNeeded {
		_, _ = s.repo.UpdateRunStatus(runID, protocol.RunWaitingForApproval, "")
	}
	platform.WriteJSON(w, http.StatusCreated, event)
}

func (s *Server) internalCompleteRun(w http.ResponseWriter, r *http.Request, runID string) {
	var input protocol.RunCompleteRequest
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		platform.WriteError(w, http.StatusBadRequest, "invalid json body")
		return
	}
	current, err := s.repo.CheckRunAttempt(runID, input.AttemptID)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	if app.IsTerminalRunStatus(current.Status) {
		platform.WriteJSON(w, http.StatusOK, current)
		return
	}
	if usage := usageFromCompleteRequest(input); !protocol.IsZeroRunUsage(usage) {
		if _, err := s.repo.SaveRunUsage(runID, usage); err != nil {
			writeStoreErr(w, err)
			return
		}
	}
	if err := (app.RepositorySink{Repo: s.repo}).Complete(runID, input.Content, input.Artifacts...); err != nil {
		writeStoreErr(w, err)
		return
	}
	run, err := s.repo.GetRun(runID)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	platform.WriteJSON(w, http.StatusOK, run)
}

func usageFromCompleteRequest(input protocol.RunCompleteRequest) protocol.RunUsage {
	usage := input.Usage
	if protocol.IsZeroRunUsage(usage) && (input.TokenUsage.InputTokens > 0 || input.TokenUsage.OutputTokens > 0) {
		usage.InputTokens = input.TokenUsage.InputTokens
		usage.OutputTokens = input.TokenUsage.OutputTokens
		usage.Estimated = true
	}
	return protocol.NormalizeRunUsage(usage)
}

func (s *Server) internalFailRun(w http.ResponseWriter, r *http.Request, runID string) {
	var input protocol.RunFailRequest
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		platform.WriteError(w, http.StatusBadRequest, "invalid json body")
		return
	}
	if strings.TrimSpace(input.Error) == "" {
		input.Error = "run failed"
	}
	if _, err := s.repo.CheckRunAttempt(runID, input.AttemptID); err != nil {
		writeStoreErr(w, err)
		return
	}
	if err := (app.RepositorySink{Repo: s.repo}).Fail(runID, input.Error); err != nil {
		writeStoreErr(w, err)
		return
	}
	run, err := s.repo.GetRun(runID)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	platform.WriteJSON(w, http.StatusOK, run)
}

func internalToken(optionToken string) string {
	if strings.TrimSpace(optionToken) != "" {
		return strings.TrimSpace(optionToken)
	}
	return strings.TrimSpace(os.Getenv("INTERNAL_API_TOKEN"))
}

func (s *Server) authorizeInternal(w http.ResponseWriter, r *http.Request) bool {
	token := s.internalToken
	if token == "" {
		return true
	}
	if r.Header.Get("Authorization") == "Bearer "+token {
		return true
	}
	platform.WriteError(w, http.StatusUnauthorized, "unauthorized")
	return false
}

func writeStoreErr(w http.ResponseWriter, err error) {
	if errors.Is(err, app.ErrInvalidInput) {
		platform.WriteError(w, http.StatusBadRequest, "invalid input")
		return
	}
	if errors.Is(err, app.ErrNotFound) {
		platform.WriteError(w, http.StatusNotFound, "not found")
		return
	}
	if errors.Is(err, app.ErrAttemptMismatch) {
		platform.WriteError(w, http.StatusConflict, "run attempt mismatch")
		return
	}
	if errors.Is(err, app.ErrIdentityConflict) {
		platform.WriteError(w, http.StatusConflict, "identity binding conflict")
		return
	}
	platform.WriteError(w, http.StatusInternalServerError, err.Error())
}

func pathSegment(value string) (string, error) {
	decoded, err := url.PathUnescape(value)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(decoded), nil
}

func splitPath(path string) []string {
	raw := strings.Split(strings.Trim(path, "/"), "/")
	parts := make([]string, 0, len(raw))
	for _, part := range raw {
		if part != "" {
			parts = append(parts, part)
		}
	}
	return parts
}

func parseBool(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "y", "on":
		return true
	default:
		return false
	}
}

func parseLimit(value string, fallback int) int {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	limit, err := strconv.Atoi(value)
	if err != nil || limit <= 0 {
		return fallback
	}
	if limit > 100 {
		return 100
	}
	return limit
}

func parseIntBounded(value string, fallback, min, max int) int {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	if parsed < min {
		return min
	}
	if parsed > max {
		return max
	}
	return parsed
}

type actorContextKey struct{}

func normalizeAuthMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "oidc":
		return "oidc"
	case "trusted-header", "trusted_header":
		return "trusted-header"
	default:
		return "demo"
	}
}

func (s *Server) reserveRunQuota(ctx context.Context, actor app.ActorContext, userMessage string) (quotapkg.Reservation, bool, string, map[string]any, error) {
	quota := s.effectiveQuotaPolicy(actor.ProjectID)
	if quota.MaxModelTokensPerDay > 0 {
		since := startOfUTCDay(time.Now().UTC())
		current := s.repo.SumRunUsageTokensSince(actor.UserID, actor.ProjectID, since)
		if current >= quota.MaxModelTokensPerDay {
			return nil, true, "已达到当前项目每日模型 token 上限，请明天再试。", map[string]any{
				"quota":   "model_tokens_per_day",
				"limit":   quota.MaxModelTokensPerDay,
				"current": current,
			}, nil
		}
	}
	if denied, message, metadata := s.checkDailyUsageQuota(actor, quota); denied {
		return nil, true, message, metadata, nil
	}
	if s.quotaLimiter != nil {
		reservation, denial, err := s.reserveWithLimiter(ctx, actor, quota, userMessage)
		if err != nil {
			return nil, false, "", nil, err
		}
		if denial.Message != "" {
			return nil, true, denial.Message, denial.Metadata, nil
		}
		return reservation, false, "", nil, nil
	}
	if denied, message, metadata := s.checkRunQuota(actor); denied {
		return nil, true, message, metadata, nil
	}
	return nil, false, "", nil, nil
}

func (s *Server) reserveWithLimiter(ctx context.Context, actor app.ActorContext, quota protocol.ProjectQuotaPolicy, userMessage string) (quotapkg.Reservation, quotapkg.Denial, error) {
	if hinted, ok := s.quotaLimiter.(quotapkg.HintedLimiter); ok {
		return hinted.ReserveRunWithHint(ctx, actor, quota, quotapkg.ReservationHint{
			ModelTokens: s.estimateModelTokenReservation(userMessage),
		})
	}
	return s.quotaLimiter.ReserveRun(ctx, actor, quota)
}

func (s *Server) estimateModelTokenReservation(userMessage string) int {
	if s.tokenReservation.Mode != "dynamic" {
		return 0
	}
	estimate := platform.EstimateTextTokensForModel(strings.TrimSpace(userMessage), s.tokenReservation.Model).Tokens + s.tokenReservation.OutputBuffer
	if estimate < 1 {
		return 1
	}
	return estimate
}

func (s *Server) checkRunQuota(actor app.ActorContext) (bool, string, map[string]any) {
	quota := s.effectiveQuotaPolicy(actor.ProjectID)
	if quota.MaxConcurrentRuns > 0 {
		active := s.repo.CountActiveRuns(actor.UserID, actor.ProjectID)
		if active >= quota.MaxConcurrentRuns {
			return true, "已达到当前项目并发任务上限，请稍后再试。", map[string]any{
				"quota":   "concurrent_runs",
				"limit":   quota.MaxConcurrentRuns,
				"current": active,
			}
		}
	}
	if quota.MaxRunsPerHour > 0 {
		since := time.Now().UTC().Add(-time.Hour)
		current := s.repo.CountRunsCreatedSince(actor.UserID, actor.ProjectID, since)
		if current >= quota.MaxRunsPerHour {
			return true, "已达到当前项目每小时任务数上限，请稍后再试。", map[string]any{
				"quota":   "runs_per_hour",
				"limit":   quota.MaxRunsPerHour,
				"current": current,
			}
		}
	}
	if quota.MaxModelTokensPerDay > 0 {
		since := startOfUTCDay(time.Now().UTC())
		current := s.repo.SumRunUsageTokensSince(actor.UserID, actor.ProjectID, since)
		if current >= quota.MaxModelTokensPerDay {
			return true, "已达到当前项目每日模型 token 上限，请明天再试。", map[string]any{
				"quota":   "model_tokens_per_day",
				"limit":   quota.MaxModelTokensPerDay,
				"current": current,
			}
		}
	}
	if denied, message, metadata := s.checkDailyUsageQuota(actor, quota); denied {
		return true, message, metadata
	}
	return false, "", nil
}

func (s *Server) checkDailyUsageQuota(actor app.ActorContext, quota protocol.ProjectQuotaPolicy) (bool, string, map[string]any) {
	if quota.MaxToolCallsPerDay <= 0 && quota.MaxSandboxSecondsPerDay <= 0 {
		return false, "", nil
	}
	since := startOfUTCDay(time.Now().UTC())
	usage := s.repo.SumRunUsageSince(actor.UserID, actor.ProjectID, since)
	if quota.MaxToolCallsPerDay > 0 && usage.ToolCalls >= quota.MaxToolCallsPerDay {
		return true, "已达到当前项目每日工具调用上限，请明天再试。", map[string]any{
			"quota":   "tool_calls_per_day",
			"limit":   quota.MaxToolCallsPerDay,
			"current": usage.ToolCalls,
		}
	}
	if quota.MaxSandboxSecondsPerDay > 0 {
		current := int((usage.SandboxDurationMillis + 999) / 1000)
		if current >= quota.MaxSandboxSecondsPerDay {
			return true, "已达到当前项目每日 Sandbox 执行时长上限，请明天再试。", map[string]any{
				"quota":   "sandbox_seconds_per_day",
				"limit":   quota.MaxSandboxSecondsPerDay,
				"current": current,
			}
		}
	}
	return false, "", nil
}

func (s *Server) effectiveQuotaPolicy(projectID string) protocol.ProjectQuotaPolicy {
	if policy, ok := s.repo.GetProjectQuotaPolicy(projectID); ok {
		return policy
	}
	return protocol.ProjectQuotaPolicy{
		ProjectID:               projectID,
		MaxConcurrentRuns:       s.runQuota.MaxConcurrentRuns,
		MaxRunsPerHour:          s.runQuota.MaxRunsPerHour,
		MaxModelTokensPerDay:    s.runQuota.MaxTokensPerDay,
		MaxToolCallsPerDay:      s.runQuota.MaxToolCallsPerDay,
		MaxSandboxSecondsPerDay: s.runQuota.MaxSandboxSecondsPerDay,
	}
}

func startOfUTCDay(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

func parseUsageWindow(value string) (string, time.Time, error) {
	window := strings.ToLower(strings.TrimSpace(value))
	if window == "" {
		window = "24h"
	}
	now := time.Now().UTC()
	switch window {
	case "24h":
		return window, now.Add(-24 * time.Hour), nil
	case "7d":
		return window, now.Add(-7 * 24 * time.Hour), nil
	case "30d":
		return window, now.Add(-30 * 24 * time.Hour), nil
	default:
		return "", time.Time{}, fmt.Errorf("window must be one of 24h, 7d, 30d")
	}
}

func totalUsageBucket(buckets []protocol.RunUsageBucket) protocol.RunUsageBucket {
	var total protocol.RunUsageBucket
	for _, bucket := range buckets {
		total.RunCount += bucket.RunCount
		total.InputTokens += bucket.InputTokens
		total.OutputTokens += bucket.OutputTokens
		total.ReasoningTokens += bucket.ReasoningTokens
		total.CachedTokens += bucket.CachedTokens
		total.TotalTokens += bucket.TotalTokens
		total.Cost += bucket.Cost
		total.LatencyMillis += bucket.LatencyMillis
		total.RetryCount += bucket.RetryCount
		total.ToolCalls += bucket.ToolCalls
		total.ToolErrors += bucket.ToolErrors
		total.SandboxCommands += bucket.SandboxCommands
		total.SandboxDurationMillis += bucket.SandboxDurationMillis
		total.SandboxOutputBytes += bucket.SandboxOutputBytes
		total.SandboxCPUMillis += bucket.SandboxCPUMillis
		if bucket.SandboxMemoryMaxBytes > total.SandboxMemoryMaxBytes {
			total.SandboxMemoryMaxBytes = bucket.SandboxMemoryMaxBytes
		}
		total.ArtifactCount += bucket.ArtifactCount
		total.ArtifactBytes += bucket.ArtifactBytes
	}
	return total
}

func normalizeTokenReservationOptions(opts TokenReservationOptions) TokenReservationOptions {
	switch strings.ToLower(strings.TrimSpace(opts.Mode)) {
	case "dynamic":
		opts.Mode = "dynamic"
	default:
		opts.Mode = "fixed"
	}
	if opts.OutputBuffer < 0 {
		opts.OutputBuffer = 0
	}
	return opts
}

func (s *Server) withActor(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !externalAuthRequired(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		if s.authMode == "demo" {
			actor := app.DemoActor()
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), actorContextKey{}, actor)))
			return
		}
		var actor app.ActorContext
		switch s.authMode {
		case "trusted-header":
			var ok bool
			actor, ok = actorFromTrustedHeaders(r)
			if !ok {
				s.writeAuditEvent(r, app.ActorContext{}, "auth.authenticate", "request", r.URL.Path, "", protocol.AuditDecisionDeny, "missing trusted actor headers", nil)
				platform.WriteError(w, http.StatusUnauthorized, "unauthorized")
				return
			}
		case "oidc":
			var err error
			actor, err = s.actorFromOIDCRequest(r)
			if err != nil {
				s.writeAuditEvent(r, app.ActorContext{}, "auth.authenticate", "request", r.URL.Path, "", protocol.AuditDecisionDeny, err.Error(), nil)
				platform.WriteError(w, http.StatusUnauthorized, "unauthorized")
				return
			}
		default:
			s.writeAuditEvent(r, app.ActorContext{}, "auth.authenticate", "request", r.URL.Path, "", protocol.AuditDecisionDeny, "unsupported auth mode", map[string]any{"auth_mode": s.authMode})
			platform.WriteError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		if !s.bindActorIdentity(w, r, actor) {
			return
		}
		if len(actor.Roles) == 0 {
			actor.Roles = s.loadPersistentActorRoles(actor, r.URL.Path)
		}
		if len(actor.Roles) == 0 && invitationAcceptPath(r.URL.Path) {
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), actorContextKey{}, actor)))
			return
		}
		if len(actor.Roles) == 0 {
			resourceType := "project"
			resourceID := actor.ProjectID
			message := "当前用户没有该项目的成员关系。"
			if strings.HasPrefix(r.URL.Path, "/api/organizations/") && actor.OrgID != "" {
				resourceType = "organization"
				resourceID = actor.OrgID
				message = "当前用户没有该组织的成员关系。"
			}
			s.writeAuditEvent(r, actor, "auth.authorize", resourceType, resourceID, "", protocol.AuditDecisionDeny, "membership not found", nil)
			platform.WriteError(w, http.StatusForbidden, message)
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), actorContextKey{}, actor)))
	})
}

func (s *Server) bindActorIdentity(w http.ResponseWriter, r *http.Request, actor app.ActorContext) bool {
	hasIssuer := strings.TrimSpace(actor.IdentityIssuer) != ""
	hasSubject := strings.TrimSpace(actor.IdentitySubject) != ""
	if !hasIssuer && !hasSubject {
		return true
	}
	if !hasIssuer || !hasSubject {
		s.writeAuditEvent(r, actor, "auth.identity.bind", "identity", actor.UserID, "", protocol.AuditDecisionDeny, "identity issuer and subject are both required", nil)
		platform.WriteError(w, http.StatusUnauthorized, "identity issuer and subject are required")
		return false
	}
	provider := firstNonEmpty(strings.TrimSpace(actor.IdentityProvider), "oidc")
	_, err := s.repo.BindUserIdentity(protocol.UserIdentity{
		UserID:   actor.UserID,
		Provider: provider,
		Issuer:   actor.IdentityIssuer,
		Subject:  actor.IdentitySubject,
		Email:    actor.Email,
		Name:     actor.Name,
	})
	if err == nil {
		return true
	}
	if errors.Is(err, app.ErrIdentityConflict) {
		s.writeAuditEvent(r, actor, "auth.identity.bind", "identity", actor.UserID, "", protocol.AuditDecisionDeny, "identity binding conflict", map[string]any{"provider": provider, "issuer": actor.IdentityIssuer})
		platform.WriteError(w, http.StatusConflict, "identity binding conflict")
		return false
	}
	if errors.Is(err, app.ErrInvalidInput) {
		s.writeAuditEvent(r, actor, "auth.identity.bind", "identity", actor.UserID, "", protocol.AuditDecisionDeny, "invalid identity headers", nil)
		platform.WriteError(w, http.StatusUnauthorized, "invalid identity headers")
		return false
	}
	platform.WriteError(w, http.StatusInternalServerError, err.Error())
	return false
}

func (s *Server) actorFromOIDCRequest(r *http.Request) (app.ActorContext, error) {
	if s.oidcVerifier == nil {
		return app.ActorContext{}, errors.New("OIDC verifier is not configured")
	}
	token := bearerToken(r)
	if token == "" {
		return app.ActorContext{}, errors.New("missing bearer token")
	}
	return s.oidcVerifier.ActorFromBearer(r.Context(), token)
}

func (s *Server) loadPersistentActorRoles(actor app.ActorContext, path string) []string {
	if strings.HasPrefix(path, "/api/organizations/") && actor.OrgID != "" {
		if roles := s.repo.ListOrganizationRoles(actor.UserID, actor.OrgID); len(roles) > 0 {
			return roles
		}
	}
	if roles := s.repo.ListProjectRoles(actor.UserID, actor.ProjectID); len(roles) > 0 {
		return roles
	}
	if actor.OrgID != "" && s.repo.ProjectBelongsToOrganization(actor.ProjectID, actor.OrgID) {
		return s.repo.ListOrganizationRoles(actor.UserID, actor.OrgID)
	}
	return nil
}

func externalAuthRequired(path string) bool {
	return strings.HasPrefix(path, "/api/")
}

func invitationAcceptPath(path string) bool {
	return strings.HasPrefix(path, "/api/invitations/") && strings.HasSuffix(path, "/accept")
}

func actorFromTrustedHeaders(r *http.Request) (app.ActorContext, bool) {
	actor := app.ActorContext{
		UserID:           firstHeader(r, "X-NiceAgent-User-ID", "X-User-ID"),
		Email:            firstHeader(r, "X-NiceAgent-User-Email", "X-User-Email"),
		Name:             firstHeader(r, "X-NiceAgent-User-Name", "X-User-Name"),
		IdentityProvider: firstHeader(r, "X-NiceAgent-Identity-Provider", "X-Identity-Provider"),
		IdentityIssuer:   firstHeader(r, "X-NiceAgent-Identity-Issuer", "X-OIDC-Issuer", "X-Identity-Issuer"),
		IdentitySubject:  firstHeader(r, "X-NiceAgent-Identity-Subject", "X-OIDC-Subject", "X-Identity-Subject"),
		ProjectID:        firstHeader(r, "X-NiceAgent-Project-ID", "X-Project-ID"),
		OrgID:            firstHeader(r, "X-NiceAgent-Org-ID", "X-Org-ID"),
		Roles:            splitCSV(firstHeader(r, "X-NiceAgent-Roles", "X-User-Roles")),
	}
	if actor.UserID == "" || actor.ProjectID == "" {
		return app.ActorContext{}, false
	}
	return actor, true
}

func firstHeader(r *http.Request, names ...string) string {
	for _, name := range names {
		if value := strings.TrimSpace(r.Header.Get(name)); value != "" {
			return value
		}
	}
	return ""
}

func splitCSV(value string) []string {
	if value == "" {
		return nil
	}
	raw := strings.Split(value, ",")
	values := make([]string, 0, len(raw))
	for _, item := range raw {
		item = strings.TrimSpace(item)
		if item != "" {
			values = append(values, item)
		}
	}
	return values
}

func actorFromRequest(r *http.Request) app.ActorContext {
	if actor, ok := r.Context().Value(actorContextKey{}).(app.ActorContext); ok && actor.UserID != "" {
		return actor
	}
	return app.DemoActor()
}

func (s *Server) requireWriteRole(w http.ResponseWriter, r *http.Request, action, resourceType, resourceID, runID string) bool {
	actor := actorFromRequest(r)
	if actorCanWrite(actor) {
		return true
	}
	s.auditDeny(r, action, resourceType, resourceID, runID, "actor role cannot write", map[string]any{"roles": actor.Roles})
	platform.WriteError(w, http.StatusForbidden, "当前角色没有执行该操作的权限。")
	return false
}

func (s *Server) requireProjectAdminRole(w http.ResponseWriter, r *http.Request, action, resourceType, resourceID, runID string) bool {
	actor := actorFromRequest(r)
	if actorCanAdminProject(actor) {
		return true
	}
	s.auditDeny(r, action, resourceType, resourceID, runID, "actor role cannot manage project", map[string]any{"roles": actor.Roles})
	platform.WriteError(w, http.StatusForbidden, "当前角色没有管理项目的权限。")
	return false
}

func (s *Server) requireOrganizationAdminRole(w http.ResponseWriter, r *http.Request, action, resourceType, resourceID, runID string) bool {
	actor := actorFromRequest(r)
	if actorCanAdminProject(actor) {
		return true
	}
	s.auditDeny(r, action, resourceType, resourceID, runID, "actor role cannot manage organization", map[string]any{"roles": actor.Roles})
	platform.WriteError(w, http.StatusForbidden, "当前角色没有管理组织的权限。")
	return false
}

func actorCanWrite(actor app.ActorContext) bool {
	if len(actor.Roles) == 0 {
		return false
	}
	for _, role := range actor.Roles {
		switch strings.ToLower(strings.TrimSpace(role)) {
		case "owner", "admin", "member", "editor", "writer":
			return true
		}
	}
	return false
}

func actorCanAdminProject(actor app.ActorContext) bool {
	for _, role := range actor.Roles {
		switch strings.ToLower(strings.TrimSpace(role)) {
		case "owner", "admin":
			return true
		}
	}
	return false
}

func normalizeMemberRole(role string) string {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case "owner", "admin", "member", "editor", "writer", "viewer":
		return strings.ToLower(strings.TrimSpace(role))
	default:
		return ""
	}
}

func requestIDFromRequest(r *http.Request) string {
	return platform.RequestIDFromContext(r.Context())
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func (s *Server) readAuthorizedArtifact(r *http.Request, artifactID string) (protocol.Artifact, error) {
	artifact, err := s.repo.GetArtifact(artifactID)
	if err != nil {
		return protocol.Artifact{}, err
	}
	if !s.actorCanReadArtifact(actorFromRequest(r), artifact) {
		s.auditDeny(r, "artifact.read", "artifact", artifactID, artifact.RunID, "actor cannot read artifact", nil)
		return protocol.Artifact{}, app.ErrNotFound
	}
	return artifact, nil
}

func (s *Server) actorCanReadArtifact(actor app.ActorContext, artifact protocol.Artifact) bool {
	if artifact.UserID != "" {
		if artifact.UserID != actor.UserID {
			return false
		}
		if artifact.ProjectID != "" {
			return artifact.ProjectID == actor.ProjectID
		}
		return true
	}
	if artifact.RunID == "" {
		return false
	}
	return s.actorCanReadRun(actor, artifact.RunID)
}

func (s *Server) actorCanReadChatID(actor app.ActorContext, chatID string) bool {
	chat, _, err := s.repo.GetChat(chatID)
	return err == nil && actorCanReadChat(actor, chat)
}

func actorCanReadChat(actor app.ActorContext, chat protocol.ChatSession) bool {
	return chat.UserID == actor.UserID && chat.ProjectID == actor.ProjectID
}

func (s *Server) actorCanReadRun(actor app.ActorContext, runID string) bool {
	run, err := s.repo.GetRun(runID)
	if err != nil || run.UserID != actor.UserID {
		return false
	}
	chat, _, err := s.repo.GetChat(run.ChatID)
	return err == nil && actorCanReadChat(actor, chat)
}

func resolveArtifactDownloadPath(workspace protocol.Workspace, artifact protocol.Artifact) (string, error) {
	rel, err := cleanArtifactPath(artifact.Path)
	if err != nil {
		return "", err
	}
	rootPath := workspace.RootPath
	if rootPath == "" {
		rootPath = workspace.ID
	}
	if !filepath.IsAbs(rootPath) {
		rootPath = filepath.Join(workspaceRoot(), rootPath)
	}
	rootAbs, err := filepath.Abs(rootPath)
	if err != nil {
		return "", err
	}
	rootReal, err := filepath.EvalSymlinks(rootAbs)
	if err != nil {
		return "", fmt.Errorf("workspace root is unavailable: %w", err)
	}
	target := filepath.Join(rootReal, rel)
	targetReal, err := filepath.EvalSymlinks(target)
	if err != nil {
		return "", fmt.Errorf("artifact file is unavailable: %w", err)
	}
	if !pathWithin(rootReal, targetReal) {
		return "", errors.New("artifact path escapes workspace root")
	}
	return targetReal, nil
}

func cleanArtifactPath(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", errors.New("artifact path is required")
	}
	if filepath.IsAbs(path) {
		return "", errors.New("artifact path must be relative")
	}
	clean := filepath.Clean(filepath.FromSlash(path))
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(os.PathSeparator)) {
		return "", errors.New("artifact path escapes workspace root")
	}
	for _, part := range strings.Split(clean, string(os.PathSeparator)) {
		if part == "." || part == ".." || part == "" {
			return "", errors.New("artifact path contains an unsafe segment")
		}
	}
	outputPrefix := "output" + string(os.PathSeparator)
	if clean != "output" && !strings.HasPrefix(clean, outputPrefix) {
		return "", errors.New("artifact path must be under workspace output")
	}
	return clean, nil
}

func isTextArtifact(artifact protocol.Artifact, filePath string) bool {
	contentType := strings.ToLower(strings.TrimSpace(artifact.MimeType))
	if contentType == "" {
		contentType = strings.ToLower(mime.TypeByExtension(filepath.Ext(firstNonEmpty(artifact.Name, filePath))))
	}
	if strings.HasPrefix(contentType, "text/") {
		return true
	}
	contentType = strings.Split(contentType, ";")[0]
	switch contentType {
	case "application/json", "application/xml", "application/yaml", "application/x-yaml", "application/csv", "application/javascript":
		return true
	default:
		return false
	}
}

func workspaceRoot() string {
	if root := strings.TrimSpace(os.Getenv("SANDBOX_WORKSPACE_ROOT")); root != "" {
		return root
	}
	if root := strings.TrimSpace(os.Getenv("WORKSPACE_ROOT")); root != "" {
		return root
	}
	return "workspaces"
}

func pathWithin(root, target string) bool {
	root = filepath.Clean(root)
	target = filepath.Clean(target)
	return target == root || strings.HasPrefix(target, root+string(os.PathSeparator))
}

func (s *Server) auditAllow(r *http.Request, action, resourceType, resourceID, runID string, metadata map[string]any) {
	s.writeAuditEvent(r, actorFromRequest(r), action, resourceType, resourceID, runID, protocol.AuditDecisionAllow, "", metadata)
}

func (s *Server) auditDeny(r *http.Request, action, resourceType, resourceID, runID, reason string, metadata map[string]any) {
	s.writeAuditEvent(r, actorFromRequest(r), action, resourceType, resourceID, runID, protocol.AuditDecisionDeny, reason, metadata)
}

func (s *Server) writeAuditEvent(
	r *http.Request,
	actor app.ActorContext,
	action, resourceType, resourceID, runID string,
	decision protocol.AuditDecision,
	reason string,
	metadata map[string]any,
) {
	if s.repo == nil {
		return
	}
	_, err := s.repo.AddAuditEvent(protocol.AuditEventInput{
		ActorUserID:    actor.UserID,
		ActorProjectID: actor.ProjectID,
		ActorOrgID:     actor.OrgID,
		Action:         action,
		ResourceType:   resourceType,
		ResourceID:     resourceID,
		Decision:       decision,
		Reason:         reason,
		RequestID:      requestIDFromRequest(r),
		TraceID:        firstNonEmpty(platform.TraceIDFromContext(r.Context()), firstHeader(r, "X-Trace-ID", "Traceparent")),
		RunID:          runID,
		IP:             clientIP(r),
		UserAgent:      r.UserAgent(),
		Metadata:       metadata,
	})
	if err != nil && s.log != nil {
		s.log.Warn("audit_event_write_failed", "request_id", requestIDFromRequest(r), "error", err.Error())
	}
}

func clientIP(r *http.Request) string {
	forwardedFor := strings.TrimSpace(r.Header.Get("X-Forwarded-For"))
	if forwardedFor != "" {
		parts := strings.Split(forwardedFor, ",")
		return strings.TrimSpace(parts[0])
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil {
		return host
	}
	return r.RemoteAddr
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

func (r *statusRecorder) Write(body []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	return r.ResponseWriter.Write(body)
}

func (r *statusRecorder) Flush() {
	if flusher, ok := r.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func requestLogger(log *slog.Logger, authMode string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		recorder := &statusRecorder{ResponseWriter: w}
		next.ServeHTTP(recorder, r)
		status := recorder.status
		if status == 0 {
			status = http.StatusOK
		}
		actor := actorForLog(r, authMode)
		log.Info("http_request",
			"request_id", requestIDFromRequest(r),
			"trace_id", platform.TraceIDFromContext(r.Context()),
			"auth_mode", authMode,
			"actor_user_id", actor.UserID,
			"actor_project_id", actor.ProjectID,
			"method", r.Method,
			"path", r.URL.Path,
			"status", status,
			"duration", time.Since(start).String(),
		)
	})
}

func actorForLog(r *http.Request, authMode string) app.ActorContext {
	if authMode == "trusted-header" && externalAuthRequired(r.URL.Path) {
		if actor, ok := actorFromTrustedHeaders(r); ok {
			return actor
		}
		return app.ActorContext{}
	}
	return actorFromRequest(r)
}
