import { ChatList } from "../features/chats/ChatList";
import { Composer } from "../features/conversation/Composer";
import { MessageList } from "../features/conversation/MessageList";
import { SkillPanel } from "../features/skills/SkillPanel";
import { statusText } from "../domain/labels";
import { useNiceAgentWorkspace } from "./useNiceAgentWorkspace";
import styles from "./App.module.scss";
import conversationStyles from "../features/conversation/Conversation.module.scss";

export default function App() {
  const workspace = useNiceAgentWorkspace();

  return (
    <main className={styles.shell}>
      <aside className={styles.sidebar}>
        <div className={styles.brand}>
          <div className={styles.brandMark}>NA</div>
          <div>
            <h1>NiceAgent</h1>
            <p>远端 agent 控制台</p>
          </div>
        </div>

        <div className={styles.sidebarActions}>
          <button onClick={workspace.createChat} type="button">
            新建
          </button>
          <button onClick={() => void workspace.refreshChats()} type="button">
            刷新
          </button>
        </div>

        <ChatList
          activeChatId={workspace.activeChatId}
          error={workspace.chatError}
          query={workspace.chatQuery}
          chats={workspace.chats}
          loading={workspace.chatsLoading}
          onQueryChange={workspace.setChatQuery}
          onSelectChat={(chatID) => void workspace.selectChat(chatID)}
          onShowArchivedChange={workspace.setShowArchived}
          showArchived={workspace.showArchived}
        />

        <SkillPanel
          groups={workspace.skillGroups}
          onCreateHTTPSkill={workspace.createHTTPSkill}
          onSetSkillEnabled={workspace.setSkillEnabled}
        />
      </aside>

      <section className={styles.main}>
        <header className={styles.topbar}>
          <div>
            <div className={styles.overline}>当前会话</div>
            <h2>{workspace.activeChat?.title || "新的对话"}</h2>
            {workspace.activeChat?.archived && (
              <p className={styles.topbarNote}>此会话已归档，可恢复后继续使用。</p>
            )}
          </div>
          <div className={styles.runState}>
            <span
              className={[styles.status, styles[workspace.runStatus] || ""]
                .filter(Boolean)
                .join(" ")}
            >
              {statusText[workspace.runStatus] || workspace.runStatus}
            </span>
            {workspace.activeChat && (
              <button
                className={styles.lineButton}
                onClick={() =>
                  void workspace.setActiveChatArchived(!workspace.activeChat?.archived)
                }
                type="button"
              >
                {workspace.activeChat.archived ? "恢复" : "归档"}
              </button>
            )}
            <button
              className={styles.lineButton}
              disabled={!workspace.canCancel}
              onClick={() => void workspace.cancelRun()}
              type="button"
            >
              取消
            </button>
          </div>
        </header>

        <section className={conversationStyles.conversation}>
          <MessageList loading={workspace.messageLoading} messages={workspace.visibleMessages} />
        </section>

        <Composer
          input={workspace.input}
          onInputChange={workspace.setInput}
          onSend={workspace.sendMessage}
          sending={workspace.sending}
        />
        <div className={styles.notice}>{workspace.notice}</div>
      </section>
    </main>
  );
}
