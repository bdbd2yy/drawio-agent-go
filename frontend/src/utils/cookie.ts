export const COOKIE_NAME = "ai_agent_login";
export const COOKIE_DAYS = 7;

export interface UserInfo {
  user: string;
  ts: number;
}

export const setCookie = (name: string, value: string, days: number) => {
  const maxAge = Math.max(0, Math.floor(days * 86400));
  document.cookie = `${name}=${encodeURIComponent(value)}; Max-Age=${maxAge}; Path=/; SameSite=Lax`;
};

export const getCookie = (name: string): string | null => {
  const cookies = document.cookie ? document.cookie.split("; ") : [];
  for (const item of cookies) {
    const eqIndex = item.indexOf("=");
    const k = eqIndex >= 0 ? item.slice(0, eqIndex) : item;
    const v = eqIndex >= 0 ? item.slice(eqIndex + 1) : "";
    if (k === name) return decodeURIComponent(v);
  }
  return null;
};

export const deleteCookie = (name: string) => {
  document.cookie = `${name}=; Max-Age=0; Path=/; SameSite=Lax`;
};

export const getUserInfo = (): UserInfo | null => {
  const raw = getCookie(COOKIE_NAME);
  if (!raw) return null;
  try {
    return JSON.parse(raw);
  } catch {
    return null;
  }
};

export const setUserInfo = (user: string) => {
  const payload: UserInfo = { user, ts: Date.now() };
  setCookie(COOKIE_NAME, JSON.stringify(payload), COOKIE_DAYS);
};

export const clearUserInfo = () => {
  deleteCookie(COOKIE_NAME);
};
