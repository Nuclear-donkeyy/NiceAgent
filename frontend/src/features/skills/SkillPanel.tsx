import { useState, type FormEvent } from "react";

import { Empty } from "../../components/Empty";
import { SectionTitle } from "../../components/SectionTitle";
import type {
  HTTPSkillImportCandidate,
  HTTPSkillInput,
  MCPImportPreviewInput,
  MCPImportPreviewResponse,
  MCPSkillImportCandidate,
  OpenAPIImportCreateInput,
  OpenAPIImportPreviewInput,
  OpenAPIImportPreviewResponse,
  Skill,
  SkillGroups,
} from "../../domain/skill";
import styles from "./SkillPanel.module.scss";

const defaultForm: HTTPSkillInput = {
  name: "",
  description: "",
  url: "",
  method: "POST",
  auth_type: "none",
  bearer_token: "",
  bearer_token_secret_ref: "",
};

type FieldName = "name" | "description" | "url" | "bearer_token" | "bearer_token_secret_ref";
type FieldErrors = Partial<Record<FieldName, string>>;
type ImportFieldName = "document" | "selected" | "bearer_token" | "bearer_token_secret_ref";
type ImportFieldErrors = Partial<Record<ImportFieldName, string>>;
type MCPPreviewFieldName = "document";
type MCPPreviewFieldErrors = Partial<Record<MCPPreviewFieldName, string>>;

interface ImportFormState {
  document: string;
  base_url: string;
  selected: string;
  bearer_token: string;
  bearer_token_secret_ref: string;
}

const defaultImportForm: ImportFormState = {
  document: "",
  base_url: "",
  selected: "",
  bearer_token: "",
  bearer_token_secret_ref: "",
};

interface MCPPreviewFormState {
  document: string;
}

const defaultMCPPreviewForm: MCPPreviewFormState = {
  document: "",
};

interface SkillPanelProps {
  groups: SkillGroups;
  onCreateHTTPSkill: (input: HTTPSkillInput) => Promise<void>;
  onCreateOpenAPIImportedSkill: (input: OpenAPIImportCreateInput) => Promise<void>;
  onPreviewOpenAPIImport: (
    input: OpenAPIImportPreviewInput,
  ) => Promise<OpenAPIImportPreviewResponse>;
  onPreviewMCPImport: (input: MCPImportPreviewInput) => Promise<MCPImportPreviewResponse>;
  onSetSkillEnabled: (skillID: string, enabled: boolean) => Promise<void>;
}

