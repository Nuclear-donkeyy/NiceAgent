package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"niceagent/common/platform"
	"niceagent/common/protocol"
	"niceagent/common/skillmanifest"
	"niceagent/control-plane/internal/app"
)

type Server struct {
	repo       app.Repository
	dispatcher app.RunDispatcher
	log        *slog.Logger
}

func NewServer(repo app.Repository, dispatcher app.RunDispatcher, log *slog.Logger) *Server {
	return &Server{repo: repo, dispatcher: dispatcher, log: log}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", platform.Method(http.MethodGet, s.health))
	mux.HandleFunc("/api/chats", s.chats)
	mux.HandleFunc("/api/chats/", s.chatSubroutes)
	mux.HandleFunc("/api/runs/", s.runSubroutes)
	mux.HandleFunc("/api/artifacts/", s.artifactSubroutes)
	mux.HandleFunc("/api/skills", platform.Method(http.MethodGet, s.skills))
	mux.HandleFunc("/api/skills/", s.skillSubroutes)
	mux.HandleFunc("/internal/runs/", s.internalRunSubroutes)
	mux.Handle("/", http.FileServer(http.Dir(staticDir())))
	return requestLogger(s.log, withActor(mux))
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
		platform.WriteJSON(w, http.StatusOK, map[string]any{"chats": s.repo.ListChats(actor.UserID, opts)})
	case http.MethodPost:
		var input struct {
			Title string `json:"title"`
		}
		_ = json.NewDecoder(r.Body).Decode(&input)
		chat, err := s.repo.CreateChat(actor.UserID, input.Title)
		if err != nil {
			writeStoreErr(w, err)
			return
		}
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
		platform.WriteJSON(w, http.StatusOK, map[string]any{"chat": chat, "messages": messages})
		return
	}
	if len(parts) == 2 && parts[1] == "messages" && r.Method == http.MethodPost {
		s.createMessage(w, r, chatID)
		return
	}
	if len(parts) == 2 && parts[1] == "archive" && r.Method == http.MethodPost {
		chat, err := s.repo.SetChatArchived(chatID, actor.UserID, true)
		if err != nil {
			writeStoreErr(w, err)
			return
		}
		platform.WriteJSON(w, http.StatusOK, chat)
		return
	}
	if len(parts) == 2 && parts[1] == "restore" && r.Method == http.MethodPost {
		chat, err := s.repo.SetChatArchived(chatID, actor.UserID, false)
		if err != nil {
			writeStoreErr(w, err)
			return
		}
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
	message, run, err := s.repo.AddUserMessage(chatID, actor.UserID, input.Content)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	_, _ = s.repo.AddEvent(run.ID, protocol.EventRunQueued, "Run queued by control plane.", map[string]any{"message_id": message.ID})
	if err := s.dispatcher.Dispatch(context.Background(), run, input.Content); err != nil {
		_, _ = s.repo.UpdateRunStatus(run.ID, protocol.RunFailed, err.Error())
		_, _ = s.repo.AddEvent(run.ID, protocol.EventRunFailed, "Run dispatch failed.", map[string]any{"error": err.Error()})
		platform.WriteError(w, http.StatusServiceUnavailable, "run dispatch failed")
		return
	}
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
		run, err := s.repo.GetRun(runID)
		if err != nil {
			writeStoreErr(w, err)
			return
		}
		platform.WriteJSON(w, http.StatusOK, run)
		return
	}
	if len(parts) == 2 && parts[1] == "events" && r.Method == http.MethodGet {
		s.runEvents(w, r, runID)
		return
	}
	if len(parts) == 2 && parts[1] == "artifacts" && r.Method == http.MethodGet {
		if !s.actorCanReadRun(actorFromRequest(r), runID) {
			platform.WriteError(w, http.StatusNotFound, "not found")
			return
		}
		platform.WriteJSON(w, http.StatusOK, protocol.ArtifactListResponse{Artifacts: s.repo.ListArtifacts(runID)})
		return
	}
	if len(parts) == 2 && parts[1] == "cancel" && r.Method == http.MethodPost {
		run, err := s.repo.UpdateRunStatus(runID, protocol.RunCanceled, "")
		if err != nil {
			writeStoreErr(w, err)
			return
		}
		_, _ = s.repo.AddEvent(runID, protocol.EventRunCanceled, "Run canceled by user.", nil)
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
	platform.WriteError(w, http.StatusNotFound, "artifact route not found")
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
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": filename}))
	http.ServeContent(w, r, filename, stat.ModTime(), file)
}

