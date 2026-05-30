package controlplane

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"niceagent/internal/protocol"
	"niceagent/internal/runtime"
	"niceagent/internal/sandbox"
)

func TestServerCreatesChatSendsMessageAndCancelsRun(t *testing.T) {
	store, handler := newTestHandler()

	createChat := httptest.NewRequest(http.MethodPost, "/api/chats", jsonBody(t, map[string]string{
		"title": "集成测试",
	}))
	createChatResponse := httptest.NewRecorder()
	handler.ServeHTTP(createChatResponse, createChat)
	if createChatResponse.Code != http.StatusCreated {
		t.Fatalf("create chat status = %d, body = %s", createChatResponse.Code, createChatResponse.Body.String())
	}
	var chat protocol.ChatSession
	decodeJSON(t, createChatResponse.Body, &chat)
	if chat.ID == "" {
		t.Fatal("expected chat id")
	}

	createMessage := httptest.NewRequest(http.MethodPost, "/api/chats/"+chat.ID+"/messages", jsonBody(t, map[string]string{
		"content": "hello agent",
	}))
	createMessageResponse := httptest.NewRecorder()
	handler.ServeHTTP(createMessageResponse, createMessage)
	if createMessageResponse.Code != http.StatusAccepted {
		t.Fatalf("create message status = %d, body = %s", createMessageResponse.Code, createMessageResponse.Body.String())
	}
	var messageResponse struct {
		Message protocol.Message `json:"message"`
		Run     protocol.Run     `json:"run"`
	}
	decodeJSON(t, createMessageResponse.Body, &messageResponse)
	if messageResponse.Message.RunID != messageResponse.Run.ID {
		t.Fatalf("message run id = %q, want %q", messageResponse.Message.RunID, messageResponse.Run.ID)
	}

	cancelRun := httptest.NewRequest(http.MethodPost, "/api/runs/"+messageResponse.Run.ID+"/cancel", nil)
	cancelRunResponse := httptest.NewRecorder()
	handler.ServeHTTP(cancelRunResponse, cancelRun)
	if cancelRunResponse.Code != http.StatusOK {
		t.Fatalf("cancel run status = %d, body = %s", cancelRunResponse.Code, cancelRunResponse.Body.String())
	}
	var canceled protocol.Run
	decodeJSON(t, cancelRunResponse.Body, &canceled)
	if canceled.Status != protocol.RunCanceled {
		t.Fatalf("canceled status = %q, want canceled", canceled.Status)
	}

	if !eventually(300*time.Millisecond, func() bool {
		run, err := store.GetRun(messageResponse.Run.ID)
		return err == nil && run.Status == protocol.RunCanceled
	}) {
		run, err := store.GetRun(messageResponse.Run.ID)
		t.Fatalf("run after background execution = %#v, err = %v; want canceled", run, err)
	}

	getChat := httptest.NewRequest(http.MethodGet, "/api/chats/"+chat.ID, nil)
	getChatResponse := httptest.NewRecorder()
	handler.ServeHTTP(getChatResponse, getChat)
	if getChatResponse.Code != http.StatusOK {
		t.Fatalf("get chat status = %d, body = %s", getChatResponse.Code, getChatResponse.Body.String())
	}
	var chatResponse struct {
		Chat     protocol.ChatSession `json:"chat"`
		Messages []protocol.Message   `json:"messages"`
	}
	decodeJSON(t, getChatResponse.Body, &chatResponse)
	if len(chatResponse.Messages) == 0 {
		t.Fatal("expected at least one persisted message")
	}
}

