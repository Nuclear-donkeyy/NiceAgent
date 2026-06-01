package httpapi

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
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
	quotapkg "niceagent/control-plane/internal/quota"
	"niceagent/control-plane/internal/repository"
)

func TestServerCreatesChatSendsMessageAndCancelsRun(t *testing.T) {
	store, handler := newTestHandler()

	createChat := httptest.NewRequest(http.MethodPost, "/api/chats", jsonBody(t, map[string]string{
		"title": "集成测试",
	}))
	createChat.Header.Set("X-Request-ID", "test-request-create-chat")
	createChatResponse := httptest.NewRecorder()
	handler.ServeHTTP(createChatResponse, createChat)
	if createChatResponse.Code != http.StatusCreated {
		t.Fatalf("create chat status = %d, body = %s", createChatResponse.Code, createChatResponse.Body.String())
	}
	if got := createChatResponse.Header().Get("X-Request-ID"); got != "test-request-create-chat" {
		t.Fatalf("x-request-id = %q, want propagated request id", got)
	}
	if got := createChatResponse.Header().Get("X-Trace-ID"); got == "" {
		t.Fatal("expected trace id response header")
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

	auditEvents := store.ListAuditEvents(app.DemoActor(), app.AuditEventListOptions{RequestID: "test-request-create-chat"})
	if len(auditEvents) != 1 || auditEvents[0].Action != "chat.create" || auditEvents[0].RequestID != "test-request-create-chat" {
		t.Fatalf("audit events = %#v, want chat.create with request id", auditEvents)
	}
	if auditEvents[0].TraceID == "" {
		t.Fatalf("audit event missing trace id: %#v", auditEvents[0])
	}

	metricsRequest := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	metricsResponse := httptest.NewRecorder()
	handler.ServeHTTP(metricsResponse, metricsRequest)
	if metricsResponse.Code != http.StatusOK {
		t.Fatalf("metrics status = %d", metricsResponse.Code)
	}
	if !strings.Contains(metricsResponse.Body.String(), "niceagent_http_requests_total") {
		t.Fatalf("metrics body = %s", metricsResponse.Body.String())
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
	if !strings.Contains(response.Body.String(), "id: 2\n") || !strings.Contains(response.Body.String(), "id: 3\n") {
		t.Fatalf("sse body = %s, want id lines for replayed seq", response.Body.String())
	}
	if events[0].Seq != 2 || events[0].Type != protocol.EventRunStarted {
		t.Fatalf("first replay event = seq %d type %q, want seq 2 run.started", events[0].Seq, events[0].Type)
	}
	if events[1].Seq != 3 || events[1].Type != protocol.EventRunSucceeded {
		t.Fatalf("second replay event = seq %d type %q, want seq 3 run.succeeded", events[1].Seq, events[1].Type)
	}
}

func TestServerReplaysRunEventsAfterLastEventID(t *testing.T) {
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
	request := httptest.NewRequest(http.MethodGet, "/api/runs/"+run.ID+"/events", nil).WithContext(ctx)
	request.Header.Set("Last-Event-ID", "2")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	events := decodeSSEEvents(t, response.Body.String())
	if len(events) != 1 {
		t.Fatalf("events len = %d, want 1, body = %s", len(events), response.Body.String())
	}
	if events[0].Seq != 3 || events[0].Type != protocol.EventRunSucceeded {
		t.Fatalf("replayed event = seq %d type %q, want seq 3 run.succeeded", events[0].Seq, events[0].Type)
	}
	if !strings.Contains(response.Body.String(), "id: 3\n") || strings.Contains(response.Body.String(), "id: 2\n") {
		t.Fatalf("sse body = %s, want only id 3 after Last-Event-ID", response.Body.String())
	}
}

func TestServerTrustedHeaderModeRequiresActorAndIsolatesUsers(t *testing.T) {
	_, handler := newTestHandlerWithOptions(ServerOptions{AuthMode: "trusted-header"})

	unauthorized := httptest.NewRequest(http.MethodGet, "/api/chats", nil)
	unauthorizedResponse := httptest.NewRecorder()
	handler.ServeHTTP(unauthorizedResponse, unauthorized)
	if unauthorizedResponse.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized status = %d, body = %s", unauthorizedResponse.Code, unauthorizedResponse.Body.String())
	}
	if unauthorizedResponse.Header().Get("X-Request-ID") == "" {
		t.Fatal("expected request id on unauthorized response")
	}

	createChat := httptest.NewRequest(http.MethodPost, "/api/chats", jsonBody(t, map[string]string{"title": "private"}))
	setTrustedActor(createChat, "user-a", "project-a")
	createChatResponse := httptest.NewRecorder()
	handler.ServeHTTP(createChatResponse, createChat)
	if createChatResponse.Code != http.StatusCreated {
		t.Fatalf("create chat status = %d, body = %s", createChatResponse.Code, createChatResponse.Body.String())
	}
	var chat protocol.ChatSession
	decodeJSON(t, createChatResponse.Body, &chat)
	if chat.UserID != "user-a" || chat.ProjectID != "project-a" {
		t.Fatalf("chat actor fields = %#v", chat)
	}

	getAsOtherUser := httptest.NewRequest(http.MethodGet, "/api/chats/"+chat.ID, nil)
	setTrustedActor(getAsOtherUser, "user-b", "project-a")
	getAsOtherUserResponse := httptest.NewRecorder()
	handler.ServeHTTP(getAsOtherUserResponse, getAsOtherUser)
	if getAsOtherUserResponse.Code != http.StatusNotFound {
		t.Fatalf("cross-user get chat status = %d, body = %s", getAsOtherUserResponse.Code, getAsOtherUserResponse.Body.String())
	}

	listAsOtherProject := httptest.NewRequest(http.MethodGet, "/api/chats", nil)
	setTrustedActor(listAsOtherProject, "user-a", "project-b")
	listAsOtherProjectResponse := httptest.NewRecorder()
	handler.ServeHTTP(listAsOtherProjectResponse, listAsOtherProject)
	var listOutput struct {
		Chats []protocol.ChatSession `json:"chats"`
	}
	decodeJSON(t, listAsOtherProjectResponse.Body, &listOutput)
	if len(listOutput.Chats) != 0 {
		t.Fatalf("cross-project chats = %#v, want none", listOutput.Chats)
	}

	createMessage := httptest.NewRequest(http.MethodPost, "/api/chats/"+chat.ID+"/messages", jsonBody(t, map[string]string{"content": "hello"}))
	setTrustedActor(createMessage, "user-a", "project-a")
	createMessageResponse := httptest.NewRecorder()
	handler.ServeHTTP(createMessageResponse, createMessage)
	if createMessageResponse.Code != http.StatusAccepted {
		t.Fatalf("create message status = %d, body = %s", createMessageResponse.Code, createMessageResponse.Body.String())
	}
	var messageResponse struct {
		Run protocol.Run `json:"run"`
	}
	decodeJSON(t, createMessageResponse.Body, &messageResponse)

	cancelAsOtherUser := httptest.NewRequest(http.MethodPost, "/api/runs/"+messageResponse.Run.ID+"/cancel", nil)
	setTrustedActor(cancelAsOtherUser, "user-b", "project-a")
	cancelAsOtherUserResponse := httptest.NewRecorder()
	handler.ServeHTTP(cancelAsOtherUserResponse, cancelAsOtherUser)
	if cancelAsOtherUserResponse.Code != http.StatusNotFound {
		t.Fatalf("cross-user cancel status = %d, body = %s", cancelAsOtherUserResponse.Code, cancelAsOtherUserResponse.Body.String())
	}
}

func TestServerTrustedHeaderViewerRoleIsReadOnly(t *testing.T) {
	_, handler := newTestHandlerWithOptions(ServerOptions{AuthMode: "trusted-header"})
	createChat := httptest.NewRequest(http.MethodPost, "/api/chats", jsonBody(t, map[string]string{"title": "rbac"}))
	setTrustedActor(createChat, "user-a", "project-a")
	createChatResponse := httptest.NewRecorder()
	handler.ServeHTTP(createChatResponse, createChat)
	if createChatResponse.Code != http.StatusCreated {
		t.Fatalf("owner create chat status = %d, body = %s", createChatResponse.Code, createChatResponse.Body.String())
	}
	var chat protocol.ChatSession
	decodeJSON(t, createChatResponse.Body, &chat)

	readChat := httptest.NewRequest(http.MethodGet, "/api/chats/"+chat.ID, nil)
	setTrustedActorWithRoles(readChat, "user-a", "project-a", "viewer")
	readChatResponse := httptest.NewRecorder()
	handler.ServeHTTP(readChatResponse, readChat)
	if readChatResponse.Code != http.StatusOK {
		t.Fatalf("viewer read chat status = %d, body = %s", readChatResponse.Code, readChatResponse.Body.String())
	}

	createMessage := httptest.NewRequest(http.MethodPost, "/api/chats/"+chat.ID+"/messages", jsonBody(t, map[string]string{"content": "viewer cannot write"}))
	setTrustedActorWithRoles(createMessage, "user-a", "project-a", "viewer")
	createMessageResponse := httptest.NewRecorder()
	handler.ServeHTTP(createMessageResponse, createMessage)
	if createMessageResponse.Code != http.StatusForbidden {
		t.Fatalf("viewer create message status = %d, body = %s", createMessageResponse.Code, createMessageResponse.Body.String())
	}
	if !strings.Contains(createMessageResponse.Body.String(), "没有执行该操作的权限") {
		t.Fatalf("viewer deny body = %s", createMessageResponse.Body.String())
	}
}

func TestServerTrustedHeaderUsesPersistentProjectMembership(t *testing.T) {
	store, handler := newTestHandlerWithOptions(ServerOptions{AuthMode: "trusted-header"})
	store.SetProjectRole("member-user", "member-project", "viewer")

	listChats := httptest.NewRequest(http.MethodGet, "/api/chats", nil)
	setTrustedActorWithoutRoles(listChats, "member-user", "member-project")
	listResponse := httptest.NewRecorder()
	handler.ServeHTTP(listResponse, listChats)
	if listResponse.Code != http.StatusOK {
		t.Fatalf("member list status = %d, body = %s", listResponse.Code, listResponse.Body.String())
	}

	createChat := httptest.NewRequest(http.MethodPost, "/api/chats", jsonBody(t, map[string]string{"title": "membership"}))
	setTrustedActorWithoutRoles(createChat, "member-user", "member-project")
	createResponse := httptest.NewRecorder()
	handler.ServeHTTP(createResponse, createChat)
	if createResponse.Code != http.StatusForbidden {
		t.Fatalf("viewer create status = %d, body = %s", createResponse.Code, createResponse.Body.String())
	}

	store.SetProjectRole("member-user", "member-project", "member")
	createChat = httptest.NewRequest(http.MethodPost, "/api/chats", jsonBody(t, map[string]string{"title": "membership"}))
	setTrustedActorWithoutRoles(createChat, "member-user", "member-project")
	createResponse = httptest.NewRecorder()
	handler.ServeHTTP(createResponse, createChat)
	if createResponse.Code != http.StatusCreated {
		t.Fatalf("member create status = %d, body = %s", createResponse.Code, createResponse.Body.String())
	}
}

func TestServerTrustedHeaderRequiresMembershipWhenRolesMissing(t *testing.T) {
	_, handler := newTestHandlerWithOptions(ServerOptions{AuthMode: "trusted-header"})
	request := httptest.NewRequest(http.MethodGet, "/api/chats", nil)
	setTrustedActorWithoutRoles(request, "unknown-user", "unknown-project")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("missing membership status = %d, body = %s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), "成员关系") {
		t.Fatalf("missing membership body = %s", response.Body.String())
	}
}

func TestServerTrustedHeaderBindsExternalIdentity(t *testing.T) {
	store, handler := newTestHandlerWithOptions(ServerOptions{AuthMode: "trusted-header"})
	store.SetProjectRole("identity-user", "project-a", "viewer")

	request := httptest.NewRequest(http.MethodGet, "/api/chats", nil)
	setTrustedActorWithoutRoles(request, "identity-user", "project-a")
	setTrustedActorEmail(request, "identity-user@example.test")
	setTrustedIdentity(request, "oidc", "https://issuer.example.test", "subject-a")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("bind identity status = %d, body = %s", response.Code, response.Body.String())
	}

	store.SetProjectRole("other-user", "project-a", "viewer")
	conflict := httptest.NewRequest(http.MethodGet, "/api/chats", nil)
	setTrustedActorWithoutRoles(conflict, "other-user", "project-a")
	setTrustedActorEmail(conflict, "other-user@example.test")
	setTrustedIdentity(conflict, "oidc", "https://issuer.example.test", "subject-a")
	conflictResponse := httptest.NewRecorder()
	handler.ServeHTTP(conflictResponse, conflict)
	if conflictResponse.Code != http.StatusConflict {
		t.Fatalf("external identity conflict status = %d, body = %s", conflictResponse.Code, conflictResponse.Body.String())
	}

	userConflict := httptest.NewRequest(http.MethodGet, "/api/chats", nil)
	setTrustedActorWithoutRoles(userConflict, "identity-user", "project-a")
	setTrustedActorEmail(userConflict, "identity-user@example.test")
	setTrustedIdentity(userConflict, "oidc", "https://issuer.example.test", "subject-other")
	userConflictResponse := httptest.NewRecorder()
	handler.ServeHTTP(userConflictResponse, userConflict)
	if userConflictResponse.Code != http.StatusConflict {
		t.Fatalf("user identity conflict status = %d, body = %s", userConflictResponse.Code, userConflictResponse.Body.String())
	}

	incomplete := httptest.NewRequest(http.MethodGet, "/api/chats", nil)
	setTrustedActorWithoutRoles(incomplete, "identity-user", "project-a")
	setTrustedActorEmail(incomplete, "identity-user@example.test")
	incomplete.Header.Set("X-NiceAgent-Identity-Subject", "subject-a")
	incompleteResponse := httptest.NewRecorder()
	handler.ServeHTTP(incompleteResponse, incomplete)
	if incompleteResponse.Code != http.StatusUnauthorized {
		t.Fatalf("incomplete identity status = %d, body = %s", incompleteResponse.Code, incompleteResponse.Body.String())
	}
}

