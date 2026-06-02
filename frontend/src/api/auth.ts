import { api } from "./client";

interface AuthActionResponse {
  status: string;
}

export function startOIDCLogin() {
  window.location.assign("/auth/oidc/login");
}

export async function refreshOIDCSession(): Promise<AuthActionResponse> {
  return api<AuthActionResponse>("/auth/oidc/refresh", oidcPostOptions());
}

export async function logout(): Promise<AuthActionResponse> {
  return api<AuthActionResponse>("/auth/logout", oidcPostOptions());
}

function oidcPostOptions(): RequestInit {
  const csrfToken = readCookie("niceagent_csrf");
  return {
    method: "POST",
    headers: csrfToken ? { "X-NiceAgent-CSRF": csrfToken } : {},
  };
}

function readCookie(name: string): string {
  const prefix = `${encodeURIComponent(name)}=`;
  return (
    document.cookie
      .split(";")
      .map((part) => part.trim())
      .find((part) => part.startsWith(prefix))
      ?.slice(prefix.length) ?? ""
  );
}