func (s *Server) runEvents(w http.ResponseWriter, r *http.Request, runID string) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	afterSeq, _ := strconv.ParseInt(firstNonEmpty(r.URL.Query().Get("after"), r.Header.Get("Last-Event-ID")), 10, 64)
	for _, event := range s.repo.ListEvents(runID, afterSeq) {
		if err := platform.WriteSSEWithID(w, strconv.FormatInt(event.Seq, 10), string(event.Type), event); err != nil {
			return
		}
		afterSeq = event.Seq
	}

	ch, cancel := s.repo.Subscribe(runID)
	defer cancel()
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

func (s *Server) skills(w http.ResponseWriter, r *http.Request) {
	actor := actorFromRequest(r)
	skills := s.repo.ListSkillsForUser(actor.UserID, actor.ProjectID)
	platform.WriteJSON(w, http.StatusOK, protocol.SkillsResponse{
		Skills: skills,
		Groups: groupSkills(skills),
	})
}

func (s *Server) skillSubroutes(w http.ResponseWriter, r *http.Request) {
	actor := actorFromRequest(r)
	parts := splitPath(strings.TrimPrefix(r.URL.Path, "/api/skills/"))
	if len(parts) == 1 && parts[0] == "http" && r.Method == http.MethodPost {
		s.createHTTPSkill(w, r)
		return
	}
	if len(parts) == 1 && r.Method == http.MethodPatch {
		s.updateHTTPSkill(w, r, parts[0])
		return
	}
	if len(parts) == 2 && parts[1] == "enable" && r.Method == http.MethodPost {
		skill, err := s.repo.SetSkillEnabled(actor.UserID, parts[0], true)
		if err != nil {
			writeStoreErr(w, err)
			return
		}
		platform.WriteJSON(w, http.StatusOK, skill)
		return
	}
	if len(parts) == 2 && parts[1] == "disable" && r.Method == http.MethodPost {
		skill, err := s.repo.SetSkillEnabled(actor.UserID, parts[0], false)
		if err != nil {
			writeStoreErr(w, err)
			return
		}
		platform.WriteJSON(w, http.StatusOK, skill)
		return
	}
	if len(parts) == 2 && parts[1] == "approve" && r.Method == http.MethodPost {
		platform.WriteJSON(w, http.StatusAccepted, map[string]any{
			"skill_id": parts[0],
			"status":   "approved",
		})
		return
	}
	platform.WriteError(w, http.StatusNotFound, "skill route not found")
}

func (s *Server) createHTTPSkill(w http.ResponseWriter, r *http.Request) {
	actor := actorFromRequest(r)
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
	platform.WriteJSON(w, http.StatusCreated, skill)
}

func (s *Server) updateHTTPSkill(w http.ResponseWriter, r *http.Request, skillID string) {
	actor := actorFromRequest(r)
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
	if !authorizeInternal(w, r) {
		return
	}
	parts := splitPath(strings.TrimPrefix(r.URL.Path, "/internal/runs/"))
	if len(parts) != 2 {
		platform.WriteError(w, http.StatusNotFound, "internal run route not found")
		return
	}
	runID, action := parts[0], parts[1]
	switch {
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
	default:
		platform.WriteError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
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
	current, err := s.repo.GetRun(runID)
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

func authorizeInternal(w http.ResponseWriter, r *http.Request) bool {
	token := os.Getenv("INTERNAL_API_TOKEN")
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
	if errors.Is(err, app.ErrNotFound) {
		platform.WriteError(w, http.StatusNotFound, "not found")
		return
	}
	platform.WriteError(w, http.StatusInternalServerError, err.Error())
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

type actorContextKey struct{}

func withActor(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		actor := app.DemoActor()
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), actorContextKey{}, actor)))
	})
}

func actorFromRequest(r *http.Request) app.ActorContext {
	if actor, ok := r.Context().Value(actorContextKey{}).(app.ActorContext); ok && actor.UserID != "" {
		return actor
	}
	return app.DemoActor()
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
		return protocol.Artifact{}, app.ErrNotFound
	}
	return artifact, nil
}

func (s *Server) actorCanReadArtifact(actor app.ActorContext, artifact protocol.Artifact) bool {
	if artifact.UserID != "" {
		return artifact.UserID == actor.UserID
	}
	if artifact.RunID == "" {
		return false
	}
	return s.actorCanReadRun(actor, artifact.RunID)
}

func (s *Server) actorCanReadRun(actor app.ActorContext, runID string) bool {
	run, err := s.repo.GetRun(runID)
	return err == nil && run.UserID == actor.UserID
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

func requestLogger(log *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		log.Info("http_request", "method", r.Method, "path", r.URL.Path, "duration", time.Since(start).String())
	})
}