func TestServerTrustedHeaderUsesPersistentOrganizationMembership(t *testing.T) {
	store, handler := newTestHandlerWithOptions(ServerOptions{AuthMode: "trusted-header"})
	store.SetOrganizationRole("org-admin", "org-project-a", "admin")
	store.SetProjectRole("org-admin", "project-a", "viewer")

	create := httptest.NewRequest(http.MethodPost, "/api/organizations/org-project-a/members", jsonBody(t, protocol.OrganizationMemberInput{
		UserID: "org-member",
		Role:   "viewer",
	}))
	setTrustedActorWithoutRoles(create, "org-admin", "project-a")
	createResponse := httptest.NewRecorder()
	handler.ServeHTTP(createResponse, create)
	if createResponse.Code != http.StatusCreated {
		t.Fatalf("organization member create with org membership status = %d, body = %s", createResponse.Code, createResponse.Body.String())
	}

	list := httptest.NewRequest(http.MethodGet, "/api/organizations/org-project-a/members", nil)
	setTrustedActorWithoutRoles(list, "org-admin", "project-a")
	listResponse := httptest.NewRecorder()
	handler.ServeHTTP(listResponse, list)
	if listResponse.Code != http.StatusOK {
		t.Fatalf("organization member list with org membership status = %d, body = %s", listResponse.Code, listResponse.Body.String())
	}
}

func TestServerTrustedHeaderAllowsOrgRoleForProjectAPI(t *testing.T) {
	store, handler := newTestHandlerWithOptions(ServerOptions{AuthMode: "trusted-header"})
	store.SetProjectOrganization("project-a", "org-project-a")
	store.SetOrganizationRole("org-owner", "org-project-a", "owner")

	createChat := httptest.NewRequest(http.MethodPost, "/api/chats", jsonBody(t, map[string]string{"title": "org inherited"}))
	setTrustedActorWithoutRoles(createChat, "org-owner", "project-a")
	createResponse := httptest.NewRecorder()
	handler.ServeHTTP(createResponse, createChat)
	if createResponse.Code != http.StatusCreated {
		t.Fatalf("org owner create project chat status = %d, body = %s", createResponse.Code, createResponse.Body.String())
	}

	createMember := httptest.NewRequest(http.MethodPost, "/api/projects/project-a/members", jsonBody(t, protocol.ProjectMemberInput{
		UserID: "project-member",
		Role:   "viewer",
	}))
	setTrustedActorWithoutRoles(createMember, "org-owner", "project-a")
	memberResponse := httptest.NewRecorder()
	handler.ServeHTTP(memberResponse, createMember)
	if memberResponse.Code != http.StatusCreated {
		t.Fatalf("org owner create project member status = %d, body = %s", memberResponse.Code, memberResponse.Body.String())
	}
}

func TestServerTrustedHeaderDoesNotInheritOrgRoleForUnrelatedProject(t *testing.T) {
	store, handler := newTestHandlerWithOptions(ServerOptions{AuthMode: "trusted-header"})
	store.SetProjectOrganization("project-a", "org-a")
	store.SetOrganizationRole("org-owner", "org-other", "owner")

	createChat := httptest.NewRequest(http.MethodPost, "/api/chats", jsonBody(t, map[string]string{"title": "wrong org"}))
	setTrustedActorWithoutRoles(createChat, "org-owner", "project-a")
	createChat.Header.Set("X-NiceAgent-Org-ID", "org-other")
	createResponse := httptest.NewRecorder()
	handler.ServeHTTP(createResponse, createChat)
	if createResponse.Code != http.StatusForbidden {
		t.Fatalf("unrelated org role create status = %d, body = %s", createResponse.Code, createResponse.Body.String())
	}
}

func TestServerTrustedHeaderRequiresOrganizationMembershipWhenRolesMissing(t *testing.T) {
	_, handler := newTestHandlerWithOptions(ServerOptions{AuthMode: "trusted-header"})
	request := httptest.NewRequest(http.MethodGet, "/api/organizations/org-project-a/members", nil)
	setTrustedActorWithoutRoles(request, "unknown-user", "project-a")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("missing organization membership status = %d, body = %s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), "组织") {
		t.Fatalf("missing organization membership body = %s", response.Body.String())
	}
}

func TestServerManagesProjectMembers(t *testing.T) {
	store, handler := newTestHandler()

	list := httptest.NewRequest(http.MethodGet, "/api/projects/"+app.DemoProjectID+"/members", nil)
	listResponse := httptest.NewRecorder()
	handler.ServeHTTP(listResponse, list)
	if listResponse.Code != http.StatusOK {
		t.Fatalf("list members status = %d, body = %s", listResponse.Code, listResponse.Body.String())
	}
	var listOutput protocol.ProjectMembersResponse
	decodeJSON(t, listResponse.Body, &listOutput)
	if len(listOutput.Members) != 1 || listOutput.Members[0].UserID != "demo-user" || listOutput.Members[0].Role != "owner" {
		t.Fatalf("initial members = %#v", listOutput.Members)
	}

	create := httptest.NewRequest(http.MethodPost, "/api/projects/"+app.DemoProjectID+"/members", jsonBody(t, protocol.ProjectMemberInput{
		UserID: "user-member",
		Email:  "member@example.test",
		Name:   "Member User",
		Role:   "viewer",
	}))
	createResponse := httptest.NewRecorder()
	handler.ServeHTTP(createResponse, create)
	if createResponse.Code != http.StatusCreated {
		t.Fatalf("create member status = %d, body = %s", createResponse.Code, createResponse.Body.String())
	}
	var createOutput protocol.ProjectMemberResponse
	decodeJSON(t, createResponse.Body, &createOutput)
	if createOutput.Member.UserID != "user-member" || createOutput.Member.Role != "viewer" {
		t.Fatalf("created member = %#v", createOutput.Member)
	}
	if roles := store.ListProjectRoles("user-member", app.DemoProjectID); len(roles) != 1 || roles[0] != "viewer" {
		t.Fatalf("created member roles = %#v", roles)
	}

	update := httptest.NewRequest(http.MethodPatch, "/api/projects/"+app.DemoProjectID+"/members/user-member", jsonBody(t, protocol.ProjectMemberInput{Role: "member"}))
	updateResponse := httptest.NewRecorder()
	handler.ServeHTTP(updateResponse, update)
	if updateResponse.Code != http.StatusOK {
		t.Fatalf("update member status = %d, body = %s", updateResponse.Code, updateResponse.Body.String())
	}
	var updateOutput protocol.ProjectMemberResponse
	decodeJSON(t, updateResponse.Body, &updateOutput)
	if updateOutput.Member.Role != "member" {
		t.Fatalf("updated member = %#v", updateOutput.Member)
	}

	remove := httptest.NewRequest(http.MethodDelete, "/api/projects/"+app.DemoProjectID+"/members/user-member", nil)
	removeResponse := httptest.NewRecorder()
	handler.ServeHTTP(removeResponse, remove)
	if removeResponse.Code != http.StatusOK {
		t.Fatalf("remove member status = %d, body = %s", removeResponse.Code, removeResponse.Body.String())
	}
	if roles := store.ListProjectRoles("user-member", app.DemoProjectID); len(roles) != 0 {
		t.Fatalf("removed member roles = %#v", roles)
	}
}

