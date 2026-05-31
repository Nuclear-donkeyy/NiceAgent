import { useState, type FormEvent } from "react";

import { Empty } from "../../components/Empty";
import { SectionTitle } from "../../components/SectionTitle";
import type { HTTPSkillInput, SkillGroups } from "../../domain/skill";
import styles from "./SkillPanel.module.scss";

const defaultForm: HTTPSkillInput = {
  name: "",
  description: "",
  url: "",
  method: "POST",
  auth_type: "none",
  bearer_token: "",
};

interface SkillPanelProps {
  groups: SkillGroups;
  onCreateHTTPSkill: (input: HTTPSkillInput) => Promise<void>;
  onSetSkillEnabled: (skillID: string, enabled: boolean) => Promise<void>;
}

export function SkillPanel({ groups, onCreateHTTPSkill, onSetSkillEnabled }: SkillPanelProps) {
  const [formOpen, setFormOpen] = useState(false);
  const [form, setForm] = useState<HTTPSkillInput>(defaultForm);

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const payload: HTTPSkillInput = {
      ...form,
      bearer_token: form.auth_type === "bearer" ? form.bearer_token : "",
      input_schema: '{"type":"object","additionalProperties":true}',
    };
    await onCreateHTTPSkill(payload);
    setForm(defaultForm);
    setFormOpen(false);
  }

  return (
    <>
      <SectionTitle text="系统能力" />
      <div className={styles.list}>
        {groups.system.map((skill) => (
          <article className={styles.card} key={skill.id}>
            <div className={styles.head}>
              <strong>{skill.name || skill.id}</strong>
              <span>系统</span>
            </div>
            <p>{skill.description || "暂无说明"}</p>
          </article>
        ))}
        {groups.system.length === 0 && <Empty text="暂无系统能力" />}
      </div>

      <div className={styles.sectionRow}>
        <SectionTitle text="我的能力" />
        <button
          className={styles.miniButton}
          onClick={() => setFormOpen((value) => !value)}
          type="button"
        >
          {formOpen ? "收起" : "添加"}
        </button>
      </div>

      {formOpen && (
        <form className={styles.form} onSubmit={submit}>
          <input
            value={form.name}
            onChange={(event) => setForm((prev) => ({ ...prev, name: event.target.value }))}
            placeholder="Skill 名称"
          />
          <input
            value={form.url}
            onChange={(event) => setForm((prev) => ({ ...prev, url: event.target.value }))}
            placeholder="https://api.example.com/tool"
          />
          <textarea
            value={form.description}
            onChange={(event) => setForm((prev) => ({ ...prev, description: event.target.value }))}
            placeholder="什么时候应该调用这个能力"
            rows={2}
          />
          <div className={styles.formRow}>
            <select
              value={form.method}
              onChange={(event) =>
                setForm((prev) => ({ ...prev, method: event.target.value as "GET" | "POST" }))
              }
            >
              <option value="POST">POST</option>
              <option value="GET">GET</option>
            </select>
            <select
              value={form.auth_type}
              onChange={(event) =>
                setForm((prev) => ({ ...prev, auth_type: event.target.value as "none" | "bearer" }))
              }
            >
              <option value="none">无鉴权</option>
              <option value="bearer">Bearer</option>
            </select>
          </div>
          {form.auth_type === "bearer" && (
            <input
              value={form.bearer_token}
              onChange={(event) =>
                setForm((prev) => ({ ...prev, bearer_token: event.target.value }))
              }
              placeholder="Bearer token，不会展示给前端列表"
              type="password"
            />
          )}
          <button className={styles.primaryButton} type="submit">
            保存 HTTP Skill
          </button>
        </form>
      )}

      <div className={styles.list}>
        {groups.user.map((skill) => (
          <article className={styles.card} key={skill.id}>
            <div className={styles.head}>
              <strong>{skill.name || skill.id}</strong>
              <span>{skill.enabled ? "已启用" : "已停用"}</span>
            </div>
            <p>{skill.description || "暂无说明"}</p>
            <button
              className={styles.secondaryButton}
              onClick={() => onSetSkillEnabled(skill.id, !skill.enabled)}
              type="button"
            >
              {skill.enabled ? "停用" : "启用"}
            </button>
          </article>
        ))}
        {groups.user.length === 0 && <Empty text="还没有用户 Skill" />}
      </div>
    </>
  );
}
