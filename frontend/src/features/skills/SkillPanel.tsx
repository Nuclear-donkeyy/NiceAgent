import { useState, type FormEvent } from "react";

import { Empty } from "../../components/Empty";
import { SectionTitle } from "../../components/SectionTitle";
import type { HTTPSkillInput, Skill, SkillGroups } from "../../domain/skill";
import styles from "./SkillPanel.module.scss";

const defaultForm: HTTPSkillInput = {
  name: "",
  description: "",
  url: "",
  method: "POST",
  auth_type: "none",
  bearer_token: "",
};

type FieldName = "name" | "description" | "url" | "bearer_token";
type FieldErrors = Partial<Record<FieldName, string>>;

interface SkillPanelProps {
  groups: SkillGroups;
  onCreateHTTPSkill: (input: HTTPSkillInput) => Promise<void>;
  onSetSkillEnabled: (skillID: string, enabled: boolean) => Promise<void>;
}

export function SkillPanel({ groups, onCreateHTTPSkill, onSetSkillEnabled }: SkillPanelProps) {
  const [formOpen, setFormOpen] = useState(false);
  const [form, setForm] = useState<HTTPSkillInput>(defaultForm);
  const [fieldErrors, setFieldErrors] = useState<FieldErrors>({});
  const [serverError, setServerError] = useState("");
  const [saving, setSaving] = useState(false);
  const [pendingSkillID, setPendingSkillID] = useState("");
  const [actionError, setActionError] = useState("");

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const errors = validateForm(form);
    setFieldErrors(errors);
    setServerError("");
    if (Object.keys(errors).length > 0) return;

    const payload: HTTPSkillInput = {
      ...form,
      name: form.name.trim(),
      description: form.description.trim(),
      url: form.url.trim(),
      bearer_token: form.auth_type === "bearer" ? form.bearer_token?.trim() : "",
      input_schema: '{"type":"object","additionalProperties":true}',
    };
    setSaving(true);
    try {
      await onCreateHTTPSkill(payload);
      setForm(defaultForm);
      setFieldErrors({});
      setFormOpen(false);
    } catch (error) {
      setServerError(errorMessage(error));
    } finally {
      setSaving(false);
    }
  }

  async function toggleSkill(skill: Skill) {
    setPendingSkillID(skill.id);
    setActionError("");
    try {
      await onSetSkillEnabled(skill.id, !skill.enabled);
    } catch (error) {
      setActionError(`Skill 更新失败：${errorMessage(error)}`);
    } finally {
      setPendingSkillID("");
    }
  }

  function updateForm<K extends keyof HTTPSkillInput>(field: K, value: HTTPSkillInput[K]) {
    setForm((prev) => ({ ...prev, [field]: value }));
    setServerError("");
    if (isFieldName(field)) {
      setFieldErrors((prev) => withoutFieldError(prev, field));
    }
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
          disabled={saving}
          onClick={() => setFormOpen((value) => !value)}
          type="button"
        >
          {formOpen ? "收起" : "添加"}
        </button>
      </div>

      {formOpen && (
        <form className={styles.form} noValidate onSubmit={submit}>
          <label className={styles.field}>
            <span>Skill 名称</span>
            <input
              aria-invalid={Boolean(fieldErrors.name)}
              disabled={saving}
              value={form.name}
              onBlur={() => setFieldErrors((prev) => mergeFieldError(prev, form, "name"))}
              onChange={(event) => updateForm("name", event.target.value)}
              placeholder="例如：天气查询"
            />
            {fieldErrors.name && <small>{fieldErrors.name}</small>}
          </label>
          <label className={styles.field}>
            <span>请求地址</span>
            <input
              aria-invalid={Boolean(fieldErrors.url)}
              disabled={saving}
              value={form.url}
              onBlur={() => setFieldErrors((prev) => mergeFieldError(prev, form, "url"))}
              onChange={(event) => updateForm("url", event.target.value)}
              placeholder="https://api.example.com/tool"
            />
            {fieldErrors.url && <small>{fieldErrors.url}</small>}
          </label>
          <label className={styles.field}>
            <span>调用说明</span>
            <textarea
              aria-invalid={Boolean(fieldErrors.description)}
              disabled={saving}
              value={form.description}
              onBlur={() => setFieldErrors((prev) => mergeFieldError(prev, form, "description"))}
              onChange={(event) => updateForm("description", event.target.value)}
              placeholder="什么时候应该调用这个能力"
              rows={2}
            />
            {fieldErrors.description && <small>{fieldErrors.description}</small>}
          </label>
          <div className={styles.formRow}>
            <select
              disabled={saving}
              value={form.method}
              onChange={(event) =>
                updateForm("method", event.target.value as HTTPSkillInput["method"])
              }
            >
              <option value="POST">POST</option>
              <option value="GET">GET</option>
            </select>
            <select
              disabled={saving}
              value={form.auth_type}
              onChange={(event) =>
                updateForm("auth_type", event.target.value as HTTPSkillInput["auth_type"])
              }
            >
              <option value="none">无鉴权</option>
              <option value="bearer">Bearer</option>
            </select>
          </div>
          {form.auth_type === "bearer" && (
            <label className={styles.field}>
              <span>Bearer Token</span>
              <input
                aria-invalid={Boolean(fieldErrors.bearer_token)}
                disabled={saving}
                value={form.bearer_token}
                onBlur={() => setFieldErrors((prev) => mergeFieldError(prev, form, "bearer_token"))}
                onChange={(event) => updateForm("bearer_token", event.target.value)}
                placeholder="Bearer token"
                type="password"
              />
              {fieldErrors.bearer_token && <small>{fieldErrors.bearer_token}</small>}
            </label>
          )}
          {serverError && (
            <p className={styles.formError} role="alert">
              服务端返回：{serverError}
            </p>
          )}
          <button className={styles.primaryButton} disabled={saving} type="submit">
            {saving ? "保存中..." : "保存 HTTP Skill"}
          </button>
        </form>
      )}

      {actionError && (
        <p className={styles.formError} role="alert">
          {actionError}
        </p>
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
              disabled={pendingSkillID === skill.id}
              onClick={() => void toggleSkill(skill)}
              type="button"
            >
              {pendingSkillID === skill.id ? "更新中..." : skill.enabled ? "停用" : "启用"}
            </button>
          </article>
        ))}
        {groups.user.length === 0 && <Empty text="还没有用户 Skill" />}
      </div>
    </>
  );
}

