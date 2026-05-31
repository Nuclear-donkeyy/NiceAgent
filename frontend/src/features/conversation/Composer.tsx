import type { FormEvent } from "react";

import styles from "./Conversation.module.css";

interface ComposerProps {
  input: string;
  sending: boolean;
  onInputChange: (value: string) => void;
  onSend: (event: FormEvent<HTMLFormElement>) => void;
}

export function Composer({ input, sending, onInputChange, onSend }: ComposerProps) {
  const canSend = !sending && input.trim().length > 0;
  return (
    <form className={styles.composer} onSubmit={onSend}>
      <textarea
        value={input}
        onChange={(event) => onInputChange(event.target.value)}
        placeholder="发送消息给远端 agent"
        rows={3}
        disabled={sending}
      />
      <button type="submit" disabled={!canSend}>
        {sending ? "发送中" : "发送"}
      </button>
    </form>
  );
}
