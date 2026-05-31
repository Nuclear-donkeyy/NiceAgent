export type MessageRole = "system" | "user" | "assistant" | "tool";

export interface ChatSession {
  id: string;
  user_id: string;
  project_id: string;
  title: string;
  archived: boolean;
  last_run_id?: string;
  created_at: string;
  updated_at: string;
  message_count: number;
}

export interface Message {
  id: string;
  chat_id?: string;
  run_id?: string;
  role: MessageRole;
  content: string;
  created_at?: string;
  status?: string;
  transient?: boolean;
}

export interface ChatDetailResponse {
  chat: ChatSession;
  messages: Message[];
}
