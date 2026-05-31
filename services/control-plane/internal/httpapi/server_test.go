package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"niceagent/common/protocol"
	"niceagent/control-plane/internal/app"
	"niceagent/control-plane/internal/dispatch"
	"niceagent/control-plane/internal/repository"
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

func TestServerSearchesArchivesAndRestoresChats(t *testing.T) {
	_, handler := newTestHandler()
	alpha := createChatWithHandler(t, handler, "Alpha planning")
	beta := createChatWithHandler(t, handler, "Beta archived")

	search := httptest.NewRequest(http.MethodGet, "/api/chats?q=alpha", nil)
	searchResponse := httptest.NewRecorder()
	handler.ServeHTTP(searchResponse, search)
	if searchResponse.Code != http.StatusOK {
		t.Fatalf("search chats status = %d, body = %s", searchResponse.Code, searchResponse.Body.String())
	}
	var searchOutput struct {
		Chats []protocol.ChatSession `json:"chats"`
	}
	decodeJSON(t, searchResponse.Body, &searchOutput)
	if len(searchOutput.Chats) != 1 || searchOutput.Chats[0].ID != alpha.ID {
		t.Fatalf("search chats = %#v, want alpha", searchOutput.Chats)
	}

	archive := httptest.NewRequest(http.MethodPost, "/api/chats/"+beta.ID+"/archive", nil)
	archiveResponse := httptest.NewRecorder()
	handler.ServeHTTP(archiveResponse, archive)
	if archiveResponse.Code != http.StatusOK {
		t.Fatalf("archive chat status = %d, body = %s", archiveResponse.Code, archiveResponse.Body.String())
	}
	var archived protocol.ChatSession
	decodeJSON(t, archiveResponse.Body, &archived)
	if !archived.Archived {
		t.Fatalf("archived chat = %#v, want archived", archived)
	}

	list := httptest.NewRequest(http.MethodGet, "/api/chats", nil)
	listResponse := httptest.NewRecorder()
	handler.ServeHTTP(listResponse, list)
	var listOutput struct {
		Chats []protocol.ChatSession `json:"chats"`
	}
	decodeJSON(t, listResponse.Body, &listOutput)
	if len(listOutput.Chats) != 1 || listOutput.Chats[0].ID != alpha.ID {
		t.Fatalf("active chats = %#v, want alpha only", listOutput.Chats)
	}

	all := httptest.NewRequest(http.MethodGet, "/api/chats?include_archived=true", nil)
	allResponse := httptest.NewRecorder()
	handler.ServeHTTP(allResponse, all)
	var allOutput struct {
		Chats []protocol.ChatSession `json:"chats"`
	}
	decodeJSON(t, allResponse.Body, &allOutput)
	if len(allOutput.Chats) != 2 {
		t.Fatalf("all chats len = %d, want 2", len(allOutput.Chats))
	}

	restore := httptest.NewRequest(http.MethodPost, "/api/chats/"+beta.ID+"/restore", nil)
	restoreResponse := httptest.NewRecorder()
	handler.ServeHTTP(restoreResponse, restore)
	if restoreResponse.Code != http.StatusOK {
		t.Fatalf("restore chat status = %d, body = %s", restoreResponse.Code, restoreResponse.Body.String())
	}
	var restored protocol.ChatSession
	decodeJSON(t, restoreResponse.Body, &restored)
	if restored.Archived {
		t.Fatalf("restored chat = %#v, want active", restored)
	}
}

func TestServerListsCurrentUserSkills(t *testing.T) {
	_, handler := newTestHandler()
	request := httptest.NewRequest(http.MethodGet, "/api/skills", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("skills status = %d, body = %s", response.Code, response.Body.String())
	}
	var output struct {
		Skills []protocol.Skill     `json:"skills"`
		Groups protocol.SkillGroups `json:"groups"`
	}
	decodeJSON(t, response.Body, &output)
	if len(output.Skills) == 0 {
		t.Fatal("expected skills")
	}
	if len(output.Groups.System) == 0 {
		t.Fatalf("groups = %#v, want system skills", output.Groups)
	}
	for _, skill := range output.Skills {
		if skill.ID == "cli.exec" && skill.RequiresAuth {
			t.Fatalf("cli.exec requires auth = true, want false")
		}
	}
}

