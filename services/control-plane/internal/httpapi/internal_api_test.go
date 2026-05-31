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
		Usage: protocol.RunUsage{
			Provider:      "openai-compatible",
			Model:         "deepseek-v4-flash",
			InputTokens:   9,
			OutputTokens:  4,
			TotalTokens:   13,
			Estimated:     false,
			LatencyMillis: 123,
		},
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
	if gotRun.Usage.Provider != "openai-compatible" || gotRun.Usage.Model != "deepseek-v4-flash" || gotRun.Usage.InputTokens != 9 || gotRun.Usage.OutputTokens != 4 {
		t.Fatalf("completed usage = %#v", gotRun.Usage)
	}
	var completedResponse protocol.Run
	decodeJSON(t, completeResponse.Body, &completedResponse)
	if completedResponse.Usage.TotalTokens != 13 || completedResponse.Usage.LatencyMillis != 123 {
		t.Fatalf("complete response usage = %#v", completedResponse.Usage)
	}

	duplicateComplete := httptest.NewRequest(http.MethodPost, "/internal/runs/"+run.ID+"/complete", jsonBody(t, protocol.RunCompleteRequest{
		Content: "duplicate completion",
	}))
	duplicateCompleteResponse := httptest.NewRecorder()
	handler.ServeHTTP(duplicateCompleteResponse, duplicateComplete)
	if duplicateCompleteResponse.Code != http.StatusOK {
		t.Fatalf("duplicate complete status = %d, body = %s", duplicateCompleteResponse.Code, duplicateCompleteResponse.Body.String())
	}
	lateFail := httptest.NewRequest(http.MethodPost, "/internal/runs/"+run.ID+"/fail", jsonBody(t, protocol.RunFailRequest{
		Error: "late failure",
	}))
	lateFailResponse := httptest.NewRecorder()
	handler.ServeHTTP(lateFailResponse, lateFail)
	if lateFailResponse.Code != http.StatusOK {
		t.Fatalf("late fail status = %d, body = %s", lateFailResponse.Code, lateFailResponse.Body.String())
	}
	_, messages, err := store.GetChat(chat.ID)
	if err != nil {
		t.Fatalf("get chat after duplicate terminal callbacks: %v", err)
	}
	var assistantMessages int
	for _, message := range messages {
		if message.RunID == run.ID && message.Role == protocol.RoleAssistant {
			assistantMessages++
			if message.Content != "done" {
				t.Fatalf("assistant message content = %q, want original completion", message.Content)
			}
		}
	}
	if assistantMessages != 1 {
		t.Fatalf("assistant messages for run = %d, want 1", assistantMessages)
	}
	terminalEvents := terminalEventCounts(store.ListEvents(run.ID, 0))
	if terminalEvents[protocol.EventRunSucceeded] != 1 || terminalEvents[protocol.EventRunFailed] != 0 {
		t.Fatalf("terminal events = %#v, want one succeeded and no late failed event", terminalEvents)
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

func TestInternalRunExecutionContextMaterializesRuntimeRequest(t *testing.T) {
	store, handler := newTestHandlerWithOptions(ServerOptions{ControlPlanePublicURL: "http://control.internal:8080"})
	chat := mustCreateChat(t, store, "demo-user", "queued")
	message, run, err := store.AddUserMessage(chat.ID, "demo-user", "/cli echo hello")
	if err != nil {
		t.Fatalf("add user message: %v", err)
	}

	request := httptest.NewRequest(http.MethodGet, "/internal/runs/"+run.ID+"/execution-context?attempt_id=attempt_queue_1", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("execution context status = %d, body = %s", response.Code, response.Body.String())
	}
	var context protocol.RunExecutionRequest
	decodeJSON(t, response.Body, &context)
	if context.UserMessage != message.Content {
		t.Fatalf("user message = %q, want %q", context.UserMessage, message.Content)
	}
	if context.ControlPlaneURL != "http://control.internal:8080" {
		t.Fatalf("control plane url = %q", context.ControlPlaneURL)
	}
	req := context.Request
	if req.RunID != run.ID || req.ChatID != chat.ID || req.UserID != "demo-user" || req.WorkspaceID != run.WorkspaceID {
		t.Fatalf("run request = %#v", req)
	}
	if req.AttemptID != "attempt_queue_1" {
		t.Fatalf("attempt id = %q", req.AttemptID)
	}
	if !app.ContainsSkillID(req.SkillIDs, "cli.exec") {
		t.Fatalf("skill ids = %#v, want cli.exec", req.SkillIDs)
	}
	if len(req.Skills) == 0 {
		t.Fatalf("runtime skills were not materialized")
	}
}

func TestInternalRunQuotaReservePersistsUsageAndDeniesLimits(t *testing.T) {
	store, handler := newTestHandlerWithOptions(ServerOptions{RunQuota: RunQuota{
		MaxToolCallsPerDay:      1,
		MaxSandboxSecondsPerDay: 3,
	}})
	chat := mustCreateChat(t, store, "demo-user", "quota reserve")
	_, run, err := store.AddUserMessage(chat.ID, "demo-user", "/cli echo hello")
	if err != nil {
		t.Fatalf("add user message: %v", err)
	}

	reserve := httptest.NewRequest(http.MethodPost, "/internal/runs/"+run.ID+"/quota-reserve", jsonBody(t, protocol.ToolQuotaReserveRequest{
		SkillID:        "cli.exec",
		ToolCalls:      1,
		SandboxSeconds: 3,
	}))
	reserveResponse := httptest.NewRecorder()
	handler.ServeHTTP(reserveResponse, reserve)
	if reserveResponse.Code != http.StatusOK {
		t.Fatalf("reserve status = %d, body = %s", reserveResponse.Code, reserveResponse.Body.String())
	}
	var output protocol.ToolQuotaReserveResponse
	decodeJSON(t, reserveResponse.Body, &output)
	if !output.Allowed || output.Usage.ToolCalls != 1 || output.Usage.SandboxCommands != 1 || output.Usage.SandboxDurationMillis != 3000 {
		t.Fatalf("reserve response = %#v", output)
	}
	usage, err := store.GetRunUsage(run.ID)
	if err != nil {
		t.Fatalf("get usage: %v", err)
	}
	if usage.ToolCalls != 1 || usage.SandboxCommands != 1 || usage.SandboxDurationMillis != 3000 {
		t.Fatalf("stored usage = %#v", usage)
	}

	denied := httptest.NewRequest(http.MethodPost, "/internal/runs/"+run.ID+"/quota-reserve", jsonBody(t, protocol.ToolQuotaReserveRequest{
		SkillID:   "workspace.read",
		ToolCalls: 1,
	}))
	deniedResponse := httptest.NewRecorder()
	handler.ServeHTTP(deniedResponse, denied)
	if deniedResponse.Code != http.StatusOK {
		t.Fatalf("denied status = %d, body = %s", deniedResponse.Code, deniedResponse.Body.String())
	}
	decodeJSON(t, deniedResponse.Body, &output)
	if output.Allowed || output.Quota != "tool_calls_per_day" {
		t.Fatalf("denied response = %#v", output)
	}
}

func TestInternalRunQuotaReserveHonorsAttemptFencing(t *testing.T) {
	store, handler := newTestHandler()
	chat := mustCreateChat(t, store, "demo-user", "quota attempt")
	_, run, err := store.AddUserMessage(chat.ID, "demo-user", "/cli echo hello")
	if err != nil {
		t.Fatalf("add user message: %v", err)
	}
	claim := httptest.NewRequest(http.MethodPost, "/internal/runs/"+run.ID+"/claim", jsonBody(t, protocol.RunClaimRequest{
		AttemptID: "attempt-active",
		ClaimedBy: "runtime-a",
	}))
	claimResponse := httptest.NewRecorder()
	handler.ServeHTTP(claimResponse, claim)
	if claimResponse.Code != http.StatusOK {
		t.Fatalf("claim status = %d, body = %s", claimResponse.Code, claimResponse.Body.String())
	}

	stale := httptest.NewRequest(http.MethodPost, "/internal/runs/"+run.ID+"/quota-reserve", jsonBody(t, protocol.ToolQuotaReserveRequest{
		AttemptID: "attempt-stale",
		SkillID:   "cli.exec",
		ToolCalls: 1,
	}))
	staleResponse := httptest.NewRecorder()
	handler.ServeHTTP(staleResponse, stale)
	if staleResponse.Code != http.StatusConflict {
		t.Fatalf("stale reserve status = %d, body = %s", staleResponse.Code, staleResponse.Body.String())
	}
	usage, err := store.GetRunUsage(run.ID)
	if err != nil {
		t.Fatalf("get usage: %v", err)
	}
	if usage.ToolCalls != 0 {
		t.Fatalf("usage after stale reserve = %#v, want unchanged", usage)
	}
}

func TestInternalRunAttemptFencingRejectsStaleCallbacks(t *testing.T) {
	store, handler := newTestHandler()
	chat := mustCreateChat(t, store, "demo-user", "attempt")
	_, run, err := store.AddUserMessage(chat.ID, "demo-user", "hello")
	if err != nil {
		t.Fatalf("add user message: %v", err)
	}

	claim := httptest.NewRequest(http.MethodPost, "/internal/runs/"+run.ID+"/claim", jsonBody(t, protocol.RunClaimRequest{
		AttemptID: "attempt-active",
		ClaimedBy: "runtime-a",
	}))
	claimResponse := httptest.NewRecorder()
	handler.ServeHTTP(claimResponse, claim)
	if claimResponse.Code != http.StatusOK {
		t.Fatalf("claim status = %d, body = %s", claimResponse.Code, claimResponse.Body.String())
	}
	var claimed protocol.RunClaimResponse
	decodeJSON(t, claimResponse.Body, &claimed)
	if claimed.Run.AttemptID != "attempt-active" || claimed.Run.ClaimedBy != "runtime-a" || claimed.Run.AttemptCount != 1 || claimed.Run.LeaseExpiresAt == nil {
		t.Fatalf("claimed run = %#v", claimed.Run)
	}

	staleEvent := httptest.NewRequest(http.MethodPost, "/internal/runs/"+run.ID+"/events", jsonBody(t, protocol.RunEventWriteRequest{
		Type:      protocol.EventModelToken,
		Message:   "stale",
		AttemptID: "attempt-stale",
	}))
	staleEventResponse := httptest.NewRecorder()
	handler.ServeHTTP(staleEventResponse, staleEvent)
	if staleEventResponse.Code != http.StatusConflict {
		t.Fatalf("stale event status = %d, body = %s", staleEventResponse.Code, staleEventResponse.Body.String())
	}

	staleComplete := httptest.NewRequest(http.MethodPost, "/internal/runs/"+run.ID+"/complete", jsonBody(t, protocol.RunCompleteRequest{
		Content:   "stale complete",
		AttemptID: "attempt-stale",
	}))
	staleCompleteResponse := httptest.NewRecorder()
	handler.ServeHTTP(staleCompleteResponse, staleComplete)
	if staleCompleteResponse.Code != http.StatusConflict {
		t.Fatalf("stale complete status = %d, body = %s", staleCompleteResponse.Code, staleCompleteResponse.Body.String())
	}
	_, messages, err := store.GetChat(chat.ID)
	if err != nil {
		t.Fatalf("get chat: %v", err)
	}
	if len(messages) != 1 {
		t.Fatalf("messages after stale complete = %#v, want only user message", messages)
	}

	complete := httptest.NewRequest(http.MethodPost, "/internal/runs/"+run.ID+"/complete", jsonBody(t, protocol.RunCompleteRequest{
		Content:   "fresh complete",
		AttemptID: "attempt-active",
	}))
	completeResponse := httptest.NewRecorder()
	handler.ServeHTTP(completeResponse, complete)
	if completeResponse.Code != http.StatusOK {
		t.Fatalf("fresh complete status = %d, body = %s", completeResponse.Code, completeResponse.Body.String())
	}
	gotRun, err := store.GetRun(run.ID)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if gotRun.Status != protocol.RunSucceeded {
		t.Fatalf("run status = %q, want succeeded", gotRun.Status)
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

func terminalEventCounts(events []protocol.RunEvent) map[protocol.RunEventType]int {
	counts := map[protocol.RunEventType]int{}
	for _, event := range events {
		if event.Type == protocol.EventRunSucceeded || event.Type == protocol.EventRunFailed || event.Type == protocol.EventRunCanceled {
			counts[event.Type]++
		}
	}
	return counts
}