func TestServerManagesOrganizationMembers(t *testing.T) {
	_, handler := newTestHandler()

	list := httptest.NewRequest(http.MethodGet, "/api/organizations/"+app.DemoOrgID+"/members", nil)
	listResponse := httptest.NewRecorder()
	handler.ServeHTTP(listResponse, list)
	if listResponse.Code != http.StatusOK {
		t.Fatalf("list organization members status = %d, body = %s", listResponse.Code, listResponse.Body.String())
	}
	var listOutput protocol.OrganizationMembersResponse
	decodeJSON(t, listResponse.Body, &listOutput)
	if len(listOutput.Members) != 1 || listOutput.Members[0].UserID != "demo-user" || listOutput.Members[0].Role != "owner" {
		t.Fatalf("initial organization members = %#v", listOutput.Members)
	}

	create := httptest.NewRequest(http.MethodPost, "/api/organizations/"+app.DemoOrgID+"/members", jsonBody(t, protocol.OrganizationMemberInput{
		UserID: "org-member",
		Email:  "org-member@example.test",
		Name:   "Org Member",
		Role:   "admin",
	}))
	createResponse := httptest.NewRecorder()
	handler.ServeHTTP(createResponse, create)
	if createResponse.Code != http.StatusCreated {
		t.Fatalf("create organization member status = %d, body = %s", createResponse.Code, createResponse.Body.String())
	}
	var createOutput protocol.OrganizationMemberResponse
	decodeJSON(t, createResponse.Body, &createOutput)
	if createOutput.Member.UserID != "org-member" || createOutput.Member.Role != "admin" {
		t.Fatalf("created organization member = %#v", createOutput.Member)
	}

	update := httptest.NewRequest(http.MethodPatch, "/api/organizations/"+app.DemoOrgID+"/members/org-member", jsonBody(t, protocol.OrganizationMemberInput{Role: "viewer"}))
	updateResponse := httptest.NewRecorder()
	handler.ServeHTTP(updateResponse, update)
	if updateResponse.Code != http.StatusOK {
		t.Fatalf("update organization member status = %d, body = %s", updateResponse.Code, updateResponse.Body.String())
	}
	var updateOutput protocol.OrganizationMemberResponse
	decodeJSON(t, updateResponse.Body, &updateOutput)
	if updateOutput.Member.Role != "viewer" {
		t.Fatalf("updated organization member = %#v", updateOutput.Member)
	}

	remove := httptest.NewRequest(http.MethodDelete, "/api/organizations/"+app.DemoOrgID+"/members/org-member", nil)
	removeResponse := httptest.NewRecorder()
	handler.ServeHTTP(removeResponse, remove)
	if removeResponse.Code != http.StatusOK {
		t.Fatalf("remove organization member status = %d, body = %s", removeResponse.Code, removeResponse.Body.String())
	}
}

func TestServerRecordsSignedInvitationEmailWebhook(t *testing.T) {
	store, handler := newTestHandlerWithOptions(ServerOptions{
		InvitationWebhookSecret: "test-webhook-secret",
	})
	invitation, err := store.CreateInvitation(app.DemoOrgID, app.DemoUserID, protocol.InvitationInput{
		Email: "webhook-bounced@example.test",
		Role:  "viewer",
	})
	if err != nil {
		t.Fatalf("create invitation: %v", err)
	}
	delivery, err := store.EnqueueInvitationEmail(invitation, 2)
	if err != nil {
		t.Fatalf("enqueue invitation email: %v", err)
	}
	body := []byte(`{"invitation_id":"` + invitation.ID + `","delivery_id":"` + delivery.ID + `","provider":"smtp-test","provider_message_id":"message-a","type":"bounced","reason":"mailbox unavailable","payload":{"smtp_code":"550"}}`)
	request := httptest.NewRequest(http.MethodPost, "/webhooks/invitation-email-events", bytes.NewReader(body))
	request.Header.Set("X-NiceAgent-Webhook-Signature", signedWebhookBody("test-webhook-secret", body))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusAccepted {
		t.Fatalf("webhook status = %d, body = %s", response.Code, response.Body.String())
	}
	var output protocol.InvitationEmailEventResponse
	decodeJSON(t, response.Body, &output)
	if output.Event.Type != protocol.InvitationEmailEventBounced || output.Event.InvitationID != invitation.ID || output.Event.DeliveryID != delivery.ID {
		t.Fatalf("webhook event = %#v", output.Event)
	}
	events := store.ListInvitationEmailEvents(app.DemoOrgID, app.InvitationEmailEventListOptions{InvitationID: invitation.ID})
	if len(events) != 1 || events[0].Reason != "mailbox unavailable" {
		t.Fatalf("stored events = %#v, want bounced event", events)
	}
}

func TestServerRejectsUnsignedInvitationEmailWebhook(t *testing.T) {
	_, handler := newTestHandlerWithOptions(ServerOptions{
		InvitationWebhookSecret: "test-webhook-secret",
	})
	body := []byte(`{"invitation_id":"invitation-a","type":"bounced"}`)
	request := httptest.NewRequest(http.MethodPost, "/webhooks/invitation-email-events", bytes.NewReader(body))
	request.Header.Set("X-NiceAgent-Webhook-Signature", "sha256=bad")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("unsigned webhook status = %d, body = %s", response.Code, response.Body.String())
	}
}

func TestServerOrganizationMemberManagementRequiresAdminRole(t *testing.T) {
	store, handler := newTestHandlerWithOptions(ServerOptions{AuthMode: "trusted-header"})
	store.SetProjectRole("viewer-user", "project-a", "viewer")

	create := httptest.NewRequest(http.MethodPost, "/api/organizations/org-project-a/members", jsonBody(t, protocol.OrganizationMemberInput{
		UserID: "new-user",
		Role:   "member",
	}))
	setTrustedActorWithoutRoles(create, "viewer-user", "project-a")
	createResponse := httptest.NewRecorder()
	handler.ServeHTTP(createResponse, create)
	if createResponse.Code != http.StatusForbidden {
		t.Fatalf("viewer create organization member status = %d, body = %s", createResponse.Code, createResponse.Body.String())
	}
	if !strings.Contains(createResponse.Body.String(), "管理组织") {
		t.Fatalf("viewer organization deny body = %s", createResponse.Body.String())
	}
}

func TestServerOrganizationMemberManagementRejectsSelfMutation(t *testing.T) {
	_, handler := newTestHandler()
	update := httptest.NewRequest(http.MethodPatch, "/api/organizations/"+app.DemoOrgID+"/members/demo-user", jsonBody(t, protocol.OrganizationMemberInput{Role: "viewer"}))
	updateResponse := httptest.NewRecorder()
	handler.ServeHTTP(updateResponse, update)
	if updateResponse.Code != http.StatusBadRequest {
		t.Fatalf("self organization update status = %d, body = %s", updateResponse.Code, updateResponse.Body.String())
	}
	if !strings.Contains(updateResponse.Body.String(), "自己") {
		t.Fatalf("self organization update body = %s", updateResponse.Body.String())
	}
}

