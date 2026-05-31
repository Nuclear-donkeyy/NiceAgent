import type { ChatSession } from "../../domain/chat";
import { Empty } from "../../components/Empty";
import { SectionTitle } from "../../components/SectionTitle";
import styles from "./ChatList.module.css";

interface ChatListProps {
  chats: ChatSession[];
  activeChatId: string | null;
  query: string;
  showArchived: boolean;
  loading: boolean;
  error: string;
  onQueryChange: (value: string) => void;
  onShowArchivedChange: (value: boolean) => void;
  onSelectChat: (chatID: string) => void;
}

export function ChatList({
  chats,
  activeChatId,
  query,
  showArchived,
  loading,
  error,
  onQueryChange,
  onShowArchivedChange,
  onSelectChat,
}: ChatListProps) {
  return (
    <>
      <SectionTitle text="会话" />
      <div className={styles.filters}>
        <input value={query} onChange={(event) => onQueryChange(event.target.value)} placeholder="搜索会话标题" />
        <label>
          <input
            type="checkbox"
            checked={showArchived}
            onChange={(event) => onShowArchivedChange(event.target.checked)}
          />
          显示归档
        </label>
      </div>
      <div className={styles.list}>
        {loading ? (
          <Empty text="正在加载会话" />
        ) : error ? (
          <Empty text={`会话加载失败：${error}`} />
        ) : chats.length === 0 ? (
          <Empty text={query ? "没有匹配的会话" : "还没有会话"} />
        ) : (
          chats.map((chat) => (
            <button
              key={chat.id}
              className={[
                styles.row,
                chat.id === activeChatId ? styles.active : "",
                chat.archived ? styles.archived : "",
              ]
                .filter(Boolean)
                .join(" ")}
              onClick={() => onSelectChat(chat.id)}
              type="button"
            >
              <span>
                {chat.title || "未命名会话"}
                {chat.archived && <em>归档</em>}
              </span>
              <small>{chat.message_count || 0} 条消息</small>
            </button>
          ))
        )}
      </div>
    </>
  );
}
