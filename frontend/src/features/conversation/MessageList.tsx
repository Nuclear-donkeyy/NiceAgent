import type { Message } from "../../domain/chat";
import { roleText } from "../../domain/labels";
import { Empty } from "../../components/Empty";
import styles from "./Conversation.module.css";

interface MessageListProps {
  messages: Message[];
  loading: boolean;
}

export function MessageList({ messages, loading }: MessageListProps) {
  if (loading) {
    return <Empty text="正在加载消息" />;
  }
  if (messages.length === 0) {
    return (
      <div className={styles.welcome}>
        <h3>今天要让远端 agent 做什么？</h3>
        <p>可以先试试 `/cli curl https://example.com`，让 agent 通过系统 CLI 获取外部信息。</p>
      </div>
    );
  }
  return (
    <>
      {messages.map((message) => (
        <MessageBubble key={message.id} message={message} />
      ))}
    </>
  );
}

function MessageBubble({ message }: { message: Message }) {
  return (
    <article className={[styles.message, styles[message.role] || ""].filter(Boolean).join(" ")}>
      <div className={styles.messageRole}>{roleText[message.role] || message.role}</div>
      {message.status && <div className={styles.agentStatus}>{message.status}</div>}
      {message.content ? <p>{message.content}</p> : <p className={styles.mutedText}>Agent 正在思考...</p>}
    </article>
  );
}