func TestServerReplaysRunEventsAfterSeq(t *testing.T) {
	store, handler := newTestHandler()
	chat := store.CreateChat("demo-user", "sse")
	_, run, err := store.AddUserMessage(chat.ID, "demo-user", "events")
	if err != nil {
		t.Fatalf("add user message: %v", err)
	}
	if _, err := store.AddEvent(run.ID, protocol.EventRunQueued, "queued", nil); err != nil {
		t.Fatalf("add queued event: %v", err)
	}
	if _, err := store.AddEvent(run.ID, protocol.EventRunStarted, "started", nil); err != nil {
		t.Fatalf("add started event: %v", err)
	}
	if _, err := store.AddEvent(run.ID, protocol.EventRunSucceeded, "succeeded", nil); err != nil {
		t.Fatalf("add succeeded event: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	request := httptest.NewRequest(http.MethodGet, "/api/runs/"+run.ID+"/events?after=1", nil).WithContext(ctx)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("events status = %d, body = %s", response.Code, response.Body.String())
	}
	events := decodeSSEEvents(t, response.Body.String())
	if len(events) != 2 {
		t.Fatalf("events len = %d, want 2, body = %s", len(events), response.Body.String())
	}
	if events[0].Seq != 2 || events[0].Type != protocol.EventRunStarted {
		t.Fatalf("first replay event = seq %d type %q, want seq 2 run.started", events[0].Seq, events[0].Type)
	}
	if events[1].Seq != 3 || events[1].Type != protocol.EventRunSucceeded {
		t.Fatalf("second replay event = seq %d type %q, want seq 3 run.succeeded", events[1].Seq, events[1].Type)
	}
}

func TestControlSinkDoesNotOverwriteCanceledRun(t *testing.T) {
	store := NewStore()
	chat := store.CreateChat("demo-user", "sink")
	_, run, err := store.AddUserMessage(chat.ID, "demo-user", "cancel")
	if err != nil {
		t.Fatalf("add user message: %v", err)
	}
	if _, err := store.UpdateRunStatus(run.ID, protocol.RunCanceled, ""); err != nil {
		t.Fatalf("cancel run: %v", err)
	}
	sink := controlSink{repo: store}

	if err := sink.Complete(run.ID, "late completion"); err != nil {
		t.Fatalf("late complete: %v", err)
	}
	got, err := store.GetRun(run.ID)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if got.Status != protocol.RunCanceled {
		t.Fatalf("status after late complete = %q, want canceled", got.Status)
	}
	_, messages, err := store.GetChat(chat.ID)
	if err != nil {
		t.Fatalf("get chat: %v", err)
	}
	for _, message := range messages {
		if message.Role == protocol.RoleAssistant {
			t.Fatalf("late completion added assistant message: %#v", message)
		}
	}

	if err := sink.Fail(run.ID, "late failure"); err != nil {
		t.Fatalf("late fail: %v", err)
	}
	got, err = store.GetRun(run.ID)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if got.Status != protocol.RunCanceled || got.Error != "" {
		t.Fatalf("run after late failure = status %q error %q, want canceled with empty error", got.Status, got.Error)
	}
}

func newTestHandler() (*Store, http.Handler) {
	store := NewStore()
	engine := runtime.NewEngine(sandbox.NewExecutor())
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	dispatcher := NewLocalDispatcher(store, engine, log)
	return store, NewServer(store, dispatcher, log).Handler()
}

func jsonBody(t *testing.T, value any) io.Reader {
	t.Helper()
	body, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal json body: %v", err)
	}
	return bytes.NewReader(body)
}

func decodeJSON(t *testing.T, body io.Reader, target any) {
	t.Helper()
	if err := json.NewDecoder(body).Decode(target); err != nil {
		t.Fatalf("decode json: %v", err)
	}
}

func decodeSSEEvents(t *testing.T, body string) []protocol.RunEvent {
	t.Helper()
	var events []protocol.RunEvent
	for _, line := range strings.Split(body, "\n") {
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var event protocol.RunEvent
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &event); err != nil {
			t.Fatalf("decode sse data %q: %v", line, err)
		}
		events = append(events, event)
	}
	return events
}

func eventually(timeout time.Duration, condition func() bool) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return condition()
}
