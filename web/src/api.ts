export type Messages = Record<string, string>;

export type Session = {
  authenticated: boolean;
  email: string;
  needsSetup: boolean;
  lang: string;
  baseURL: string;
  messages: Messages;
};

export type Site = {
  slug: string;
  url: string;
  host: string;
  files: number;
  count: string;
};

export type SiteFile = {
  path: string;
  size: number;
};

export type SiteDetail = {
  site: Site;
  files: SiteFile[];
  curl: string;
};

export class ApiError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "ApiError";
  }
}

export async function api<T>(path: string, init?: RequestInit): Promise<T> {
  const headers = new Headers(init?.headers);
  if (init?.body && !(init.body instanceof FormData) && !headers.has("Content-Type")) {
    headers.set("Content-Type", "application/json");
  }
  const res = await fetch(path, { credentials: "same-origin", ...init, headers });
  const text = await res.text();
  const data = text ? (JSON.parse(text) as T & { error?: string }) : ({} as T & { error?: string });
  if (!res.ok) {
    throw new ApiError(data.error || "err_internal");
  }
  return data;
}
