import {
  AccountAdminApiError,
  AccountAdminArgumentError,
  AccountAdminCancelledError,
  AccountAdminProtocolError,
  AccountAdminTokenProviderError,
  AccountAdminTransportError,
} from "./errors";
import { decode } from "./decode";
import type {
  AdminUser,
  AdminUserDetail,
  AdminUserList,
  AdminSessionList,
  RevealIdentity,
  RevokeAllResult,
  AuditEventList,
  FreezeRequest,
  UnfreezeRequest,
  ListUsersOptions,
  PageOptions,
  RequestOptions,
} from "./models";

export interface AccountAdminConfig {
  baseUrl: string;
  apiPrefix?: string;
  getAccessToken: () =>
    | string
    | null
    | undefined
    | Promise<string | null | undefined>;
  fetch?: typeof globalThis.fetch;
  requestId?: () => string;
}
function argument(): never {
  throw new AccountAdminArgumentError();
}
function safePath(value: string) {
  if (typeof value !== "string" || /[\\?#\u0000-\u0020]/.test(value))
    argument();
  for (const segment of value.split("/")) {
    let decoded: string;
    try {
      decoded = decodeURIComponent(segment);
    } catch {
      argument();
    }
    if (
      decoded === "." ||
      decoded === ".." ||
      /[\\/%?#\u0000-\u0020]/.test(decoded)
    )
      argument();
  }
}
function root(config: AccountAdminConfig) {
  if (
    !config ||
    typeof config.getAccessToken !== "function" ||
    typeof config.baseUrl !== "string"
  )
    argument();
  safePath(config.baseUrl.replace(/^https?:\/\//, ""));
  let url: URL;
  try {
    url = new URL(config.baseUrl);
  } catch {
    argument();
  }
  if (
    !["https:", "http:"].includes(url.protocol) ||
    url.username ||
    url.password ||
    url.search ||
    url.hash
  )
    argument();
  if (
    url.protocol === "http:" &&
    !["localhost", "127.0.0.1", "[::1]"].includes(url.hostname)
  )
    argument();
  const prefix = config.apiPrefix ?? "/admin/v1";
  safePath(prefix);
  if (
    !prefix.startsWith("/") ||
    prefix.startsWith("//") ||
    prefix.includes("//")
  )
    argument();
  return url.href.replace(/\/$/, "") + prefix.replace(/\/$/, "");
}
const alphabet = "[0-9a-hjkmnp-tv-z]{13}";
function user(value: string) {
  if (typeof value !== "string") argument();
  const id = value.startsWith("users/") ? value.slice(6) : value;
  if (!new RegExp(`^u_${alphabet}$`).test(id)) argument();
  return `users/${id}`;
}
function child(
  parent: string,
  value: string,
  collection: "sessions" | "identities",
) {
  const letter = collection === "sessions" ? "s" : "i";
  if (typeof value !== "string") argument();
  if (new RegExp(`^${letter}_${alphabet}$`).test(value))
    return `${parent}/${collection}/${value}`;
  if (new RegExp(`^${parent}/${collection}/${letter}_${alphabet}$`).test(value))
    return value;
  return argument();
}
function cancelled(signal?: AbortSignal) {
  if (signal?.aborted) throw new AccountAdminCancelledError();
}
async function abortable<T>(
  promise: Promise<T>,
  signal?: AbortSignal,
): Promise<T> {
  cancelled(signal);
  if (!signal) return promise;
  return new Promise<T>((resolve, reject) => {
    const abort = () => reject(new AccountAdminCancelledError());
    signal.addEventListener("abort", abort, { once: true });
    promise
      .then(resolve, reject)
      .finally(() => signal.removeEventListener("abort", abort));
  });
}
function query(options: ListUsersOptions | PageOptions) {
  const params = new URLSearchParams();
  for (const key of [
    "page_size",
    "page_token",
    "show_deleted",
    "filter",
  ] as const) {
    const value = (options as ListUsersOptions)[key];
    if (value === undefined) continue;
    if (
      key === "page_size"
        ? typeof value !== "number" || !Number.isSafeInteger(value) || value < 0
        : key === "show_deleted"
          ? typeof value !== "boolean"
          : typeof value !== "string"
    )
      argument();
    params.set(key, String(value));
  }
  return params.size ? `?${params}` : "";
}
function reason(body: FreezeRequest | UnfreezeRequest, required: boolean) {
  if (
    !body ||
    typeof body !== "object" ||
    Object.keys(body).some((k) => k !== "reason")
  )
    argument();
  if (body.reason !== undefined && typeof body.reason !== "string") argument();
  const text = body.reason?.trim() ?? "";
  if (
    (required && !text) ||
    [...text].length > 200 ||
    /[\u0000-\u001f\u007f-\u009f\u202a-\u202e\u2066-\u2069]/u.test(text)
  )
    argument();
  return body.reason === undefined ? {} : { reason: body.reason };
}
export function createAccountAdminClient(config: AccountAdminConfig) {
  const base = root(config);
  const transport = config.fetch ?? globalThis.fetch;
  if (typeof transport !== "function") argument();
  async function request<T>(
    method: string,
    path: string,
    decoder: ((v: unknown) => unknown) | null,
    options: RequestOptions = {},
    body?: unknown,
  ): Promise<T> {
    cancelled(options.signal);
    const requestId =
      options.requestId ??
      config.requestId?.() ??
      globalThis.crypto?.randomUUID?.() ??
      `${Date.now().toString(36)}-${Math.random().toString(36).slice(2)}`;
    if (
      requestId !== undefined &&
      (typeof requestId !== "string" || !requestId)
    )
      argument();
    const headers = new Headers({ Accept: "application/json" });
    try {
      headers.set("X-Request-Id", requestId);
    } catch {
      argument();
    }
    let token: unknown;
    try {
      token = await abortable(
        Promise.resolve().then(() => config.getAccessToken()),
        options.signal,
      );
    } catch (error) {
      if (error instanceof AccountAdminCancelledError) throw error;
      throw new AccountAdminTokenProviderError();
    }
    cancelled(options.signal);
    if (
      typeof token !== "string" ||
      !token.trim() ||
      /[\s\u0000-\u001f\u007f]/.test(token)
    )
      throw new AccountAdminTokenProviderError();
    try {
      headers.set("Authorization", `Bearer ${token}`);
    } catch {
      throw new AccountAdminTokenProviderError();
    }
    if (body !== undefined) headers.set("Content-Type", "application/json");
    let response: Response;
    let text: string;
    try {
      response = await abortable(
        transport(base + "/" + path, {
          method,
          headers,
          body: body === undefined ? undefined : JSON.stringify(body),
          signal: options.signal,
          cache: "no-store",
          redirect: "error",
        }),
        options.signal,
      );
      text = await abortable(response.text(), options.signal);
    } catch (error) {
      if (
        error instanceof AccountAdminCancelledError ||
        options.signal?.aborted
      )
        throw new AccountAdminCancelledError();
      throw new AccountAdminTransportError();
    }
    cancelled(options.signal);
    if (
      response.redirected ||
      (response.status >= 300 && response.status < 400)
    )
      throw new AccountAdminProtocolError();
    const responseId = response.headers.get("X-Request-Id") ?? requestId;
    let data: unknown;
    try {
      data = JSON.parse(text);
    } catch {
      data = undefined;
    }
    if (!response.ok) {
      let status = "UNKNOWN",
        message = "Account API request failed",
        apiReason: string | undefined,
        retryAfterSeconds: number | undefined;
      if (data && typeof data === "object" && "error" in data) {
        const error = data.error;
        if (
          error &&
          typeof error === "object" &&
          "code" in error &&
          error.code === response.status &&
          "status" in error &&
          typeof error.status === "string" &&
          "message" in error &&
          typeof error.message === "string"
        ) {
          status = error.status;
          message = error.message;
          if ("reason" in error && typeof error.reason === "string")
            apiReason = error.reason;
          if (
            "retry_after_seconds" in error &&
            typeof error.retry_after_seconds === "number" &&
            Number.isSafeInteger(error.retry_after_seconds) &&
            error.retry_after_seconds > 0
          )
            retryAfterSeconds = error.retry_after_seconds;
        }
      }
      throw new AccountAdminApiError({
        httpStatus: response.status,
        status,
        reason: apiReason,
        message,
        retryAfterSeconds,
        requestId: responseId,
      });
    }
    if (decoder === null) {
      if (response.status !== 204 || text !== "")
        throw new AccountAdminProtocolError();
      return undefined as T;
    }
    if (response.status !== 200) throw new AccountAdminProtocolError();
    const result = decoder(data) as Record<string, unknown>;
    const resource = path.split("?")[0].split(":")[0];
    if (resource !== "users") {
      if ("name" in result && result.name !== resource)
        throw new AccountAdminProtocolError();
      for (const collection of ["identities", "sessions", "audit_events"]) {
        const items = result[collection];
        if (Array.isArray(items)) {
          const prefix =
            collection === "identities"
              ? resource + "/identities/"
              : resource + "/";
          if (
            items.some(
              (item) =>
                typeof item.name !== "string" || !item.name.startsWith(prefix),
            )
          )
            throw new AccountAdminProtocolError();
        }
      }
    }
    return result as T;
  }
  return {
    listUsers: (params: ListUsersOptions = {}, options?: RequestOptions) =>
      request<AdminUserList>(
        "GET",
        "users" + query(params),
        decode.users,
        options,
      ),
    getUser: async (value: string, options?: RequestOptions) =>
      request<AdminUserDetail>("GET", user(value), decode.detail, options),
    deleteUser: async (value: string, options?: RequestOptions) =>
      request<AdminUser>("DELETE", user(value), decode.user, options),
    freezeUser: async (
      value: string,
      body: FreezeRequest,
      options?: RequestOptions,
    ) =>
      request<AdminUser>(
        "POST",
        user(value) + ":freeze",
        decode.user,
        options,
        reason(body, true),
      ),
    unfreezeUser: async (
      value: string,
      body: UnfreezeRequest = {},
      options?: RequestOptions,
    ) =>
      request<AdminUser>(
        "POST",
        user(value) + ":unfreeze",
        decode.user,
        options,
        reason(body, false),
      ),
    undeleteUser: async (value: string, options?: RequestOptions) =>
      request<AdminUser>(
        "POST",
        user(value) + ":undelete",
        decode.user,
        options,
      ),
    revealIdentity: async (
      value: string,
      identity: string,
      options?: RequestOptions,
    ) =>
      request<RevealIdentity>(
        "GET",
        child(user(value), identity, "identities") + ":reveal",
        decode.reveal,
        options,
      ),
    listSessions: async (value: string, options?: RequestOptions) =>
      request<AdminSessionList>(
        "GET",
        user(value) + "/sessions",
        decode.sessions,
        options,
      ),
    revokeSession: async (
      value: string,
      session: string,
      options?: RequestOptions,
    ) =>
      request<void>(
        "DELETE",
        child(user(value), session, "sessions"),
        null,
        options,
      ),
    revokeAllSessions: async (value: string, options?: RequestOptions) =>
      request<RevokeAllResult>(
        "POST",
        user(value) + "/sessions:revokeAll",
        decode.revokeAll,
        options,
      ),
    listAuditEvents: async (
      value: string,
      params: PageOptions = {},
      options?: RequestOptions,
    ) =>
      request<AuditEventList>(
        "GET",
        user(value) + "/auditEvents" + query(params),
        decode.audit,
        options,
      ),
  };
}
export type AccountAdminClient = ReturnType<typeof createAccountAdminClient>;
