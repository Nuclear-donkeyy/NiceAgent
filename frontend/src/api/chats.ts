import { api } from "./client";
import type { ChatDetailResponse, ChatSession } from "../domain/chat";
import type { SendMessageResponse } from "../domain/run";

export interface ListChatsParams {
  query?: string;
  includeArchived?: boolean;
}

export async function listChats(params: ListChatsParams = {}): Promise<ChatSession[]> {
  const search = new URLSearchParams();
  if (params.query?.trim()) search.set("q", params.query.trim());
  if (params.includeArchived) search.set("include_archived", "true");
  const path = search.toString() ? `/api/chats?${search.toString()}` : "/api/chats";
  const data = await api<{ chats: ChatSession[] }>(path);
  return data.chats || [];
}

export function createChat(title: string): Promise<ChatSession> {
  return api<ChatSession>("/api/chats", {
    method: "POST",
    body: JSON.stringify({ title }),
  });
}

export function getChat(chatID: string): Promise<ChatDetailResponse> {
  return api<ChatDetailResponse>(`/api/chats/${encodeURIComponent(chatID)}`);
}

export function sendChatMessage(chatID: string, content: string): Promise<SendMessageResponse> {
  return api<SendMessageResponse>(`/api/chats/${encodeURIComponent(chatID)}/messages`, {
    method: "POST",
    body: JSON.stringify({ content }),
  });
}

export function setChatArchived(chatID: string, archived: boolean): Promise<ChatSession> {
  return api<ChatSession>(
    `/api/chats/${encodeURIComponent(chatID)}/${archived ? "archive" : "restore"}`,
    {
      method: "POST",
    },
  );
}