func TestServerManagesInvitations(t *testing.T) {
	store, handler := newTestHandlerWithOptions(ServerOptions{AuthMode: "trusted-header"})
	store.SetProjectOrganization("project-a", "org-project-a")
	store.SetOrganizationRole("org-owner", "org-project-a", "owner")

	createOrgInvite := httptest.NewRequest(http.MethodPost, "/api/organizations/org-project-a/invitations", jsonBody(t, protocol.InvitationInput{
		Email: "org-invited@example.test",
		Role:  "admin",
	}))
	setTrustedActorWithoutRoles(createOrgInvite, "org-owner", "project-a")
	createOrgResponse := httptest.NewRecorder()
	handler.ServeHTTP(createOrgResponse, createOrgInvite)
	if createOrgResponse.Code != http.StatusCreated {
		t.Fatalf("create org invitation status = %d, body = %s", createOrgResponse.Code, createOrgResponse.Body.String())
	}
	var orgInviteOutput protocol.InvitationResponse
	decodeJSON(t, createOrgResponse.Body, &orgInviteOutput)
	if orgInviteOutput.Invitation.Token == "" || orgInviteOutput.Invitation.Status != protocol.InvitationPending {
		t.Fatalf("created org invitation = %#v", orgInviteOutput.Invitation)
	}

	listInvites := httptest.NewRequest(http.MethodGet, "/api/organizations/org-project-a/invitations", nil)
	setTrustedActorWithoutRoles(listInvites, "org-owner", "project-a")
	listResponse := httptest.NewRecorder()
	handler.ServeHTTP(listResponse, listInvites)
	if listResponse.Code != http.StatusOK {
		t.Fatalf("list invitations status = %d, body = %s", listResponse.Code, listResponse.Body.String())
	}
	var listOutput protocol.InvitationsResponse
	decodeJSON(t, listResponse.Body, &listOutput)
	if len(listOutput.Invitations) != 1 || listOutput.Invitations[0].Token != "" {
		t.Fatalf("listed invitations = %#v, want redacted token", listOutput.Invitations)
	}

	acceptOrg := httptest.NewRequest(http.MethodPost, "/api/invitations/"+orgInviteOutput.Invitation.Token+"/accept", jsonBody(t, protocol.InvitationAcceptInput{Name: "Org Invited"}))
	setTrustedActorWithoutRoles(acceptOrg, "org-invited-user", "project-a")
	setTrustedActorEmail(acceptOrg, "org-invited@example.test")
	acceptOrgResponse := httptest.NewRecorder()
	handler.ServeHTTP(acceptOrgResponse, acceptOrg)
	if acceptOrgResponse.Code != http.StatusOK {
		t.Fatalf("accept org invitation status = %d, body = %s", acceptOrgResponse.Code, acceptOrgResponse.Body.String())
	}
	if roles := store.ListOrganizationRoles("org-invited-user", "org-project-a"); len(roles) != 1 || roles[0] != "admin" {
		t.Fatalf("org roles after invitation = %#v, want admin", roles)
	}

	createProjectInvite := httptest.NewRequest(http.MethodPost, "/api/organizations/org-project-a/invitations", jsonBody(t, protocol.InvitationInput{
		Email:     "project-invited@example.test",
		Role:      "viewer",
		ProjectID: "project-a",
	}))
	setTrustedActorWithoutRoles(createProjectInvite, "org-owner", "project-a")
	createProjectResponse := httptest.NewRecorder()
	handler.ServeHTTP(createProjectResponse, createProjectInvite)
	if createProjectResponse.Code != http.StatusCreated {
		t.Fatalf("create project invitation status = %d, body = %s", createProjectResponse.Code, createProjectResponse.Body.String())
	}
	var projectInviteOutput protocol.InvitationResponse
	decodeJSON(t, createProjectResponse.Body, &projectInviteOutput)
	acceptProject := httptest.NewRequest(http.MethodPost, "/api/invitations/"+projectInviteOutput.Invitation.Token+"/accept", jsonBody(t, protocol.InvitationAcceptInput{Name: "Project Invited"}))
	setTrustedActorWithoutRoles(acceptProject, "project-invited-user", "project-a")
	setTrustedActorEmail(acceptProject, "project-invited@example.test")
	acceptProjectResponse := httptest.NewRecorder()
	handler.ServeHTTP(acceptProjectResponse, acceptProject)
	if acceptProjectResponse.Code != http.StatusOK {
		t.Fatalf("accept project invitation status = %d, body = %s", acceptProjectResponse.Code, acceptProjectResponse.Body.String())
	}
	if roles := store.ListProjectRoles("project-invited-user", "project-a"); len(roles) != 1 || roles[0] != "viewer" {
		t.Fatalf("project roles after invitation = %#v, want viewer", roles)
	}

	createMismatchInvite := httptest.NewRequest(http.MethodPost, "/api/organizations/org-project-a/invitations", jsonBody(t, protocol.InvitationInput{
		Email: "mismatch@example.test",
		Role:  "viewer",
	}))
	setTrustedActorWithoutRoles(createMismatchInvite, "org-owner", "project-a")
	mismatchInviteResponse := httptest.NewRecorder()
	handler.ServeHTTP(mismatchInviteResponse, createMismatchInvite)
	if mismatchInviteResponse.Code != http.StatusCreated {
		t.Fatalf("create mismatch invitation status = %d, body = %s", mismatchInviteResponse.Code, mismatchInviteResponse.Body.String())
	}
	var mismatchInviteOutput protocol.InvitationResponse
	decodeJSON(t, mismatchInviteResponse.Body, &mismatchInviteOutput)
	acceptMismatch := httptest.NewRequest(http.MethodPost, "/api/invitations/"+mismatchInviteOutput.Invitation.Token+"/accept", jsonBody(t, protocol.InvitationAcceptInput{Name: "Wrong"}))
	setTrustedActorWithoutRoles(acceptMismatch, "wrong-email-user", "project-a")
	setTrustedActorEmail(acceptMismatch, "wrong@example.test")
	mismatchResponse := httptest.NewRecorder()
	handler.ServeHTTP(mismatchResponse, acceptMismatch)
	if mismatchResponse.Code != http.StatusBadRequest {
		t.Fatalf("accept mismatch invitation status = %d, body = %s", mismatchResponse.Code, mismatchResponse.Body.String())
	}

	acceptWithoutEmail := httptest.NewRequest(http.MethodPost, "/api/invitations/"+mismatchInviteOutput.Invitation.Token+"/accept", jsonBody(t, protocol.InvitationAcceptInput{Name: "Missing"}))
	setTrustedActorWithoutRoles(acceptWithoutEmail, "missing-email-user", "project-a")
	missingEmailResponse := httptest.NewRecorder()
	handler.ServeHTTP(missingEmailResponse, acceptWithoutEmail)
	if missingEmailResponse.Code != http.StatusBadRequest {
		t.Fatalf("accept missing email status = %d, body = %s", missingEmailResponse.Code, missingEmailResponse.Body.String())
	}
}

func TestServerSendsInvitationEmailWhenMailerConfigured(t *testing.T) {
	fakeMailer := &recordingInvitationMailer{}
	store, handler := newTestHandlerWithOptions(ServerOptions{
		AuthMode:         "trusted-header",
		InvitationMailer: fakeMailer,
	})
	store.SetProjectOrganization("project-a", "org-project-a")
	store.SetOrganizationRole("org-owner", "org-project-a", "owner")

	request := httptest.NewRequest(http.MethodPost, "/api/organizations/org-project-a/invitations", jsonBody(t, protocol.InvitationInput{
		Email:     "project-invited@example.test",
		Role:      "viewer",
		ProjectID: "project-a",
	}))
	setTrustedActorWithoutRoles(request, "org-owner", "project-a")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusCreated {
		t.Fatalf("create invitation status = %d, body = %s", response.Code, response.Body.String())
	}
	if len(fakeMailer.sent) != 1 {
		t.Fatalf("sent invitations = %d, want 1", len(fakeMailer.sent))
	}
	sent := fakeMailer.sent[0]
	if sent.Email != "project-invited@example.test" || sent.Token == "" || sent.ProjectID != "project-a" {
		t.Fatalf("sent invitation = %#v", sent)
	}
	events := store.ListAuditEvents(app.ActorContext{UserID: "org-owner", ProjectID: "project-a", OrgID: "org-project-a", Roles: []string{"owner"}}, app.AuditEventListOptions{Action: "invitation.email.send"})
	if len(events) != 1 || events[0].Decision != protocol.AuditDecisionAllow {
		t.Fatalf("email audit events = %#v", events)
	}
}

func TestServerProjectMemberManagementRequiresAdminRole(t *testing.T) {
	store, handler := newTestHandlerWithOptions(ServerOptions{AuthMode: "trusted-header"})
	store.SetProjectRole("viewer-user", "project-a", "viewer")

	create := httptest.NewRequest(http.MethodPost, "/api/projects/project-a/members", jsonBody(t, protocol.ProjectMemberInput{
		UserID: "new-user",
		Role:   "member",
	}))
	setTrustedActorWithoutRoles(create, "viewer-user", "project-a")
	createResponse := httptest.NewRecorder()
	handler.ServeHTTP(createResponse, create)
	if createResponse.Code != http.StatusForbidden {
		t.Fatalf("viewer create member status = %d, body = %s", createResponse.Code, createResponse.Body.String())
	}
	if !strings.Contains(createResponse.Body.String(), "管理项目") {
		t.Fatalf("viewer deny body = %s", createResponse.Body.String())
	}
}

func TestServerProjectMemberManagementRejectsSelfMutation(t *testing.T) {
	_, handler := newTestHandler()
	update := httptest.NewRequest(http.MethodPatch, "/api/projects/"+app.DemoProjectID+"/members/demo-user", jsonBody(t, protocol.ProjectMemberInput{Role: "viewer"}))
	updateResponse := httptest.NewRecorder()
	handler.ServeHTTP(updateResponse, update)
	if updateResponse.Code != http.StatusBadRequest {
		t.Fatalf("self update status = %d, body = %s", updateResponse.Code, updateResponse.Body.String())
	}
	if !strings.Contains(updateResponse.Body.String(), "自己") {
		t.Fatalf("self update body = %s", updateResponse.Body.String())
	}
}

func TestServerManagesProjectQuotaPolicyAndEnforcesIt(t *testing.T) {
	store := repository.NewStore()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	dispatcher := dispatchFunc(func(context.Context, protocol.Run, string) error { return nil })
	handler := NewServerWithOptions(store, dispatcher, log, ServerOptions{}).Handler()

	getQuota := httptest.NewRequest(http.MethodGet, "/api/projects/"+app.DemoProjectID+"/quota", nil)
	getQuotaResponse := httptest.NewRecorder()
	handler.ServeHTTP(getQuotaResponse, getQuota)
	if getQuotaResponse.Code != http.StatusOK {
		t.Fatalf("get quota status = %d, body = %s", getQuotaResponse.Code, getQuotaResponse.Body.String())
	}
	var getOutput protocol.ProjectQuotaPolicyResponse
	decodeJSON(t, getQuotaResponse.Body, &getOutput)
	if getOutput.Policy.ProjectID != app.DemoProjectID || getOutput.Policy.MaxConcurrentRuns != 0 {
		t.Fatalf("default quota policy = %#v", getOutput.Policy)
	}

	updateQuota := httptest.NewRequest(http.MethodPatch, "/api/projects/"+app.DemoProjectID+"/quota", jsonBody(t, protocol.ProjectQuotaPolicyInput{
		MaxConcurrentRuns: 1,
	}))
	updateQuotaResponse := httptest.NewRecorder()
	handler.ServeHTTP(updateQuotaResponse, updateQuota)
	if updateQuotaResponse.Code != http.StatusOK {
		t.Fatalf("update quota status = %d, body = %s", updateQuotaResponse.Code, updateQuotaResponse.Body.String())
	}
	var updateOutput protocol.ProjectQuotaPolicyResponse
	decodeJSON(t, updateQuotaResponse.Body, &updateOutput)
	if updateOutput.Policy.MaxConcurrentRuns != 1 {
		t.Fatalf("updated quota policy = %#v", updateOutput.Policy)
	}

	chat := createChatWithHandler(t, handler, "persistent quota")
	first := httptest.NewRequest(http.MethodPost, "/api/chats/"+chat.ID+"/messages", jsonBody(t, map[string]string{"content": "first"}))
	firstResponse := httptest.NewRecorder()
	handler.ServeHTTP(firstResponse, first)
	if firstResponse.Code != http.StatusAccepted {
		t.Fatalf("first message status = %d, body = %s", firstResponse.Code, firstResponse.Body.String())
	}
	second := httptest.NewRequest(http.MethodPost, "/api/chats/"+chat.ID+"/messages", jsonBody(t, map[string]string{"content": "second"}))
	secondResponse := httptest.NewRecorder()
	handler.ServeHTTP(secondResponse, second)
	if secondResponse.Code != http.StatusTooManyRequests {
		t.Fatalf("second message status = %d, body = %s", secondResponse.Code, secondResponse.Body.String())
	}
}