function validateForm(form: HTTPSkillInput): FieldErrors {
  return {
    ...validateField(form, "name"),
    ...validateField(form, "description"),
    ...validateField(form, "url"),
    ...validateField(form, "bearer_token"),
  };
}

function validateField(form: HTTPSkillInput, field: FieldName): FieldErrors {
  const errors: FieldErrors = {};
  if (field === "name") {
    const name = form.name.trim();
    if (!name) errors.name = "请输入 Skill 名称";
    else if (name.length > 60) errors.name = "名称最多 60 个字符";
  }
  if (field === "description") {
    if (!form.description.trim()) errors.description = "请填写调用说明";
  }
  if (field === "url") {
    const url = form.url.trim();
    if (!url) {
      errors.url = "请输入请求地址";
    } else {
      try {
        const parsed = new URL(url);
        if (!["http:", "https:"].includes(parsed.protocol)) {
          errors.url = "请求地址必须是 http 或 https";
        }
      } catch {
        errors.url = "请输入有效的 URL";
      }
    }
  }
  if (field === "bearer_token" && form.auth_type === "bearer" && !form.bearer_token?.trim()) {
    errors.bearer_token = "请输入 Bearer Token";
  }
  return errors;
}

function mergeFieldError(
  current: FieldErrors,
  form: HTTPSkillInput,
  field: FieldName,
): FieldErrors {
  return { ...withoutFieldError(current, field), ...validateField(form, field) };
}

function withoutFieldError(current: FieldErrors, field: FieldName): FieldErrors {
  const next = { ...current };
  delete next[field];
  return next;
}

function isFieldName(field: keyof HTTPSkillInput): field is FieldName {
  return ["name", "description", "url", "bearer_token"].includes(field);
}

function errorMessage(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}