func TestServerCreatesUserHTTPSkill(t *testing.T) {
	_, handler := newTestHandler()
	request := httptest.NewRequest(http.MethodPost, "/api/skills/http", jsonBody(t, protocol.HTTPSkillInput{
		Name:        "Weather API",
		Description: "Fetch weather",
		Method:      "POST",
		URL:         "https://example.com/weather",
		AuthType:    "bearer",
		BearerToken: "secret-token",
	}))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("create skill status = %d, body = %s", response.Code, response.Body.String())
	}
	var skill protocol.Skill
	decodeJSON(t, response.Body, &skill)
	if skill.Scope != protocol.SkillScopeUser || skill.Kind != protocol.SkillKindHTTP {
		t.Fatalf("skill scope/kind = %q/%q, want user/http", skill.Scope, skill.Kind)
	}
	if strings.Contains(response.Body.String(), "secret-token") {
		t.Fatalf("response leaked bearer token: %s", response.Body.String())
	}

	list := httptest.NewRequest(http.MethodGet, "/api/skills", nil)
	listResponse := httptest.NewRecorder()
	handler.ServeHTTP(listResponse, list)
	var output protocol.SkillsResponse
	decodeJSON(t, listResponse.Body, &output)
	if len(output.Groups.User) != 1 || output.Groups.User[0].ID != skill.ID {
		t.Fatalf("user groups = %#v, want created skill", output.Groups.User)
	}
}

func TestServerRejectsInvalidHTTPSkillSchemas(t *testing.T) {
	_, handler := newTestHandler()
	request := httptest.NewRequest(http.MethodPost, "/api/skills/http", jsonBody(t, protocol.HTTPSkillInput{
		Name:        "Broken API",
		Method:      "POST",
		URL:         "https://example.com/weather",
		InputSchema: `{"type":"object","properties":{"query":{"type":"definitely-not-a-json-type"}}}`,
	}))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("create skill status = %d, body = %s; want 400", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), "input_schema") {
		t.Fatalf("response body = %s, want input_schema error", response.Body.String())
	}

	request = httptest.NewRequest(http.MethodPost, "/api/skills/http", jsonBody(t, protocol.HTTPSkillInput{
		Name:         "Broken Output API",
		Method:       "POST",
		URL:          "https://example.com/weather",
		OutputSchema: `{"type":"object","properties":{"ok":{"type":"not-real"}}}`,
	}))
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("create skill status = %d, body = %s; want 400", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), "output_schema") {
		t.Fatalf("response body = %s, want output_schema error", response.Body.String())
	}
}

func TestServerRejectsInvalidHTTPSkillRuntimeConfig(t *testing.T) {
	_, handler := newTestHandler()
	request := httptest.NewRequest(http.MethodPost, "/api/skills/http", jsonBody(t, protocol.HTTPSkillInput{
		Name:   "Insecure API",
		Method: "POST",
		URL:    "http://127.0.0.1/hook",
	}))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("create skill status = %d, body = %s; want 400", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), "url scheme must be https") {
		t.Fatalf("response body = %s, want https scheme error", response.Body.String())
	}
}

func TestControlPlaneHTTPRuntimeContract(t *testing.T) {
	store := repository.NewStore()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	var dispatcher app.RunDispatcher
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
	dispatcher = dispatch.NewHTTPDispatcher(store, runtimeServer.URL, controlServer.URL, "", log)

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
	store := repository.NewStore()
	chat := mustCreateChat(t, store, "demo-user", "sink")
	_, run, err := store.AddUserMessage(chat.ID, "demo-user", "cancel")
	if err != nil {
		t.Fatalf("add user message: %v", err)
	}
	if _, err := store.UpdateRunStatus(run.ID, protocol.RunCanceled, ""); err != nil {
		t.Fatalf("cancel run: %v", err)
	}
	sink := app.RepositorySink{Repo: store}

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

func TestServerPersistsListsAndDownloadsArtifacts(t *testing.T) {
	workspaceRoot := t.TempDir()
	t.Setenv("SANDBOX_WORKSPACE_ROOT", workspaceRoot)
	store, handler := newTestHandler()
	chat := mustCreateChat(t, store, "demo-user", "artifacts")
	_, run, err := store.AddUserMessage(chat.ID, "demo-user", "make artifact")
	if err != nil {
		t.Fatalf("add user message: %v", err)
	}
	outputDir := filepath.Join(workspaceRoot, run.WorkspaceID, "output")
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		t.Fatalf("mkdir output: %v", err)
	}
	if err := os.WriteFile(filepath.Join(outputDir, "report.txt"), []byte("report"), 0o644); err != nil {
		t.Fatalf("write artifact: %v", err)
	}

	complete := httptest.NewRequest(http.MethodPost, "/internal/runs/"+run.ID+"/complete", jsonBody(t, protocol.RunCompleteRequest{
		Content: "done",
		Artifacts: []protocol.Artifact{{
			Path:           "output/report.txt",
			Name:           "report.txt",
			MimeType:       "text/plain",
			SizeBytes:      6,
			StorageBackend: "local",
		}},
	}))
	completeResponse := httptest.NewRecorder()
	handler.ServeHTTP(completeResponse, complete)
	if completeResponse.Code != http.StatusOK {
		t.Fatalf("complete status = %d, body = %s", completeResponse.Code, completeResponse.Body.String())
	}

	list := httptest.NewRequest(http.MethodGet, "/api/runs/"+run.ID+"/artifacts", nil)
	listResponse := httptest.NewRecorder()
	handler.ServeHTTP(listResponse, list)
	if listResponse.Code != http.StatusOK {
		t.Fatalf("list artifacts status = %d, body = %s", listResponse.Code, listResponse.Body.String())
	}
	var listed protocol.ArtifactListResponse
	decodeJSON(t, listResponse.Body, &listed)
	if len(listed.Artifacts) != 1 {
		t.Fatalf("artifacts len = %d, want 1", len(listed.Artifacts))
	}
	artifact := listed.Artifacts[0]
	if artifact.ID == "" || artifact.RunID != run.ID || artifact.WorkspaceID != run.WorkspaceID || artifact.UserID != "demo-user" {
		t.Fatalf("artifact identity = %#v", artifact)
	}

	download := httptest.NewRequest(http.MethodGet, "/api/artifacts/"+artifact.ID+"/download", nil)
	downloadResponse := httptest.NewRecorder()
	handler.ServeHTTP(downloadResponse, download)
	if downloadResponse.Code != http.StatusOK {
		t.Fatalf("download status = %d, body = %s", downloadResponse.Code, downloadResponse.Body.String())
	}
	if downloadResponse.Body.String() != "report" {
		t.Fatalf("download body = %q, want report", downloadResponse.Body.String())
	}
	if !strings.Contains(downloadResponse.Header().Get("Content-Disposition"), "report.txt") {
		t.Fatalf("content-disposition = %q", downloadResponse.Header().Get("Content-Disposition"))
	}
	if !containsEvent(store.ListEvents(run.ID, 0), protocol.EventArtifactCreated) {
		t.Fatalf("events = %#v, want artifact.created", store.ListEvents(run.ID, 0))
	}
}

