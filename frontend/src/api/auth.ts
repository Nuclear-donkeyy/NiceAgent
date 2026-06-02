import { api } from "./client";

interface AuthActionResponse {
  status: string;
}

export function startOIDCLogin() {
  window.location.assign("/auth/oidc/login");
}

export async function refreshOIDCSession(): Promise<AuthActionResponse> {
  return api<AuthActionResponse>("/auth/oidc/refresh", { method: "POST" });
}

export async function logout(): Promise<AuthActionResponse> {
  return api<AuthActionResponse>("/auth/logout", { method: "POST" });
}
