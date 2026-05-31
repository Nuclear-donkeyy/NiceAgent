import { api } from "./client";
import type { HTTPSkillInput, Skill, SkillGroups, SkillsResponse } from "../domain/skill";

export async function listSkills(): Promise<SkillGroups> {
  const data = await api<SkillsResponse>("/api/skills");
  const skills = data.skills || [];
  const groups = data.groups || {};
  return {
    system: groups.system || skills.filter((skill) => skill.scope !== "user"),
    user: groups.user || skills.filter((skill) => skill.scope === "user"),
  };
}

export function createHTTPSkill(input: HTTPSkillInput): Promise<Skill> {
  return api<Skill>("/api/skills/http", {
    method: "POST",
    body: JSON.stringify(input),
  });
}

export function setSkillEnabled(skillID: string, enabled: boolean): Promise<Skill> {
  return api<Skill>(`/api/skills/${encodeURIComponent(skillID)}/${enabled ? "enable" : "disable"}`, {
    method: "POST",
  });
}