func TestServerProjectQuotaManagementRequiresAdminRole(t *testing.T) {
	store, handler := newTestHandlerWithOptions(ServerOptions{AuthMode: "trusted-header"})
	store.SetProjectRole("viewer-user", "project-a", "viewer")

	updateQuota := httptest.NewRequest(http.MethodPatch, "/api/projects/project-a/quota", jsonBody(t, protocol.ProjectQuotaPolicyInput{MaxConcurrentRuns: 1}))
	setTrustedActorWithoutRoles(updateQuota, "viewer-user", "project-a")
	updateQuotaResponse := httptest.NewRecorder()
	handler.ServeHTTP(updateQuotaResponse, updateQuota)
	if updateQuotaResponse.Code != http.StatusForbidden {
		t.Fatalf("viewer update quota status = %d, body = %s", updateQuotaResponse.Code, updateQuotaResponse.Body.String())
	}
	if !strings.Contains(updateQuotaResponse.Body.String(), "管理项目") {
		t.Fatalf("viewer quota deny body = %s", updateQuotaResponse.Body.String())
	}
}

func TestServerReturnsProjectUsageBuckets(t *testing.T) {
	store := repository.NewStore()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	handler := NewServerWithOptions(store, dispatchFunc(func(context.Context, protocol.Run, string) error { return nil }), log, ServerOptions{}).Handler()
	chat, err := store.CreateChat(app.DemoUserID, app.DemoProjectID, "usage")
	if err != nil {
		t.Fatalf("create chat: %v", err)
	}
	_, run, err := store.AddUserMessage(chat.ID, app.DemoUserID, "hello")
	if err != nil {
		t.Fatalf("add message: %v", err)
	}
	if _, err := store.SaveRunUsage(run.ID, protocol.RunUsage{
		Provider:              "openai-compatible",
		Model:                 "deepseek-chat",
		InputTokens:           10,
		OutputTokens:          7,
		Estimated:             true,
		TokenEstimator:        "heuristic_rune_div4",
		Cost:                  0.001,
		Currency:              "USD",
		ToolCalls:             1,
		SandboxDurationMillis: 1200,
		ArtifactBytes:         128,
	}); err != nil {
		t.Fatalf("save usage: %v", err)
	}

	request := httptest.NewRequest(http.MethodGet, "/api/projects/"+app.DemoProjectID+"/usage?window=24h", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("usage status = %d, body = %s", response.Code, response.Body.String())
	}
	var output protocol.ProjectUsageResponse
	decodeJSON(t, response.Body, &output)
	if output.ProjectID != app.DemoProjectID || output.Window != "24h" || len(output.Buckets) != 1 {
		t.Fatalf("usage output = %#v", output)
	}
	bucket := output.Buckets[0]
	if bucket.Provider != "openai-compatible" || bucket.Model != "deepseek-chat" ||
		bucket.RunCount != 1 || bucket.TotalTokens != 17 || bucket.ToolCalls != 1 ||
		bucket.TokenEstimator != "heuristic_rune_div4" || !bucket.Estimated {
		t.Fatalf("usage bucket = %#v, want grouped provider/model usage", bucket)
	}
	if output.Total.RunCount != 1 || output.Total.TotalTokens != 17 || output.Total.ArtifactBytes != 128 {
		t.Fatalf("usage total = %#v, want summed totals", output.Total)
	}
}

func TestServerProjectUsageRequiresAdminRole(t *testing.T) {
	store, handler := newTestHandlerWithOptions(ServerOptions{AuthMode: "trusted-header"})
	store.SetProjectRole("viewer-user", "project-a", "viewer")

	request := httptest.NewRequest(http.MethodGet, "/api/projects/project-a/usage", nil)
	setTrustedActorWithoutRoles(request, "viewer-user", "project-a")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("viewer usage status = %d, body = %s", response.Code, response.Body.String())
	}
}

func TestServerEnforcesRunQuota(t *testing.T) {
	store := repository.NewStore()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	dispatcher := dispatchFunc(func(context.Context, protocol.Run, string) error { return nil })
	handler := NewServerWithOptions(store, dispatcher, log, ServerOptions{
		RunQuota: RunQuota{MaxConcurrentRuns: 1},
	}).Handler()
	chat := createChatWithHandler(t, handler, "quota")

	first := httptest.NewRequest(http.MethodPost, "/api/chats/"+chat.ID+"/messages", jsonBody(t, map[string]string{"content": "first"}))
	firstResponse := httptest.NewRecorder()
	handler.ServeHTTP(firstResponse, first)
	if firstResponse.Code != http.StatusAccepted {
		t.Fatalf("first message status = %d, body = %s", firstResponse.Code, firstResponse.Body.String())
	}

	second := httptest.NewRequest(http.MethodPost, "/api/chats/"+chat.ID+"/messages", jsonBody(t, map[string]string{"content": "second"}))
	secondResponse := httptest.NewRecorder()
	handler.ServeHTTP(secondResponse, second)
	if secondResponse.Code != http.StatusTooManyRequests {
		t.Fatalf("second message status = %d, body = %s", secondResponse.Code, secondResponse.Body.String())
	}
	if !strings.Contains(secondResponse.Body.String(), "并发任务上限") {
		t.Fatalf("quota response body = %s", secondResponse.Body.String())
	}
	auditEvents := store.ListAuditEvents(app.DemoActor(), app.AuditEventListOptions{Action: "quota.run.create"})
	if len(auditEvents) != 1 || auditEvents[0].Decision != protocol.AuditDecisionDeny {
		t.Fatalf("quota audit events = %#v", auditEvents)
	}
}

func TestServerEnforcesHourlyRunQuota(t *testing.T) {
	store := repository.NewStore()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	dispatcher := dispatchFunc(func(context.Context, protocol.Run, string) error { return nil })
	handler := NewServerWithOptions(store, dispatcher, log, ServerOptions{
		RunQuota: RunQuota{MaxRunsPerHour: 1},
	}).Handler()
	chat := createChatWithHandler(t, handler, "hourly quota")

	first := httptest.NewRequest(http.MethodPost, "/api/chats/"+chat.ID+"/messages", jsonBody(t, map[string]string{"content": "first"}))
	firstResponse := httptest.NewRecorder()
	handler.ServeHTTP(firstResponse, first)
	if firstResponse.Code != http.StatusAccepted {
		t.Fatalf("first message status = %d, body = %s", firstResponse.Code, firstResponse.Body.String())
	}

	second := httptest.NewRequest(http.MethodPost, "/api/chats/"+chat.ID+"/messages", jsonBody(t, map[string]string{"content": "second"}))
	secondResponse := httptest.NewRecorder()
	handler.ServeHTTP(secondResponse, second)
	if secondResponse.Code != http.StatusTooManyRequests {
		t.Fatalf("second message status = %d, body = %s", secondResponse.Code, secondResponse.Body.String())
	}
	if !strings.Contains(secondResponse.Body.String(), "每小时任务数上限") {
		t.Fatalf("quota response body = %s", secondResponse.Body.String())
	}
}

func TestServerUsesQuotaLimiterBeforeCreatingRun(t *testing.T) {
	store := repository.NewStore()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	limiter := &fakeQuotaLimiter{
		denial: quotapkg.Denial{
			Message:  "已达到当前项目并发任务上限，请稍后再试。",
			Metadata: map[string]any{"quota": "concurrent_runs", "source": "redis"},
		},
	}
	dispatcher := dispatchFunc(func(context.Context, protocol.Run, string) error {
		t.Fatal("dispatcher should not be called when quota limiter denies")
		return nil
	})
	handler := NewServerWithOptions(store, dispatcher, log, ServerOptions{
		RunQuota:     RunQuota{MaxConcurrentRuns: 1},
		QuotaLimiter: limiter,
	}).Handler()
	chat := createChatWithHandler(t, handler, "redis quota")

	create := httptest.NewRequest(http.MethodPost, "/api/chats/"+chat.ID+"/messages", jsonBody(t, map[string]string{"content": "blocked"}))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, create)
	if response.Code != http.StatusTooManyRequests {
		t.Fatalf("quota response status = %d, body = %s", response.Code, response.Body.String())
	}
	_, messages, err := store.GetChat(chat.ID)
	if err != nil {
		t.Fatalf("get chat: %v", err)
	}
	if len(messages) != 0 {
		t.Fatalf("messages after denied request = %#v, want none", messages)
	}
	if limiter.reserveCalls != 1 {
		t.Fatalf("reserve calls = %d, want 1", limiter.reserveCalls)
	}
}

func TestServerPassesDynamicTokenReservationHintToLimiter(t *testing.T) {
	store := repository.NewStore()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	limiter := &fakeHintedQuotaLimiter{}
	dispatcher := dispatchFunc(func(context.Context, protocol.Run, string) error { return nil })
	handler := NewServerWithOptions(store, dispatcher, log, ServerOptions{
		RunQuota:     RunQuota{MaxTokensPerDay: 1000},
		QuotaLimiter: limiter,
		TokenReservation: TokenReservationOptions{
			Mode:         "dynamic",
			OutputBuffer: 10,
		},
	}).Handler()
	chat := createChatWithHandler(t, handler, "dynamic token quota")

	create := httptest.NewRequest(http.MethodPost, "/api/chats/"+chat.ID+"/messages", jsonBody(t, map[string]string{"content": "1234567890123456"}))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, create)
	if response.Code != http.StatusAccepted {
		t.Fatalf("create response status = %d, body = %s", response.Code, response.Body.String())
	}
	if limiter.hint.ModelTokens != 15 {
		t.Fatalf("dynamic reservation hint = %#v, want 15", limiter.hint)
	}
}