func TestArtifactDownloadRejectsTraversalAndSymlinkEscape(t *testing.T) {
	workspaceRoot := t.TempDir()
	t.Setenv("SANDBOX_WORKSPACE_ROOT", workspaceRoot)
	store, handler := newTestHandler()
	chat := mustCreateChat(t, store, "demo-user", "artifact safety")
	_, run, err := store.AddUserMessage(chat.ID, "demo-user", "unsafe artifact")
	if err != nil {
		t.Fatalf("add user message: %v", err)
	}
	outputDir := filepath.Join(workspaceRoot, run.WorkspaceID, "output")
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		t.Fatalf("mkdir output: %v", err)
	}
	outside := filepath.Join(workspaceRoot, "outside.txt")
	if err := os.WriteFile(outside, []byte("secret"), 0o644); err != nil {
		t.Fatalf("write outside: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(outputDir, "link.txt")); err != nil {
		t.Fatalf("symlink artifact: %v", err)
	}

	traversal, err := store.AddArtifact(protocol.Artifact{
		RunID:     run.ID,
		Path:      "../outside.txt",
		Name:      "outside.txt",
		MimeType:  "text/plain",
		SizeBytes: 6,
	})
	if err != nil {
		t.Fatalf("add traversal artifact: %v", err)
	}
	symlink, err := store.AddArtifact(protocol.Artifact{
		RunID:     run.ID,
		Path:      "output/link.txt",
		Name:      "link.txt",
		MimeType:  "text/plain",
		SizeBytes: 6,
	})
	if err != nil {
		t.Fatalf("add symlink artifact: %v", err)
	}

	for _, artifactID := range []string{traversal.ID, symlink.ID} {
		request := httptest.NewRequest(http.MethodGet, "/api/artifacts/"+artifactID+"/download", nil)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("download %s status = %d, body = %s", artifactID, response.Code, response.Body.String())
		}
	}
}

type dispatchFunc func(context.Context, protocol.Run, string) error

func (f dispatchFunc) Dispatch(ctx context.Context, run protocol.Run, userMessage string) error {
	return f(ctx, run, userMessage)
}

func newTestHandler() (*repository.Store, http.Handler) {
	store := repository.NewStore()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	dispatcher := dispatch.NewLocalDispatcher(store, log)
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

func createChatWithHandler(t *testing.T, handler http.Handler, title string) protocol.ChatSession {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/api/chats", jsonBody(t, map[string]string{"title": title}))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("create chat status = %d, body = %s", response.Code, response.Body.String())
	}
	var chat protocol.ChatSession
	decodeJSON(t, response.Body, &chat)
	return chat
}

func mustCreateChat(t *testing.T, repo app.Repository, userID, title string) protocol.ChatSession {
	t.Helper()
	chat, err := repo.CreateChat(userID, title)
	if err != nil {
		t.Fatalf("create chat: %v", err)
	}
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

func containsEvent(events []protocol.RunEvent, typ protocol.RunEventType) bool {
	for _, event := range events {
		if event.Type == typ {
			return true
		}
	}
	return false
}
