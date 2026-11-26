import { shallowRef } from "vue";

export interface SiteUser {
  id: string;
  username: string;
}
export const siteUser = shallowRef<SiteUser>();
export const sessionSignal = "site.session.changed";
export function notifySessionChange() {
  try {
    localStorage.setItem(sessionSignal, crypto.randomUUID());
  } catch {
    /* Storage is optional. */
  }
}
export function accountKey(key: string) {
  const id = siteUser.value?.id;
  return !id || id === "site-owner-v1" ? key : `site:${id}:${key}`;
}

export async function accountRequest(
  action: string,
  body?: object,
): Promise<SiteUser | undefined> {
  const response = await fetch(`/api/platform/auth/${action}`, {
    cache: "no-store",
    credentials: "same-origin",
    ...(body
      ? {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify(body),
        }
      : {}),
  });
  if (action === "me" && response.status === 401) return undefined;
  if (response.status === 204) return undefined;
  const result = await response.json().catch(() => null);
  if (!response.ok || !result)
    throw new Error(result?.message || "账户服务暂时不可用，请重试");
  return result;
}

const destinations = new Set([
  "/",
  "/liquor",
  "/ledger",
  "/tax",
  "/options",
  "/weibo/",
  "/account",
  "/ledger-prototype",
]);
export function afterLogin(search: string) {
  const target = new URLSearchParams(search).get("next") || "/";
  const path = target.split("?")[0]!;
  return destinations.has(path) ? target : "/";
}
