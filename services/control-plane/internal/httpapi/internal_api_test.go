package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"niceagent/common/protocol"
	"niceagent/control-plane/internal/app"
)

func TestInternalRunAPIsWriteEventsCompleteFailAndStatus(t *testing.T) {
	store, handler := newTestHandler()
	chat := mustCreateChat(t, store, "demo-user", "internal")
	_, run, err := store.AddUserMessage(chat.ID, "demo-user", "hello")
	if err != nil {
		t.Fatalf("add user message: %v", err)
	}

	eventRequest := httptest.NewRequest(http.MethodPost, "/internal/runs/"+run.ID+"/events", jsonBody(t, protocol.RunEventWriteRequest{
		Type:    protocol.EventRunStarted,
		Message: "started remotely",
		Payload: map[string]any{"mode": "http"},
	}))
	eventResponse := httptest.NewRecorder()
	handler.ServeHTTP(eventResponse, eventRequest)
	if eventResponse.Code != http.StatusCreated {
		t.Fatalf("event status = %d, body = %s", eventResponse.Code, eventResponse.Body.String())
	}
	gotRun, err := store.GetRun(run.ID)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if gotRun.Status != protocol.RunRunning {
		t.Fatalf("run status = %q, want running", gotRun.Status)
	}

	statusRequest := httptest.NewRequest(http.MethodGet, "/internal/runs/"+run.ID+"/status", nil)
	statusResponse := httptest.NewRecorder()
	handler.ServeHTTP(statusResponse, statusRequest)
	if statusResponse.Code != http.StatusOK {
		t.Fatalf("status code = %d, body = %s", statusResponse.Code, statusResponse.Body.String())
	}
	var status protocol.RunStatusResponse
	decodeJSON(t, statusResponse.Body, &status)
	if status.Run.ID != run.ID || status.Run.Status != protocol.RunRunning {
		t.Fatalf("status response = %#v", status)
	}

	completeRequest := httptest.NewRequest(http.MethodPost, "/internal/runs/"+run.ID+"/complete", jsonBody(t, protocol.RunCompleteRequest{
		Content: "done",
	}))
	completeResponse := httptest.NewRecorder()
	handler.ServeHTTP(completeResponse, completeRequest)
	if completeResponse.Code != http.StatusOK {
		t.Fatalf("complete status = %d, body = %s", completeResponse.Code, completeResponse.Body.String())
	}
	gotRun, err = store.GetRun(run.ID)
	if err != nil {
		t.Fatalf("get completed run: %v", err)
	}
	if gotRun.Status != protocol.RunSucceeded {
		t.Fatalf("completed status = %q, want succeeded", gotRun.Status)
	}

	_, failedRun, err := store.AddUserMessage(chat.ID, "demo-user", "fail")
	if err != nil {
		t.Fatalf("add failed user message: %v", err)
	}
	failRequest := httptest.NewRequest(http.MethodPost, "/internal/runs/"+failedRun.ID+"/fail", jsonBody(t, protocol.RunFailRequest{
		Error: "runtime exploded",
	}))
	failResponse := httptest.NewRecorder()
	handler.ServeHTTP(failResponse, failRequest)
	if failResponse.Code != http.StatusOK {
		t.Fatalf("fail status = %d, body = %s", failResponse.Code, failResponse.Body.String())
	}
	gotRun, err = store.GetRun(failedRun.ID)
	if err != nil {
		t.Fatalf("get failed run: %v", err)
	}
	if gotRun.Status != protocol.RunFailed || gotRun.Error != "runtime exploded" {
		t.Fatalf("failed run = %#v", gotRun)
	}
}

func TestInternalRunAPIsRequireTokenWhenConfigured(t *testing.T) {
	t.Setenv("INTERNAL_API_TOKEN", "secret")
	store, handler := newTestHandler()
	chat := mustCreateChat(t, store, "demo-user", "auth")
	_, run, err := store.AddUserMessage(chat.ID, "demo-user", "hello")
	if err != nil {
		t.Fatalf("add user message: %v", err)
	}

	request := httptest.NewRequest(http.MethodGet, "/internal/runs/"+run.ID+"/status", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status without token = %d, want 401", response.Code)
	}

	request = httptest.NewRequest(http.MethodGet, "/internal/runs/"+run.ID+"/status", nil)
	request.Header.Set("Authorization", "Bearer secret")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status with token = %d, body = %s", response.Code, response.Body.String())
	}
}

func TestInternalRunAPIApprovalNeededSetsWaitingStatus(t *testing.T) {
	store, handler := newTestHandler()
	chat := mustCreateChat(t, store, "demo-user", "approval")
	_, run, err := store.AddUserMessage(chat.ID, "demo-user", "/cli rm -rf /")
	if err != nil {
		t.Fatalf("add user message: %v", err)
	}

	request := httptest.NewRequest(http.MethodPost, "/internal/runs/"+run.ID+"/events", jsonBody(t, protocol.RunEventWriteRequest{
		Type:    protocol.EventApprovalNeeded,
		Message: "CLI command requires approval.",
		Payload: map[string]any{
			"skill_id":     "cli.exec",
			"command":      []string{"rm", "-rf", "/"},
			"reason":       "command requires explicit approval",
			"policy":       "dangerous_command",
			"workspace_id": run.WorkspaceID,
		},
	}))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("approval event status = %d, body = %s", response.Code, response.Body.String())
	}
	gotRun, err := store.GetRun(run.ID)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if gotRun.Status != protocol.RunWaitingForApproval {
		t.Fatalf("status = %q, want waiting_for_approval", gotRun.Status)
	}

	cancelRequest := httptest.NewRequest(http.MethodPost, "/api/runs/"+run.ID+"/cancel", nil)
	cancelResponse := httptest.NewRecorder()
	handler.ServeHTTP(cancelResponse, cancelRequest)
	if cancelResponse.Code != http.StatusOK {
		t.Fatalf("cancel status = %d, body = %s", cancelResponse.Code, cancelResponse.Body.String())
	}
	if err := (app.RepositorySink{Repo: store}).Complete(run.ID, "late completion"); err != nil {
		t.Fatalf("late complete: %v", err)
	}
	gotRun, err = store.GetRun(run.ID)
	if err != nil {
		t.Fatalf("get canceled run: %v", err)
	}
	if gotRun.Status != protocol.RunCanceled {
		t.Fatalf("late complete overwrote status to %q", gotRun.Status)
	}
}