func TestServerEnforcesDailyModelTokenQuota(t *testing.T) {
	store := repository.NewStore()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	dispatcher := dispatchFunc(func(context.Context, protocol.Run, string) error { return nil })
	handler := NewServerWithOptions(store, dispatcher, log, ServerOptions{
		RunQuota: RunQuota{MaxTokensPerDay: 10},
	}).Handler()
	chat := createChatWithHandler(t, handler, "token quota")

	first := httptest.NewRequest(http.MethodPost, "/api/chats/"+chat.ID+"/messages", jsonBody(t, map[string]string{"content": "first"}))
	firstResponse := httptest.NewRecorder()
	handler.ServeHTTP(firstResponse, first)
	if firstResponse.Code != http.StatusAccepted {
		t.Fatalf("first message status = %d, body = %s", firstResponse.Code, firstResponse.Body.String())
	}
	var firstOutput struct {
		Run protocol.Run `json:"run"`
	}
	decodeJSON(t, firstResponse.Body, &firstOutput)
	if _, err := store.SaveRunUsage(firstOutput.Run.ID, protocol.RunUsage{InputTokens: 6, OutputTokens: 5}); err != nil {
		t.Fatalf("save run usage: %v", err)
	}

	second := httptest.NewRequest(http.MethodPost, "/api/chats/"+chat.ID+"/messages", jsonBody(t, map[string]string{"content": "second"}))
	secondResponse := httptest.NewRecorder()
	handler.ServeHTTP(secondResponse, second)
	if secondResponse.Code != http.StatusTooManyRequests {
		t.Fatalf("second message status = %d, body = %s", secondResponse.Code, secondResponse.Body.String())
	}
	if !strings.Contains(secondResponse.Body.String(), "每日模型 token 上限") {
		t.Fatalf("quota response body = %s", secondResponse.Body.String())
	}
}

func TestServerEnforcesDailyToolAndSandboxQuota(t *testing.T) {
	store := repository.NewStore()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	dispatcher := dispatchFunc(func(context.Context, protocol.Run, string) error { return nil })
	handler := NewServerWithOptions(store, dispatcher, log, ServerOptions{
		RunQuota: RunQuota{
			MaxToolCallsPerDay:      2,
			MaxSandboxSecondsPerDay: 3,
		},
	}).Handler()
	chat := createChatWithHandler(t, handler, "usage quota")

	first := httptest.NewRequest(http.MethodPost, "/api/chats/"+chat.ID+"/messages", jsonBody(t, map[string]string{"content": "first"}))
	firstResponse := httptest.NewRecorder()
	handler.ServeHTTP(firstResponse, first)
	if firstResponse.Code != http.StatusAccepted {
		t.Fatalf("first message status = %d, body = %s", firstResponse.Code, firstResponse.Body.String())
	}
	var firstOutput struct {
		Run protocol.Run `json:"run"`
	}
	decodeJSON(t, firstResponse.Body, &firstOutput)
	if _, err := store.SaveRunUsage(firstOutput.Run.ID, protocol.RunUsage{ToolCalls: 2, SandboxDurationMillis: 2999}); err != nil {
		t.Fatalf("save run usage: %v", err)
	}

	second := httptest.NewRequest(http.MethodPost, "/api/chats/"+chat.ID+"/messages", jsonBody(t, map[string]string{"content": "second"}))
	secondResponse := httptest.NewRecorder()
	handler.ServeHTTP(secondResponse, second)
	if secondResponse.Code != http.StatusTooManyRequests {
		t.Fatalf("second message status = %d, body = %s", secondResponse.Code, secondResponse.Body.String())
	}
	if !strings.Contains(secondResponse.Body.String(), "每日工具调用上限") {
		t.Fatalf("tool quota response body = %s", secondResponse.Body.String())
	}

	store = repository.NewStore()
	handler = NewServerWithOptions(store, dispatcher, log, ServerOptions{
		RunQuota: RunQuota{MaxSandboxSecondsPerDay: 3},
	}).Handler()
	chat = createChatWithHandler(t, handler, "sandbox quota")
	first = httptest.NewRequest(http.MethodPost, "/api/chats/"+chat.ID+"/messages", jsonBody(t, map[string]string{"content": "first"}))
	firstResponse = httptest.NewRecorder()
	handler.ServeHTTP(firstResponse, first)
	if firstResponse.Code != http.StatusAccepted {
		t.Fatalf("first sandbox message status = %d, body = %s", firstResponse.Code, firstResponse.Body.String())
	}
	decodeJSON(t, firstResponse.Body, &firstOutput)
	if _, err := store.SaveRunUsage(firstOutput.Run.ID, protocol.RunUsage{ToolCalls: 1, SandboxDurationMillis: 3000}); err != nil {
		t.Fatalf("save sandbox usage: %v", err)
	}
	second = httptest.NewRequest(http.MethodPost, "/api/chats/"+chat.ID+"/messages", jsonBody(t, map[string]string{"content": "second"}))
	secondResponse = httptest.NewRecorder()
	handler.ServeHTTP(secondResponse, second)
	if secondResponse.Code != http.StatusTooManyRequests {
		t.Fatalf("second sandbox message status = %d, body = %s", secondResponse.Code, secondResponse.Body.String())
	}
	if !strings.Contains(secondResponse.Body.String(), "Sandbox 执行时长上限") {
		t.Fatalf("sandbox quota response body = %s", secondResponse.Body.String())
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

	auditRequest := httptest.NewRequest(http.MethodGet, "/api/audit/events?action=skill.create", nil)
	auditResponse := httptest.NewRecorder()
	handler.ServeHTTP(auditResponse, auditRequest)
	if auditResponse.Code != http.StatusOK {
		t.Fatalf("audit status = %d, body = %s", auditResponse.Code, auditResponse.Body.String())
	}
	if strings.Contains(auditResponse.Body.String(), "secret-token") {
		t.Fatalf("audit response leaked bearer token: %s", auditResponse.Body.String())
	}
	var auditOutput protocol.AuditEventsResponse
	decodeJSON(t, auditResponse.Body, &auditOutput)
	if len(auditOutput.Events) != 1 || auditOutput.Events[0].ResourceID != skill.ID {
		t.Fatalf("audit events = %#v, want skill.create for created skill", auditOutput.Events)
	}
}

func TestServerPreviewsOpenAPIImport(t *testing.T) {
	_, handler := newTestHandler()
	document := `{
		"openapi":"3.1.0",
		"servers":[{"url":"https://api.example.com"}],
		"components":{"securitySchemes":{"bearerAuth":{"type":"http","scheme":"bearer"}}},
		"security":[{"bearerAuth":[]}],
		"paths":{
			"/weather":{
				"post":{
					"operationId":"getWeather",
					"summary":"Get weather",
					"requestBody":{"content":{"application/json":{"schema":{"type":"object","properties":{"city":{"type":"string"}}}}}},
					"responses":{"200":{"content":{"application/json":{"schema":{"type":"object"}}}}}
				}
			}
		}
	}`
	request := httptest.NewRequest(http.MethodPost, "/api/skills/import/openapi/preview", jsonBody(t, protocol.OpenAPIImportPreviewInput{
		Document: document,
	}))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("preview status = %d, body = %s", response.Code, response.Body.String())
	}
	var output protocol.OpenAPIImportPreviewResponse
	decodeJSON(t, response.Body, &output)
	if len(output.Candidates) != 1 {
		t.Fatalf("candidates = %#v, want one", output.Candidates)
	}
	candidate := output.Candidates[0]
	if candidate.Name != "getWeather" || candidate.URL != "https://api.example.com/weather" {
		t.Fatalf("candidate = %#v, want weather candidate", candidate)
	}
	if candidate.AuthType != "bearer" || !candidate.RequiresSecret {
		t.Fatalf("candidate auth = %#v, want bearer secret hint", candidate)
	}
}

func TestServerCreatesOpenAPIImportedSkill(t *testing.T) {
	store, handler := newTestHandler()
	document := `{
		"openapi":"3.1.0",
		"servers":[{"url":"https://api.example.com"}],
		"components":{"securitySchemes":{"bearerAuth":{"type":"http","scheme":"bearer"}}},
		"security":[{"bearerAuth":[]}],
		"paths":{
			"/weather":{
				"post":{
					"operationId":"getWeather",
					"summary":"Get weather",
					"requestBody":{"content":{"application/json":{"schema":{"type":"object","properties":{"city":{"type":"string"}}}}}},
					"responses":{"200":{"content":{"application/json":{"schema":{"type":"object"}}}}}
				}
			}
		}
	}`
	request := httptest.NewRequest(http.MethodPost, "/api/skills/import/openapi", jsonBody(t, protocol.OpenAPIImportCreateInput{
		Document:             document,
		OperationID:          "getWeather",
		Name:                 "Weather Lookup",
		BearerTokenSecretRef: "env://WEATHER_TOKEN",
		TimeoutSeconds:       20,
		RetryMaxAttempts:     2,
		RateLimitPerMinute:   30,
	}))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("import status = %d, body = %s", response.Code, response.Body.String())
	}
	var skill protocol.Skill
	decodeJSON(t, response.Body, &skill)
	if skill.ID == "" || skill.Name != "Weather Lookup" || skill.Kind != protocol.SkillKindHTTP || skill.OwnerUserID != "demo-user" {
		t.Fatalf("skill = %#v", skill)
	}
	if !strings.Contains(skill.RuntimeConfig, `"url":"https://api.example.com/weather"`) || !strings.Contains(skill.RuntimeConfig, `"max_attempts":2`) {
		t.Fatalf("runtime config = %s", skill.RuntimeConfig)
	}
	runtimeSkills := store.ListRuntimeSkillsForUser("demo-user", "demo-project")
	var material protocol.RuntimeSecret
	for _, runtimeSkill := range runtimeSkills {
		if runtimeSkill.Skill.ID == skill.ID {
			material = runtimeSkill.SecretMaterial("bearer_token")
			break
		}
	}
	if material.SecretRef != "env://WEATHER_TOKEN" || material.EncryptedValue != "" {
		t.Fatalf("secret material = %#v", material)
	}
	auditRequest := httptest.NewRequest(http.MethodGet, "/api/audit/events?action=skill.import.create", nil)
	auditResponse := httptest.NewRecorder()
	handler.ServeHTTP(auditResponse, auditRequest)
	if auditResponse.Code != http.StatusOK {
		t.Fatalf("audit status = %d, body = %s", auditResponse.Code, auditResponse.Body.String())
	}
	var auditOutput protocol.AuditEventsResponse
	decodeJSON(t, auditResponse.Body, &auditOutput)
	if len(auditOutput.Events) != 1 || auditOutput.Events[0].ResourceID != skill.ID {
		t.Fatalf("audit events = %#v", auditOutput.Events)
	}
}