export function SkillPanel({
  groups,
  onCreateHTTPSkill,
  onCreateOpenAPIImportedSkill,
  onPreviewOpenAPIImport,
  onPreviewMCPImport,
  onSetSkillEnabled,
}: SkillPanelProps) {
  const [formOpen, setFormOpen] = useState(false);
  const [form, setForm] = useState<HTTPSkillInput>(defaultForm);
  const [fieldErrors, setFieldErrors] = useState<FieldErrors>({});
  const [serverError, setServerError] = useState("");
  const [saving, setSaving] = useState(false);
  const [importOpen, setImportOpen] = useState(false);
  const [importForm, setImportForm] = useState<ImportFormState>(defaultImportForm);
  const [importErrors, setImportErrors] = useState<ImportFieldErrors>({});
  const [importCandidates, setImportCandidates] = useState<HTTPSkillImportCandidate[]>([]);
  const [importError, setImportError] = useState("");
  const [previewingImport, setPreviewingImport] = useState(false);
  const [savingImport, setSavingImport] = useState(false);
  const [mcpPreviewOpen, setMCPPreviewOpen] = useState(false);
  const [mcpPreviewForm, setMCPPreviewForm] = useState<MCPPreviewFormState>(defaultMCPPreviewForm);
  const [mcpPreviewErrors, setMCPPreviewErrors] = useState<MCPPreviewFieldErrors>({});
  const [mcpCandidates, setMCPCandidates] = useState<MCPSkillImportCandidate[]>([]);
  const [mcpPreviewError, setMCPPreviewError] = useState("");
  const [previewingMCP, setPreviewingMCP] = useState(false);
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
      bearer_token_secret_ref:
        form.auth_type === "bearer" ? form.bearer_token_secret_ref?.trim() : "",
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

  async function previewImport(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const errors = validateImportForm(importForm, null);
    setImportErrors(errors);
    setImportError("");
    setImportCandidates([]);
    if (Object.keys(errors).length > 0) return;
    setPreviewingImport(true);
    try {
      const response = await onPreviewOpenAPIImport({
        document: importForm.document,
        base_url: importForm.base_url.trim() || undefined,
      });
      setImportCandidates(response.candidates || []);
      setImportForm((prev) => ({ ...prev, selected: candidateKey(response.candidates?.[0]) }));
    } catch (error) {
      setImportError(errorMessage(error));
    } finally {
      setPreviewingImport(false);
    }
  }

  async function saveImport() {
    const selected = selectedImportCandidate(importCandidates, importForm.selected);
    const errors = validateImportForm(importForm, selected);
    setImportErrors(errors);
    setImportError("");
    if (Object.keys(errors).length > 0 || !selected) return;
    setSavingImport(true);
    try {
      await onCreateOpenAPIImportedSkill({
        document: importForm.document,
        base_url: importForm.base_url.trim() || undefined,
        operation_id: selected.operation_id || undefined,
        method: selected.operation_id ? undefined : selected.method,
        path: selected.operation_id ? undefined : selected.path,
        bearer_token: selected.requires_secret ? importForm.bearer_token.trim() : undefined,
        bearer_token_secret_ref: selected.requires_secret
          ? importForm.bearer_token_secret_ref.trim()
          : undefined,
      });
      setImportForm(defaultImportForm);
      setImportCandidates([]);
      setImportErrors({});
      setImportOpen(false);
    } catch (error) {
      setImportError(errorMessage(error));
    } finally {
      setSavingImport(false);
    }
  }

  async function previewMCP(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const errors = validateMCPPreviewForm(mcpPreviewForm);
    setMCPPreviewErrors(errors);
    setMCPPreviewError("");
    setMCPCandidates([]);
    if (Object.keys(errors).length > 0) return;
    setPreviewingMCP(true);
    try {
      const response = await onPreviewMCPImport({
        document: mcpPreviewForm.document,
      });
      setMCPCandidates(response.candidates || []);
    } catch (error) {
      setMCPPreviewError(errorMessage(error));
    } finally {
      setPreviewingMCP(false);
    }
  }

  function updateForm<K extends keyof HTTPSkillInput>(field: K, value: HTTPSkillInput[K]) {
    setForm((prev) => ({ ...prev, [field]: value }));
    setServerError("");
    if (isFieldName(field)) {
      setFieldErrors((prev) => withoutFieldError(prev, field));
    }
  }

  function updateImportForm<K extends keyof ImportFormState>(field: K, value: ImportFormState[K]) {
    setImportForm((prev) => ({ ...prev, [field]: value }));
    setImportError("");
    setImportErrors((prev) => withoutImportFieldError(prev, field));
  }

  function updateMCPPreviewForm<K extends keyof MCPPreviewFormState>(
    field: K,
    value: MCPPreviewFormState[K],
  ) {
    setMCPPreviewForm((prev) => ({ ...prev, [field]: value }));
    setMCPPreviewError("");
    setMCPPreviewErrors((prev) => withoutMCPPreviewFieldError(prev, field));
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
            <>
              <label className={styles.field}>
                <span>Bearer Token</span>
                <input
                  aria-invalid={Boolean(fieldErrors.bearer_token)}
                  disabled={saving}
                  value={form.bearer_token}
                  onBlur={() =>
                    setFieldErrors((prev) => mergeFieldError(prev, form, "bearer_token"))
                  }
                  onChange={(event) => updateForm("bearer_token", event.target.value)}
                  placeholder="直接填 token，适合本地开发"
                  type="password"
                />
                {fieldErrors.bearer_token && <small>{fieldErrors.bearer_token}</small>}
              </label>
              <label className={styles.field}>
                <span>Secret Ref</span>
                <input
                  aria-invalid={Boolean(fieldErrors.bearer_token_secret_ref)}
                  disabled={saving}
                  value={form.bearer_token_secret_ref}
                  onBlur={() =>
                    setFieldErrors((prev) => mergeFieldError(prev, form, "bearer_token_secret_ref"))
                  }
                  onChange={(event) => updateForm("bearer_token_secret_ref", event.target.value)}
                  placeholder="env://TOKEN_NAME 或 file:///var/run/secrets/token"
                />
                {fieldErrors.bearer_token_secret_ref && (
                  <small>{fieldErrors.bearer_token_secret_ref}</small>
                )}
              </label>
              <p className={styles.hint}>
                Bearer Token 和 Secret Ref 二选一。生产环境优先使用 Secret Ref。
              </p>
            </>
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

      <div className={styles.sectionRow}>
        <SectionTitle text="OpenAPI 导入" />
        <button
          className={styles.miniButton}
          disabled={previewingImport || savingImport}
          onClick={() => setImportOpen((value) => !value)}
          type="button"
        >
          {importOpen ? "收起" : "导入"}
        </button>
      </div>

      {importOpen && (
        <form className={styles.form} noValidate onSubmit={previewImport}>
          <label className={styles.field}>
            <span>OpenAPI JSON/YAML</span>
            <textarea
              aria-invalid={Boolean(importErrors.document)}
              disabled={previewingImport || savingImport}
              value={importForm.document}
              onChange={(event) => updateImportForm("document", event.target.value)}
              placeholder="粘贴 OpenAPI 3.1 文档"
              rows={6}
            />
            {importErrors.document && <small>{importErrors.document}</small>}
          </label>
          <label className={styles.field}>
            <span>Base URL 覆盖</span>
            <input
              disabled={previewingImport || savingImport}
              value={importForm.base_url}
              onChange={(event) => updateImportForm("base_url", event.target.value)}
              placeholder="https://api.example.com"
            />
          </label>
          <button
            className={styles.secondaryButton}
            disabled={previewingImport || savingImport}
            type="submit"
          >
            {previewingImport ? "预览中..." : "预览可导入能力"}
          </button>
          {importCandidates.length > 0 && (
            <>
              <label className={styles.field}>
                <span>选择 Operation</span>
                <select
                  aria-invalid={Boolean(importErrors.selected)}
                  disabled={savingImport}
                  value={importForm.selected}
                  onChange={(event) => updateImportForm("selected", event.target.value)}
                >
                  {importCandidates.map((candidate) => (
                    <option key={candidateKey(candidate)} value={candidateKey(candidate)}>
                      {candidate.name} · {candidate.method} {candidate.path}
                    </option>
                  ))}
                </select>
                {importErrors.selected && <small>{importErrors.selected}</small>}
              </label>
              {selectedImportCandidate(importCandidates, importForm.selected)?.requires_secret && (
                <>
                  <label className={styles.field}>
                    <span>Bearer Token</span>
                    <input
                      aria-invalid={Boolean(importErrors.bearer_token)}
                      disabled={savingImport}
                      value={importForm.bearer_token}
                      onChange={(event) => updateImportForm("bearer_token", event.target.value)}
                      placeholder="直接填 token，适合本地开发"
                      type="password"
                    />
                    {importErrors.bearer_token && <small>{importErrors.bearer_token}</small>}
                  </label>
                  <label className={styles.field}>
                    <span>Secret Ref</span>
                    <input
                      aria-invalid={Boolean(importErrors.bearer_token_secret_ref)}
                      disabled={savingImport}
                      value={importForm.bearer_token_secret_ref}
                      onChange={(event) =>
                        updateImportForm("bearer_token_secret_ref", event.target.value)
                      }
                      placeholder="env://TOKEN_NAME 或 file:///var/run/secrets/token"
                    />
                    {importErrors.bearer_token_secret_ref && (
                      <small>{importErrors.bearer_token_secret_ref}</small>
                    )}
                  </label>
                  <p className={styles.hint}>
                    Bearer Token 和 Secret Ref 二选一。保存后不会在 API 响应中回显。
                  </p>
                </>
              )}
              <button
                className={styles.primaryButton}
                disabled={savingImport || previewingImport}
                onClick={() => void saveImport()}
                type="button"
              >
                {savingImport ? "导入中..." : "保存选中的 Skill"}
              </button>
            </>
          )}
          {importError && (
            <p className={styles.formError} role="alert">
              OpenAPI 导入失败：{importError}
            </p>
          )}
        </form>
      )}

      <div className={styles.sectionRow}>
        <SectionTitle text="MCP 预览" />
        <button
          className={styles.miniButton}
          disabled={previewingMCP}
          onClick={() => setMCPPreviewOpen((value) => !value)}
          type="button"
        >
          {mcpPreviewOpen ? "收起" : "预览"}
        </button>
      </div>

      {mcpPreviewOpen && (
        <form className={styles.form} noValidate onSubmit={previewMCP}>
          <label className={styles.field}>
            <span>MCP tools/list JSON</span>
            <textarea
              aria-invalid={Boolean(mcpPreviewErrors.document)}
              disabled={previewingMCP}
              value={mcpPreviewForm.document}
              onChange={(event) => updateMCPPreviewForm("document", event.target.value)}
              placeholder='粘贴 {"tools":[...]} 或 JSON-RPC {"result":{"tools":[...]}}'
              rows={6}
            />
            {mcpPreviewErrors.document && <small>{mcpPreviewErrors.document}</small>}
          </label>
          <button className={styles.secondaryButton} disabled={previewingMCP} type="submit">
            {previewingMCP ? "预览中..." : "预览 MCP 能力"}
          </button>
          <p className={styles.hint}>
            当前只做 manifest dry-run，不保存 Skill，也不会连接 MCP server 或保存 secret。
          </p>
          {mcpCandidates.length > 0 && (
            <div className={styles.previewList}>
              {mcpCandidates.map((candidate) => (
                <article className={styles.previewCard} key={candidate.name}>
                  <div className={styles.head}>
                    <strong>{candidate.name}</strong>
                    <span>{candidate.open_world ? "开放世界" : "受限上下文"}</span>
                  </div>
                  <p>{candidate.description || "暂无说明"}</p>
                  <div className={styles.tagRow}>
                    <span>{candidate.read_only ? "只读" : "可写"}</span>
                    {candidate.destructive && <span>破坏性</span>}
                    {candidate.idempotent && <span>幂等</span>}
                  </div>
                  <details className={styles.schemaDetails}>
                    <summary>查看 schema 摘要</summary>
                    <pre>{formatMCPSchemaPreview(candidate)}</pre>
                  </details>
                </article>
              ))}
            </div>
          )}
          {mcpPreviewError && (
            <p className={styles.formError} role="alert">
              MCP 预览失败：{mcpPreviewError}
            </p>
          )}
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
    ...validateField(form, "bearer_token_secret_ref"),
  };
}

function validateImportForm(
  form: ImportFormState,
  selected: HTTPSkillImportCandidate | null,
): ImportFieldErrors {
  const errors: ImportFieldErrors = {};
  if (!form.document.trim()) errors.document = "请粘贴 OpenAPI 文档";
  if (selected === null && form.selected) errors.selected = "请选择有效的 operation";
  if (selected?.unsupported_auth) errors.selected = "该 operation 的鉴权方式暂不支持";
  if (selected?.requires_secret && !hasBearerSecret(form)) {
    errors.bearer_token = "请输入 Bearer Token 或 Secret Ref";
  }
  if (form.bearer_token.trim() && form.bearer_token_secret_ref.trim()) {
    errors.bearer_token_secret_ref = "Bearer Token 和 Secret Ref 只能填写一个";
  }
  if (form.bearer_token_secret_ref.trim() && !isSupportedSecretRef(form.bearer_token_secret_ref)) {
    errors.bearer_token_secret_ref = "Secret Ref 需使用 env:// 或 file://";
  }
  return errors;
}

function validateMCPPreviewForm(form: MCPPreviewFormState): MCPPreviewFieldErrors {
  const errors: MCPPreviewFieldErrors = {};
  if (!form.document.trim()) errors.document = "请粘贴 MCP tools/list JSON";
  return errors;
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
        if (parsed.protocol !== "https:") {
          errors.url = "请求地址必须使用 https";
        } else if (parsed.username || parsed.password) {
          errors.url = "请求地址不能包含用户名或密码";
        }
      } catch {
        errors.url = "请输入有效的 URL";
      }
    }
  }
  if (
    form.auth_type === "bearer" &&
    (field === "bearer_token" || field === "bearer_token_secret_ref")
  ) {
    if (!form.bearer_token?.trim() && !form.bearer_token_secret_ref?.trim()) {
      errors.bearer_token = "请输入 Bearer Token 或 Secret Ref";
    }
    if (form.bearer_token?.trim() && form.bearer_token_secret_ref?.trim()) {
      errors.bearer_token_secret_ref = "Bearer Token 和 Secret Ref 只能填写一个";
    }
    if (
      form.bearer_token_secret_ref?.trim() &&
      !isSupportedSecretRef(form.bearer_token_secret_ref)
    ) {
      errors.bearer_token_secret_ref = "Secret Ref 需使用 env:// 或 file://";
    }
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
  return ["name", "description", "url", "bearer_token", "bearer_token_secret_ref"].includes(field);
}

