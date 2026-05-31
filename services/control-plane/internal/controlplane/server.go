package controlplane

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"niceagent/common/platform"
	"niceagent/common/protocol"
)

const demoUserID = "demo-user"

type Server struct {
	repo       Repository
	dispatcher RunDispatcher
	log        *slog.Logger
}

func NewServer(repo Repository, dispatcher RunDispatcher, log *slog.Logger) *Server {
	return &Server{repo: repo, dispatcher: dispatcher, log: log}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", platform.Method(http.MethodGet, s.health))
	mux.HandleFunc("/api/chats", s.chats)
	mux.HandleFunc("/api/chats/", s.chatSubroutes)
	mux.HandleFunc("/api/runs/", s.runSubroutes)
	mux.HandleFunc("/api/skills", platform.Method(http.MethodGet, s.skills))
	mux.HandleFunc("/api/skills/", s.skillSubroutes)
	mux.HandleFunc("/internal/runs/", s.internalRunSubroutes)
	mux.Handle("/", http.FileServer(http.Dir(staticDir())))
	return requestLogger(s.log, mux)
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
	switch r.Method {
	case http.MethodGet:
		opts := ChatListOptions{
			Query:           r.URL.Query().Get("q"),
			IncludeArchived: parseBool(r.URL.Query().Get("include_archived")),
		}
		platform.WriteJSON(w, http.StatusOK, map[string]any{"chats": s.repo.ListChats(demoUserID, opts)})
	case http.MethodPost:
		var input struct {
			Title string `json:"title"`
		}
		_ = json.NewDecoder(r.Body).Decode(&input)
		chat, err := s.repo.CreateChat(demoUserID, input.Title)
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
		chat, err := s.repo.SetChatArchived(chatID, demoUserID, true)
		if err != nil {
			writeStoreErr(w, err)
			return
		}
		platform.WriteJSON(w, http.StatusOK, chat)
		return
	}
	if len(parts) == 2 && parts[1] == "restore" && r.Method == http.MethodPost {
		chat, err := s.repo.SetChatArchived(chatID, demoUserID, false)
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
	message, run, err := s.repo.AddUserMessage(chatID, demoUserID, input.Content)
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

func (s *Server) runEvents(w http.ResponseWriter, r *http.Request, runID string) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	afterSeq, _ := strconv.ParseInt(r.URL.Query().Get("after"), 10, 64)
	for _, event := range s.repo.ListEvents(runID, afterSeq) {
		if err := platform.WriteSSE(w, string(event.Type), event); err != nil {
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
				if err := platform.WriteSSE(w, string(event.Type), event); err != nil {
					return
				}
			}
		case <-ticker.C:
			if err := platform.WriteSSE(w, "ping", map[string]string{"status": "ok"}); err != nil {
				return
			}
		}
	}
}

func (s *Server) skills(w http.ResponseWriter, _ *http.Request) {
	platform.WriteJSON(w, http.StatusOK, map[string]any{"skills": s.repo.ListSkills()})
}

func (s *Server) skillSubroutes(w http.ResponseWriter, r *http.Request) {
	parts := splitPath(strings.TrimPrefix(r.URL.Path, "/api/skills/"))
	if len(parts) == 2 && parts[1] == "approve" && r.Method == http.MethodPost {
		platform.WriteJSON(w, http.StatusAccepted, map[string]any{
			"skill_id": parts[0],
			"status":   "approved",
		})
		return
	}
	platform.WriteError(w, http.StatusNotFound, "skill route not found")
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
	if err := (controlSink{repo: s.repo}).Complete(runID, input.Content); err != nil {
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

func (s *Server) internalFailRun(w http.ResponseWriter, r *http.Request, runID string) {
	var input protocol.RunFailRequest
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		platform.WriteError(w, http.StatusBadRequest, "invalid json body")
		return
	}
	if strings.TrimSpace(input.Error) == "" {
		input.Error = "run failed"
	}
	if err := (controlSink{repo: s.repo}).Fail(runID, input.Error); err != nil {
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

type controlSink struct {
	repo Repository
}

func (s controlSink) Emit(runID string, typ protocol.RunEventType, message string, payload any) error {
	_, err := s.repo.AddEvent(runID, typ, message, payload)
	return err
}

func (s controlSink) Complete(runID string, content string) error {
	run, err := s.repo.GetRun(runID)
	if err != nil {
		return err
	}
	if isTerminalRunStatus(run.Status) {
		return nil
	}
	if _, err := s.repo.AddAssistantMessage(run.ChatID, runID, content); err != nil {
		return err
	}
	if _, err := s.repo.UpdateRunStatus(runID, protocol.RunSucceeded, ""); err != nil {
		return err
	}
	_, err = s.repo.AddEvent(runID, protocol.EventRunSucceeded, "Run completed.", nil)
	return err
}

func (s controlSink) Fail(runID string, message string) error {
	run, err := s.repo.GetRun(runID)
	if err != nil {
		return err
	}
	if isTerminalRunStatus(run.Status) {
		return nil
	}
	if _, err := s.repo.UpdateRunStatus(runID, protocol.RunFailed, message); err != nil {
		return err
	}
	_, err = s.repo.AddEvent(runID, protocol.EventRunFailed, message, nil)
	return err
}

func (s controlSink) IsCanceled(runID string) bool {
	run, err := s.repo.GetRun(runID)
	return err == nil && run.Status == protocol.RunCanceled
}

func writeStoreErr(w http.ResponseWriter, err error) {
	if errors.Is(err, ErrNotFound) {
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

func requestLogger(log *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		log.Info("http_request", "method", r.Method, "path", r.URL.Path, "duration", time.Since(start).String())
	})
}