func TestServerRejectsOpenAPIImportWithoutRequiredSecret(t *testing.T) {
	_, handler := newTestHandler()
	document := `{
		"openapi":"3.1.0",
		"servers":[{"url":"https://api.example.com"}],
		"components":{"securitySchemes":{"bearerAuth":{"type":"http","scheme":"bearer"}}},
		"security":[{"bearerAuth":[]}],
		"paths":{
			"/weather":{"post":{"operationId":"getWeather","responses":{"200":{"content":{"application/json":{"schema":{"type":"object"}}}}}}}
		}
	}`
	request := httptest.NewRequest(http.MethodPost, "/api/skills/import/openapi", jsonBody(t, protocol.OpenAPIImportCreateInput{
		Document:    document,
		OperationID: "getWeather",
	}))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("import status = %d, body = %s; want 400", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), "bearer_token") {
		t.Fatalf("response body = %s, want bearer token error", response.Body.String())
	}
}

func TestServerRejectsInvalidOpenAPIImportPreview(t *testing.T) {
	_, handler := newTestHandler()
	request := httptest.NewRequest(http.MethodPost, "/api/skills/import/openapi/preview", jsonBody(t, protocol.OpenAPIImportPreviewInput{
		Document: `{"openapi":"3.1.0","paths":{"/x":{"get":{"responses":{}}}}}`,
		BaseURL:  "http://api.example.com",
	}))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("preview status = %d, body = %s; want 400", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), "base_url scheme must be https") {
		t.Fatalf("response body = %s, want https base url error", response.Body.String())
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
		if err := postJSON(input.ControlPlaneURL+"/internal/runs/"+input.Request.RunID+"/claim", protocol.RunClaimRequest{
			AttemptID: input.Request.AttemptID,
			ClaimedBy: "fake-runtime",
		}); err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		if err := postJSON(input.ControlPlaneURL+"/internal/runs/"+input.Request.RunID+"/events", protocol.RunEventWriteRequest{
			Type:      protocol.EventRunStarted,
			Message:   "fake runtime started",
			AttemptID: input.Request.AttemptID,
		}); err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		if err := postJSON(input.ControlPlaneURL+"/internal/runs/"+input.Request.RunID+"/events", protocol.RunEventWriteRequest{
			Type:      protocol.EventModelToken,
			Message:   "hello",
			AttemptID: input.Request.AttemptID,
		}); err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		if err := postJSON(input.ControlPlaneURL+"/internal/runs/"+input.Request.RunID+"/complete", protocol.RunCompleteRequest{
			Content:   "hello from fake runtime",
			AttemptID: input.Request.AttemptID,
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
	if !strings.HasPrefix(downloadResponse.Header().Get("Content-Disposition"), "attachment;") {
		t.Fatalf("content-disposition = %q, want attachment", downloadResponse.Header().Get("Content-Disposition"))
	}
	inlinePreview := httptest.NewRequest(http.MethodGet, "/api/artifacts/"+artifact.ID+"/download?disposition=inline", nil)
	inlinePreviewResponse := httptest.NewRecorder()
	handler.ServeHTTP(inlinePreviewResponse, inlinePreview)
	if inlinePreviewResponse.Code != http.StatusOK {
		t.Fatalf("inline preview status = %d, body = %s", inlinePreviewResponse.Code, inlinePreviewResponse.Body.String())
	}
	if !strings.HasPrefix(inlinePreviewResponse.Header().Get("Content-Disposition"), "inline;") {
		t.Fatalf("inline content-disposition = %q, want inline", inlinePreviewResponse.Header().Get("Content-Disposition"))
	}
	content := httptest.NewRequest(http.MethodGet, "/api/artifacts/"+artifact.ID+"/content?max_bytes=4", nil)
	contentResponse := httptest.NewRecorder()
	handler.ServeHTTP(contentResponse, content)
	if contentResponse.Code != http.StatusOK {
		t.Fatalf("content status = %d, body = %s", contentResponse.Code, contentResponse.Body.String())
	}
	var text protocol.ArtifactTextResponse
	decodeJSON(t, contentResponse.Body, &text)
	if text.Content != "repo" || !text.Truncated || text.BytesRead != 4 {
		t.Fatalf("text response = %#v", text)
	}
	if !containsEvent(store.ListEvents(run.ID, 0), protocol.EventArtifactCreated) {
		t.Fatalf("events = %#v, want artifact.created", store.ListEvents(run.ID, 0))
	}
}

func TestArtifactAPIsIsolateUsersAndProjects(t *testing.T) {
	workspaceRoot := t.TempDir()
	t.Setenv("SANDBOX_WORKSPACE_ROOT", workspaceRoot)
	store, handler := newTestHandlerWithOptions(ServerOptions{AuthMode: "trusted-header"})
	chat, err := store.CreateChat("user-a", "project-a", "artifact isolation")
	if err != nil {
		t.Fatalf("create chat: %v", err)
	}
	_, run, err := store.AddUserMessage(chat.ID, "user-a", "make artifact")
	if err != nil {
		t.Fatalf("add user message: %v", err)
	}
	outputDir := filepath.Join(workspaceRoot, run.WorkspaceID, "output")
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		t.Fatalf("mkdir output: %v", err)
	}
	if err := os.WriteFile(filepath.Join(outputDir, "report.txt"), []byte("private report"), 0o644); err != nil {
		t.Fatalf("write artifact: %v", err)
	}
	artifact, err := store.AddArtifact(protocol.Artifact{
		RunID:          run.ID,
		Path:           "output/report.txt",
		Name:           "report.txt",
		MimeType:       "text/plain",
		SizeBytes:      14,
		StorageBackend: "local",
	})
	if err != nil {
		t.Fatalf("add artifact: %v", err)
	}

	ownerList := httptest.NewRequest(http.MethodGet, "/api/runs/"+run.ID+"/artifacts", nil)
	setTrustedActor(ownerList, "user-a", "project-a")
	ownerListResponse := httptest.NewRecorder()
	handler.ServeHTTP(ownerListResponse, ownerList)
	if ownerListResponse.Code != http.StatusOK {
		t.Fatalf("owner list status = %d, body = %s", ownerListResponse.Code, ownerListResponse.Body.String())
	}

	for _, tc := range []struct {
		name      string
		userID    string
		projectID string
	}{
		{name: "other user", userID: "user-b", projectID: "project-a"},
		{name: "other project", userID: "user-a", projectID: "project-b"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			list := httptest.NewRequest(http.MethodGet, "/api/runs/"+run.ID+"/artifacts", nil)
			setTrustedActor(list, tc.userID, tc.projectID)
			listResponse := httptest.NewRecorder()
			handler.ServeHTTP(listResponse, list)
			if listResponse.Code != http.StatusNotFound {
				t.Fatalf("cross-scope list status = %d, body = %s", listResponse.Code, listResponse.Body.String())
			}

			get := httptest.NewRequest(http.MethodGet, "/api/artifacts/"+artifact.ID, nil)
			setTrustedActor(get, tc.userID, tc.projectID)
			getResponse := httptest.NewRecorder()
			handler.ServeHTTP(getResponse, get)
			if getResponse.Code != http.StatusNotFound {
				t.Fatalf("cross-scope get status = %d, body = %s", getResponse.Code, getResponse.Body.String())
			}

			download := httptest.NewRequest(http.MethodGet, "/api/artifacts/"+artifact.ID+"/download", nil)
			setTrustedActor(download, tc.userID, tc.projectID)
			downloadResponse := httptest.NewRecorder()
			handler.ServeHTTP(downloadResponse, download)
			if downloadResponse.Code != http.StatusNotFound {
				t.Fatalf("cross-scope download status = %d, body = %s", downloadResponse.Code, downloadResponse.Body.String())
			}

			content := httptest.NewRequest(http.MethodGet, "/api/artifacts/"+artifact.ID+"/content", nil)
			setTrustedActor(content, tc.userID, tc.projectID)
			contentResponse := httptest.NewRecorder()
			handler.ServeHTTP(contentResponse, content)
			if contentResponse.Code != http.StatusNotFound {
				t.Fatalf("cross-scope content status = %d, body = %s", contentResponse.Code, contentResponse.Body.String())
			}
		})
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

func TestInternalArtifactAPIsListAndReadTextWithAttemptFencing(t *testing.T) {
	workspaceRoot := t.TempDir()
	t.Setenv("SANDBOX_WORKSPACE_ROOT", workspaceRoot)
	store, handler := newTestHandler()
	chat := mustCreateChat(t, store, "demo-user", "workspace read")
	_, run, err := store.AddUserMessage(chat.ID, "demo-user", "make artifact")
	if err != nil {
		t.Fatalf("add user message: %v", err)
	}
	if _, err := store.ClaimRunAttempt(run.ID, "attempt-workspace", "runtime-a", time.Now().UTC().Add(time.Minute)); err != nil {
		t.Fatalf("claim run: %v", err)
	}
	outputDir := filepath.Join(workspaceRoot, run.WorkspaceID, "output")
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		t.Fatalf("mkdir output: %v", err)
	}
	if err := os.WriteFile(filepath.Join(outputDir, "report.txt"), []byte("hello workspace"), 0o644); err != nil {
		t.Fatalf("write artifact: %v", err)
	}
	artifact, err := store.AddArtifact(protocol.Artifact{
		RunID:       run.ID,
		Path:        "output/report.txt",
		Name:        "report.txt",
		MimeType:    "text/plain",
		SizeBytes:   15,
		WorkspaceID: run.WorkspaceID,
	})
	if err != nil {
		t.Fatalf("add artifact: %v", err)
	}

	list := httptest.NewRequest(http.MethodGet, "/internal/runs/"+run.ID+"/artifacts?attempt_id=attempt-workspace", nil)
	listResponse := httptest.NewRecorder()
	handler.ServeHTTP(listResponse, list)
	if listResponse.Code != http.StatusOK {
		t.Fatalf("internal list status = %d, body = %s", listResponse.Code, listResponse.Body.String())
	}
	var listed protocol.ArtifactListResponse
	decodeJSON(t, listResponse.Body, &listed)
	if len(listed.Artifacts) != 1 || listed.Artifacts[0].ID != artifact.ID {
		t.Fatalf("listed artifacts = %#v", listed.Artifacts)
	}

	read := httptest.NewRequest(http.MethodGet, "/internal/artifacts/"+artifact.ID+"/content?run_id="+run.ID+"&attempt_id=attempt-workspace&max_bytes=5", nil)
	readResponse := httptest.NewRecorder()
	handler.ServeHTTP(readResponse, read)
	if readResponse.Code != http.StatusOK {
		t.Fatalf("internal read status = %d, body = %s", readResponse.Code, readResponse.Body.String())
	}
	var text protocol.ArtifactTextResponse
	decodeJSON(t, readResponse.Body, &text)
	if text.Content != "hello" || !text.Truncated || text.BytesRead != 5 {
		t.Fatalf("text response = %#v", text)
	}

	stale := httptest.NewRequest(http.MethodGet, "/internal/runs/"+run.ID+"/artifacts?attempt_id=stale", nil)
	staleResponse := httptest.NewRecorder()
	handler.ServeHTTP(staleResponse, stale)
	if staleResponse.Code != http.StatusConflict {
		t.Fatalf("stale list status = %d, body = %s", staleResponse.Code, staleResponse.Body.String())
	}
}

func TestInternalRegisterRunArtifactsCreatesEventsAndIsAttemptFenced(t *testing.T) {
	store, handler := newTestHandler()
	chat := mustCreateChat(t, store, "demo-user", "incremental artifacts")
	_, run, err := store.AddUserMessage(chat.ID, "demo-user", "make artifact")
	if err != nil {
		t.Fatalf("add user message: %v", err)
	}
	if _, err := store.ClaimRunAttempt(run.ID, "attempt-artifact", "runtime-a", time.Now().UTC().Add(time.Minute)); err != nil {
		t.Fatalf("claim run: %v", err)
	}

	register := httptest.NewRequest(http.MethodPost, "/internal/runs/"+run.ID+"/artifacts", jsonBody(t, protocol.ArtifactWriteRequest{
		AttemptID: "attempt-artifact",
		Artifacts: []protocol.Artifact{{
			Path:           "output/report.txt",
			Name:           "report.txt",
			MimeType:       "text/plain",
			SizeBytes:      6,
			StorageBackend: "local",
		}},
	}))
	registerResponse := httptest.NewRecorder()
	handler.ServeHTTP(registerResponse, register)
	if registerResponse.Code != http.StatusCreated {
		t.Fatalf("register status = %d, body = %s", registerResponse.Code, registerResponse.Body.String())
	}
	var registered protocol.ArtifactListResponse
	decodeJSON(t, registerResponse.Body, &registered)
	if len(registered.Artifacts) != 1 {
		t.Fatalf("registered artifacts = %#v", registered.Artifacts)
	}
	artifact := registered.Artifacts[0]
	if artifact.ID == "" || artifact.RunID != run.ID || artifact.ChatID != chat.ID || artifact.UserID != "demo-user" || artifact.WorkspaceID != run.WorkspaceID {
		t.Fatalf("registered artifact identity = %#v", artifact)
	}
	if countEvents(store.ListEvents(run.ID, 0), protocol.EventArtifactCreated) != 1 {
		t.Fatalf("events = %#v, want one artifact.created", store.ListEvents(run.ID, 0))
	}

	complete := httptest.NewRequest(http.MethodPost, "/internal/runs/"+run.ID+"/complete", jsonBody(t, protocol.RunCompleteRequest{
		AttemptID: "attempt-artifact",
		Content:   "done",
		Artifacts: []protocol.Artifact{artifact},
	}))
	completeResponse := httptest.NewRecorder()
	handler.ServeHTTP(completeResponse, complete)
	if completeResponse.Code != http.StatusOK {
		t.Fatalf("complete status = %d, body = %s", completeResponse.Code, completeResponse.Body.String())
	}
	if countEvents(store.ListEvents(run.ID, 0), protocol.EventArtifactCreated) != 1 {
		t.Fatalf("events = %#v, want still one artifact.created", store.ListEvents(run.ID, 0))
	}
	if len(store.ListArtifacts(run.ID)) != 1 {
		t.Fatalf("artifacts = %#v, want one artifact", store.ListArtifacts(run.ID))
	}

	stale := httptest.NewRequest(http.MethodPost, "/internal/runs/"+run.ID+"/artifacts", jsonBody(t, protocol.ArtifactWriteRequest{
		AttemptID: "stale",
		Artifacts: []protocol.Artifact{{
			Path:     "output/stale.txt",
			MimeType: "text/plain",
		}},
	}))
	staleResponse := httptest.NewRecorder()
	handler.ServeHTTP(staleResponse, stale)
	if staleResponse.Code != http.StatusConflict {
		t.Fatalf("stale register status = %d, body = %s", staleResponse.Code, staleResponse.Body.String())
	}
}

func TestInternalArtifactReadRejectsUnsafeOrBinaryArtifacts(t *testing.T) {
	workspaceRoot := t.TempDir()
	t.Setenv("SANDBOX_WORKSPACE_ROOT", workspaceRoot)
	store, handler := newTestHandler()
	chat := mustCreateChat(t, store, "demo-user", "workspace read safety")
	_, run, err := store.AddUserMessage(chat.ID, "demo-user", "unsafe artifact")
	if err != nil {
		t.Fatalf("add user message: %v", err)
	}
	if _, err := store.ClaimRunAttempt(run.ID, "attempt-workspace", "runtime-a", time.Now().UTC().Add(time.Minute)); err != nil {
		t.Fatalf("claim run: %v", err)
	}
	outputDir := filepath.Join(workspaceRoot, run.WorkspaceID, "output")
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		t.Fatalf("mkdir output: %v", err)
	}
	if err := os.WriteFile(filepath.Join(outputDir, "image.bin"), []byte{0, 1, 2}, 0o644); err != nil {
		t.Fatalf("write binary artifact: %v", err)
	}
	binary, err := store.AddArtifact(protocol.Artifact{
		RunID:     run.ID,
		Path:      "output/image.bin",
		Name:      "image.bin",
		MimeType:  "application/octet-stream",
		SizeBytes: 3,
	})
	if err != nil {
		t.Fatalf("add binary artifact: %v", err)
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

	for _, artifactID := range []string{binary.ID, traversal.ID} {
		request := httptest.NewRequest(http.MethodGet, "/internal/artifacts/"+artifactID+"/content?run_id="+run.ID+"&attempt_id=attempt-workspace", nil)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("read %s status = %d, body = %s", artifactID, response.Code, response.Body.String())
		}
	}
}