function withoutImportFieldError(
  current: ImportFieldErrors,
  field: keyof ImportFormState,
): ImportFieldErrors {
  const next = { ...current };
  if (field === "document") delete next.document;
  if (field === "selected") delete next.selected;
  if (field === "bearer_token") delete next.bearer_token;
  if (field === "bearer_token_secret_ref") delete next.bearer_token_secret_ref;
  return next;
}

function withoutMCPPreviewFieldError(
  current: MCPPreviewFieldErrors,
  field: keyof MCPPreviewFormState,
): MCPPreviewFieldErrors {
  const next = { ...current };
  if (field === "document") delete next.document;
  return next;
}

function hasBearerSecret(form: Pick<ImportFormState, "bearer_token" | "bearer_token_secret_ref">) {
  return Boolean(form.bearer_token.trim() || form.bearer_token_secret_ref.trim());
}

function isSupportedSecretRef(value: string) {
  const secretRef = value.trim();
  return secretRef.startsWith("env://") || secretRef.startsWith("file://");
}

function candidateKey(candidate: HTTPSkillImportCandidate | undefined): string {
  if (!candidate) return "";
  return candidate.operation_id || `${candidate.method}:${candidate.path}`;
}

function selectedImportCandidate(
  candidates: HTTPSkillImportCandidate[],
  key: string,
): HTTPSkillImportCandidate | null {
  return candidates.find((candidate) => candidateKey(candidate) === key) || null;
}

function formatMCPSchemaPreview(candidate: MCPSkillImportCandidate): string {
  const summary = {
    input_schema: parseJSONSummary(candidate.input_schema),
    output_schema: parseJSONSummary(candidate.output_schema),
    annotations: parseJSONSummary(candidate.annotations),
  };
  return JSON.stringify(summary, null, 2);
}

function parseJSONSummary(value: string | undefined): unknown {
  if (!value) return null;
  try {
    return JSON.parse(value) as unknown;
  } catch {
    return value;
  }
}

function errorMessage(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}
