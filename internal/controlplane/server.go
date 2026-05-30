package controlplane

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"niceagent/internal/platform"
	"niceagent/internal/protocol"
	"niceagent/internal/runtime"
)

const demoUserID = "demo-user"

type Server struct {
	store  *Store
	engine *runtime.Engine
	log    *slog.Logger
}

func NewServer(store *Store, engine *runtime.Engine, log *slog.Logger) *Server {
	return &Server{store: store, engine: engine, log: log}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", platform.Method(http.MethodGet, s.health))
	mux.HandleFunc("/api/chats", s.chats)
	mux.HandleFunc("/api/chats/", s.chatSubroutes)
	mux.HandleFunc("/api/runs/", s.runSubroutes)
	mux.HandleFunc("/api/skills", platform.Method(http.MethodGet, s.skills))
	mux.HandleFunc("/api/skills/", s.skillSubroutes)
	mux.Handle("/", http.FileServer(http.Dir("web/static")))
	return requestLogger(s.log, mux)
}

func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	platform.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) chats(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		platform.WriteJSON(w, http.StatusOK, map[string]any{"chats": s.store.ListChats(demoUserID)})
	case http.MethodPost:
		var input struct {
			Title string `json:"title"`
		}
		_ = json.NewDecoder(r.Body).Decode(&input)
		platform.WriteJSON(w, http.StatusCreated, s.store.CreateChat(demoUserID, input.Title))
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
		chat, messages, err := s.store.GetChat(chatID)
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
	message, run, err := s.store.AddUserMessage(chatID, demoUserID, input.Content)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	_, _ = s.store.AddEvent(run.ID, protocol.EventRunQueued, "Run queued by control plane.", map[string]any{"message_id": message.ID})
	go s.executeRun(context.Background(), run, input.Content)
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
		run, err := s.store.GetRun(runID)
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
		run, err := s.store.UpdateRunStatus(runID, protocol.RunCanceled, "")
		if err != nil {
			writeStoreErr(w, err)
			return
		}
		_, _ = s.store.AddEvent(runID, protocol.EventRunCanceled, "Run canceled by user.", nil)
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
	for _, event := range s.store.ListEvents(runID, afterSeq) {
		if err := platform.WriteSSE(w, string(event.Type), event); err != nil {
			return
		}
		afterSeq = event.Seq
	}

	ch, cancel := s.store.Subscribe(runID)
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
	platform.WriteJSON(w, http.StatusOK, map[string]any{"skills": s.store.ListSkills()})
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

func (s *Server) executeRun(ctx context.Context, run protocol.Run, userMessage string) {
	_, _ = s.store.UpdateRunStatus(run.ID, protocol.RunRunning, "")
	req := protocol.RunRequest{
		RunID:       run.ID,
		ChatID:      run.ChatID,
		UserID:      run.UserID,
		WorkspaceID: run.WorkspaceID,
		SkillIDs:    []string{"workspace.read", "cli.exec"},
		ModelPolicy: "mock-default",
	}
	s.engine.Execute(ctx, req, userMessage, controlSink{store: s.store})
}

type controlSink struct {
	store *Store
}

func (s controlSink) Emit(runID string, typ protocol.RunEventType, message string, payload any) error {
	_, err := s.store.AddEvent(runID, typ, message, payload)
	return err
}

func (s controlSink) Complete(runID string, content string) error {
	run, err := s.store.GetRun(runID)
	if err != nil {
		return err
	}
	if run.Status == protocol.RunCanceled {
		return nil
	}
	s.store.AddAssistantMessage(run.ChatID, runID, content)
	_, _ = s.store.UpdateRunStatus(runID, protocol.RunSucceeded, "")
	_, err = s.store.AddEvent(runID, protocol.EventRunSucceeded, "Run completed.", nil)
	return err
}

func (s controlSink) Fail(runID string, message string) error {
	run, err := s.store.GetRun(runID)
	if err != nil {
		return err
	}
	if run.Status == protocol.RunCanceled {
		return nil
	}
	_, _ = s.store.UpdateRunStatus(runID, protocol.RunFailed, message)
	_, err = s.store.AddEvent(runID, protocol.EventRunFailed, message, nil)
	return err
}

func (s controlSink) IsCanceled(runID string) bool {
	run, err := s.store.GetRun(runID)
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

func requestLogger(log *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		log.Info("http_request", "method", r.Method, "path", r.URL.Path, "duration", time.Since(start).String())
	})
}