type dispatchFunc func(context.Context, protocol.Run, string) error

func (f dispatchFunc) Dispatch(ctx context.Context, run protocol.Run, userMessage string) error {
	return f(ctx, run, userMessage)
}

type fakeQuotaLimiter struct {
	denial       quotapkg.Denial
	err          error
	reserveCalls int
	releaseCalls int
}

func (l *fakeQuotaLimiter) ReserveRun(context.Context, app.ActorContext, protocol.ProjectQuotaPolicy) (quotapkg.Reservation, quotapkg.Denial, error) {
	l.reserveCalls++
	if l.err != nil || l.denial.Message != "" {
		return nil, l.denial, l.err
	}
	return fakeQuotaReservation{}, quotapkg.Denial{}, nil
}

func (l *fakeQuotaLimiter) ReleaseRun(context.Context, protocol.Run, string, protocol.RunUsage) error {
	l.releaseCalls++
	return nil
}

type fakeHintedQuotaLimiter struct {
	fakeQuotaLimiter
	hint quotapkg.ReservationHint
}

func (l *fakeHintedQuotaLimiter) ReserveRunWithHint(_ context.Context, _ app.ActorContext, _ protocol.ProjectQuotaPolicy, hint quotapkg.ReservationHint) (quotapkg.Reservation, quotapkg.Denial, error) {
	l.reserveCalls++
	l.hint = hint
	return fakeQuotaReservation{}, quotapkg.Denial{}, nil
}

type fakeQuotaReservation struct{}

func (fakeQuotaReservation) Commit(context.Context, string) error { return nil }
func (fakeQuotaReservation) Rollback(context.Context) error       { return nil }

type recordingInvitationMailer struct {
	sent []protocol.Invitation
}

func (m *recordingInvitationMailer) SendInvitation(_ context.Context, invitation protocol.Invitation) error {
	m.sent = append(m.sent, invitation)
	return nil
}

func newTestHandler() (*repository.Store, http.Handler) {
	return newTestHandlerWithOptions(ServerOptions{})
}

func newTestHandlerWithOptions(opts ServerOptions) (*repository.Store, http.Handler) {
	store := repository.NewStore()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	dispatcher := dispatch.NewLocalDispatcher(store, log)
	return store, NewServerWithOptions(store, dispatcher, log, opts).Handler()
}

func setTrustedActor(request *http.Request, userID, projectID string) {
	setTrustedActorWithRoles(request, userID, projectID, "owner")
}

func setTrustedActorWithoutRoles(request *http.Request, userID, projectID string) {
	request.Header.Set("X-NiceAgent-User-ID", userID)
	request.Header.Set("X-NiceAgent-Project-ID", projectID)
	request.Header.Set("X-NiceAgent-Org-ID", "org-"+projectID)
}

func setTrustedActorWithRoles(request *http.Request, userID, projectID string, roles ...string) {
	request.Header.Set("X-NiceAgent-User-ID", userID)
	request.Header.Set("X-NiceAgent-Project-ID", projectID)
	request.Header.Set("X-NiceAgent-Org-ID", "org-"+projectID)
	request.Header.Set("X-NiceAgent-Roles", strings.Join(roles, ","))
}

func setTrustedActorEmail(request *http.Request, email string) {
	request.Header.Set("X-NiceAgent-User-Email", email)
}

func setTrustedIdentity(request *http.Request, provider, issuer, subject string) {
	request.Header.Set("X-NiceAgent-Identity-Provider", provider)
	request.Header.Set("X-NiceAgent-Identity-Issuer", issuer)
	request.Header.Set("X-NiceAgent-Identity-Subject", subject)
}

func jsonBody(t *testing.T, value any) io.Reader {
	t.Helper()
	body, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal json body: %v", err)
	}
	return bytes.NewReader(body)
}

func signedWebhookBody(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
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
	chat, err := repo.CreateChat(userID, app.DemoProjectID, title)
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

func countEvents(events []protocol.RunEvent, typ protocol.RunEventType) int {
	count := 0
	for _, event := range events {
		if event.Type == typ {
			count++
		}
	}
	return count
}
