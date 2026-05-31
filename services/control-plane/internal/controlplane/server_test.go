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

	"niceagent/common/protocol"
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
	chat := mustCreateChat(t, store, "demo-user", "sse")
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

func TestControlPlaneHTTPRuntimeContract(t *testing.T) {
	store := NewStore()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	var dispatcher RunDispatcher
	controlServer := httptest.NewServer(NewServer(store, dispatchFunc(func(ctx context.Context, run protocol.Run, userMessage string) error {
		return dispatcher.Dispatch(ctx, run, userMessage)
	}), log).Handler())
	defer controlServer.Close()

	received := make(chan protocol.RunExecutionRequest, 1)
	runtimeServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/internal/runs/execute" {
			http.NotFound(w, r)
			return
		}
		var input protocol.RunExecutionRequest
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		received <- input
		if err := postJSON(input.ControlPlaneURL+"/internal/runs/"+input.Request.RunID+"/events", protocol.RunEventWriteRequest{
			Type:    protocol.EventRunStarted,
			Message: "fake runtime started",
		}); err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		if err := postJSON(input.ControlPlaneURL+"/internal/runs/"+input.Request.RunID+"/events", protocol.RunEventWriteRequest{
			Type:    protocol.EventModelToken,
			Message: "hello",
		}); err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		if err := postJSON(input.ControlPlaneURL+"/internal/runs/"+input.Request.RunID+"/complete", protocol.RunCompleteRequest{
			Content: "hello from fake runtime",
		}); err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"succeeded"}`))
	}))
	defer runtimeServer.Close()
	dispatcher = NewHTTPDispatcher(store, runtimeServer.URL, controlServer.URL, "", log)

	chat := createChatViaAPI(t, controlServer.URL)
	response := postMessageViaAPI(t, controlServer.URL, chat.ID, "hello")

	select {
	case request := <-received:
		if request.Request.RunID != response.Run.ID {
			t.Fatalf("runtime got run id %q, want %q", request.Request.RunID, response.Run.ID)
		}
		if request.ControlPlaneURL != controlServer.URL {
			t.Fatalf("control plane url = %q, want %q", request.ControlPlaneURL, controlServer.URL)
		}
	case <-time.After(time.Second):
		t.Fatal("fake runtime did not receive run execution request")
	}

	if !eventually(time.Second, func() bool {
		run, err := store.GetRun(response.Run.ID)
		return err == nil && run.Status == protocol.RunSucceeded
	}) {
		run, err := store.GetRun(response.Run.ID)
		t.Fatalf("run after fake runtime = %#v err=%v, want succeeded", run, err)
	}
	_, messages, err := store.GetChat(chat.ID)
	if err != nil {
		t.Fatalf("get chat: %v", err)
	}
	if len(messages) != 2 || messages[1].Role != protocol.RoleAssistant || messages[1].Content != "hello from fake runtime" {
		t.Fatalf("messages after completion = %#v", messages)
	}
	events := store.ListEvents(response.Run.ID, 0)
	if len(events) < 4 {
		t.Fatalf("events len = %d, want queued/started/token/succeeded", len(events))
	}
}

func TestControlSinkDoesNotOverwriteCanceledRun(t *testing.T) {
	store := NewStore()
	chat := mustCreateChat(t, store, "demo-user", "sink")
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

type dispatchFunc func(context.Context, protocol.Run, string) error

func (f dispatchFunc) Dispatch(ctx context.Context, run protocol.Run, userMessage string) error {
	return f(ctx, run, userMessage)
}

func newTestHandler() (*Store, http.Handler) {
	store := NewStore()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	dispatcher := NewLocalDispatcher(store, log)
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

func createChatViaAPI(t *testing.T, baseURL string) protocol.ChatSession {
	t.Helper()
	response, err := http.Post(baseURL+"/api/chats", "application/json", jsonBody(t, map[string]string{"title": "http contract"}))
	if err != nil {
		t.Fatalf("create chat request: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("create chat status = %d, body = %s", response.StatusCode, body)
	}
	var chat protocol.ChatSession
	decodeJSON(t, response.Body, &chat)
	return chat
}

func postMessageViaAPI(t *testing.T, baseURL, chatID, content string) struct {
	Message protocol.Message `json:"message"`
	Run     protocol.Run     `json:"run"`
} {
	t.Helper()
	response, err := http.Post(baseURL+"/api/chats/"+chatID+"/messages", "application/json", jsonBody(t, map[string]string{"content": content}))
	if err != nil {
		t.Fatalf("post message request: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusAccepted {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("post message status = %d, body = %s", response.StatusCode, body)
	}
	var output struct {
		Message protocol.Message `json:"message"`
		Run     protocol.Run     `json:"run"`
	}
	decodeJSON(t, response.Body, &output)
	return output
}

func postJSON(url string, value any) error {
	body, err := json.Marshal(value)
	if err != nil {
		return err
	}
	response, err := http.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		raw, _ := io.ReadAll(response.Body)
		return httpError{status: response.Status, body: string(raw)}
	}
	return nil
}

type httpError struct {
	status string
	body   string
}

func (e httpError) Error() string {
	return e.status + ": " + e.body
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
