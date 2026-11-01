import { describe, expect, it } from "vitest";
import * as sdk from "../src/index";

const uid = "u_0000000000001",
  sid = "s_0000000000001",
  iid = "i_0000000000001";
const name = `users/${uid}`;
const time = "2026-11-01T00:00:00Z";
const user = {
  name,
  state: "FUTURE_STATE",
  display_name: "",
  avatar_url: "",
  create_time: time,
  update_time: time,
  delete_time: null,
  purge_time: null,
  freeze: null,
};
const detail = { ...user, identities: [], active_session_count: 0 };
const create = (fetch: typeof globalThis.fetch, extra = {}) =>
  sdk.createAccountAdminClient({
    baseUrl: "https://account.example/proxy",
    getAccessToken: () => "secret",
    fetch,
    requestId: () => "request-1",
    ...extra,
  });

describe("official admin operations", () => {
  const cases = [
    [
      "adminListUsers",
      "listUsers",
      [],
      "GET",
      "/users",
      { users: [user], next_page_token: "" },
    ],
    ["adminGetUser", "getUser", [uid], "GET", `/${name}`, detail],
    ["adminDeleteUser", "deleteUser", [uid], "DELETE", `/${name}`, user],
    [
      "adminFreezeUser",
      "freezeUser",
      [uid, { reason: "review" }],
      "POST",
      `/${name}:freeze`,
      user,
    ],
    [
      "adminUnfreezeUser",
      "unfreezeUser",
      [uid, {}],
      "POST",
      `/${name}:unfreeze`,
      user,
    ],
    [
      "adminUndeleteUser",
      "undeleteUser",
      [uid],
      "POST",
      `/${name}:undelete`,
      user,
    ],
    [
      "adminRevealIdentity",
      "revealIdentity",
      [uid, iid],
      "GET",
      `/${name}/identities/${iid}:reveal`,
      { name: `${name}/identities/${iid}`, kind: "CUSTOM", subject: "private" },
    ],
    [
      "adminListSessions",
      "listSessions",
      [uid],
      "GET",
      `/${name}/sessions`,
      { sessions: [] },
    ],
    [
      "adminRevokeSession",
      "revokeSession",
      [uid, sid],
      "DELETE",
      `/${name}/sessions/${sid}`,
      undefined,
    ],
    [
      "adminRevokeAllSessions",
      "revokeAllSessions",
      [uid],
      "POST",
      `/${name}/sessions:revokeAll`,
      { revoked_count: 2 },
    ],
    [
      "adminListAuditEvents",
      "listAuditEvents",
      [uid],
      "GET",
      `/${name}/auditEvents`,
      { audit_events: [], next_page_token: "" },
    ],
  ] as const;
  for (const [id, method, args, verb, path, value] of cases)
    it(id, async () => {
      let count = 0;
      const client = create(async (url, init) => {
        count++;
        expect(String(url)).toBe(
          `https://account.example/proxy/admin/v1${path}`,
        );
        expect(init?.method).toBe(verb);
        expect(init?.cache).toBe("no-store");
        expect(init?.redirect).toBe("error");
        expect(new Headers(init?.headers).get("Authorization")).toBe(
          "Bearer secret",
        );
        expect(new Headers(init?.headers).get("X-Request-Id")).toBe(
          "request-1",
        );
        if (method === "freezeUser")
          expect(JSON.parse(String(init?.body))).toEqual({ reason: "review" });
        return value === undefined
          ? new Response(null, { status: 204 })
          : Response.json(value);
      });
      const result = await (
        client[method] as (...args: unknown[]) => Promise<unknown>
      )(...args);
      expect(result).toEqual(value);
      expect(count).toBe(1);
    });
});

it("provider failure and empty token never send HTTP", async () => {
  let count = 0;
  const fetch = async () => {
    count++;
    return Response.json(detail);
  };
  for (const getAccessToken of [
    () => {
      throw new Error("secret provider");
    },
    () => "",
  ]) {
    await expect(
      create(fetch, { getAccessToken }).getUser(uid),
    ).rejects.toBeInstanceOf(sdk.AccountAdminTokenProviderError);
  }
  expect(count).toBe(0);
});
it("cancellation during provider waits prevents late HTTP", async () => {
  let resolve!: (value: string) => void;
  let count = 0;
  const abort = new AbortController();
  const client = create(
    async () => {
      count++;
      return Response.json(detail);
    },
    { getAccessToken: () => new Promise<string>((r) => (resolve = r)) },
  );
  const pending = client.getUser(uid, { signal: abort.signal });
  abort.abort();
  await expect(pending).rejects.toBeInstanceOf(sdk.AccountAdminCancelledError);
  resolve("late");
  await Promise.resolve();
  expect(count).toBe(0);
});
it("non-ByteString provider token is a safe provider error before HTTP", async () => {
  let calls = 0;
  const privateToken = "private-token-" + String.fromCharCode(256);
  const client = create(
    async () => {
      calls++;
      return Response.json(detail);
    },
    { getAccessToken: () => privateToken },
  );
  const error = await client.getUser(uid).catch((value: unknown) => value);
  expect(error).toBeInstanceOf(sdk.AccountAdminTokenProviderError);
  expect(String(error)).not.toContain(privateToken);
  if (error instanceof Error) {
    expect(error.message).toBe("Access token provider failed");
    expect(error.stack).not.toContain(privateToken);
  }
  expect(calls).toBe(0);
});

it("each request obtains current provider token", async () => {
  let calls = 0;
  const observed: string[] = [];
  const client = create(
    async (_url, init) => {
      observed.push(new Headers(init?.headers).get("Authorization")!);
      return Response.json(detail);
    },
    { getAccessToken: () => `token-${++calls}` },
  );
  await Promise.all([client.getUser(uid), client.getUser(uid)]);
  expect(observed).toEqual(["Bearer token-1", "Bearer token-2"]);
});
it("invalid resources and mismatched parents fail locally", async () => {
  let count = 0;
  const client = create(async () => {
    count++;
    return Response.json(detail);
  });
  for (const value of [
    "..",
    "%2e%2e",
    "https://evil.test/a",
    `${name}/../${uid}`,
    `${uid}%2fother`,
  ])
    await expect(client.getUser(value)).rejects.toBeInstanceOf(
      sdk.AccountAdminArgumentError,
    );
  await expect(
    client.revokeSession(uid, `users/u_0000000000002/sessions/${sid}`),
  ).rejects.toBeInstanceOf(sdk.AccountAdminArgumentError);
  expect(count).toBe(0);
});
it("unsafe base paths are rejected before URL normalization", () => {
  for (const baseUrl of [
    "https://a/../x",
    "https://a/%2e%2e/x",
    "https://a/%2f/x",
    "https://a/?x=1",
    "https://user:password@a",
  ])
    expect(() => createAccount(baseUrl)).toThrow(sdk.AccountAdminArgumentError);
  function createAccount(baseUrl: string) {
    return create(globalThis.fetch, { baseUrl });
  }
});
it("AIP errors preserve status, reason, wait and response request ID without retry", async () => {
  let count = 0;
  const client = create(async () => {
    count++;
    return Response.json(
      {
        error: {
          code: 503,
          status: "UNAVAILABLE",
          reason: "FUTURE_REASON",
          message: "safe",
          retry_after_seconds: 7,
        },
      },
      { status: 503, headers: { "X-Request-Id": "response-id" } },
    );
  });
  await expect(client.getUser(uid)).rejects.toMatchObject({
    httpStatus: 503,
    status: "UNAVAILABLE",
    reason: "FUTURE_REASON",
    serverMessage: "safe",
    retryAfterSeconds: 7,
    requestId: "response-id",
  });
  expect(count).toBe(1);
});
it("HTML failure retains HTTP status and never prints body", async () => {
  const client = create(
    async () => new Response("private HTML", { status: 502 }),
  );
  try {
    await client.getUser(uid);
  } catch (e) {
    expect(e).toBeInstanceOf(sdk.AccountAdminApiError);
    expect(e).toMatchObject({ httpStatus: 502 });
    expect(String(e)).not.toContain("private");
  }
});
it("malformed success, empty success and unexpected 204 are protocol errors", async () => {
  for (const response of [
    Response.json({}),
    new Response(null, { status: 204 }),
    new Response(""),
  ])
    await expect(
      create(async () => response).getUser(uid),
    ).rejects.toBeInstanceOf(sdk.AccountAdminProtocolError);
});
it("network failures are safe and writes are sent once", async () => {
  let count = 0;
  const client = create(async () => {
    count++;
    throw new Error("secret");
  });
  await expect(
    client.freezeUser(uid, { reason: "review" }),
  ).rejects.toBeInstanceOf(sdk.AccountAdminTransportError);
  expect(count).toBe(1);
});
it("pagination passes opaque cursor and filter unmodified", async () => {
  const client = create(async (url) => {
    const q = new URL(String(url)).searchParams;
    expect(q.get("page_token")).toBe("opaque +/=");
    expect(q.get("show_deleted")).toBe("true");
    expect(q.get("filter")).toBe('state = "ACTIVE"');
    return Response.json({ users: [], next_page_token: "next" });
  });
  expect(
    await client.listUsers({
      page_token: "opaque +/=",
      show_deleted: true,
      filter: 'state = "ACTIVE"',
      page_size: 2,
    }),
  ).toEqual({ users: [], next_page_token: "next" });
});
it("filter builder supports official fields and AND without injection", () => {
  expect(
    sdk.userFilter.and(
      sdk.userFilter.eq("state", "ACTIVE"),
      sdk.userFilter.compare("create_time", ">=", time),
    ),
  ).toBe(`state = "ACTIVE" AND create_time >= "${time}"`);
  expect(() =>
    sdk.userFilter.eq("identity.email", 'a" AND state = ACTIVE'),
  ).toThrow(sdk.AccountAdminArgumentError);
});
it("page size zero and values above server cap retain formal server semantics", async () => {
  const sizes: number[] = [];
  const client = create(async (url) => {
    sizes.push(Number(new URL(String(url)).searchParams.get("page_size")));
    return Response.json({ users: [], next_page_token: "" });
  });
  await client.listUsers({ page_size: 0 });
  await client.listUsers({ page_size: 101 });
  expect(sizes).toEqual([0, 101]);
});
it("nullable audit fields retain null while omitted flow_id stays omitted", async () => {
  const event = {
    name: `${name}/auditEvents/019a3c30-0000-7000-8000-000000000001`,
    event_type: "FUTURE_EVENT",
    actor_kind: "SYSTEM",
    result: null,
    reason: null,
    session_id: null,
    identity_kind: null,
    subject_hint: null,
    ip: null,
    device_id: null,
    request_id: null,
    admin_issuer: null,
    admin_subject: null,
    admin_username: null,
    occur_time: time,
  };
  const result = await create(async () =>
    Response.json({
      audit_events: [{ ...event, extra: "ignored" }],
      next_page_token: "",
    }),
  ).listAuditEvents(uid);
  expect(result.audit_events[0]).toEqual(event);
  expect(result.audit_events[0]).not.toHaveProperty("flow_id");
  await expect(
    create(async () =>
      Response.json({
        audit_events: [{ ...event, result: undefined }],
        next_page_token: "",
      }),
    ).listAuditEvents(uid),
  ).rejects.toBeInstanceOf(sdk.AccountAdminProtocolError);
});
it("nested client metadata is decoded with optional fields and unknown enums", async () => {
  const session = {
    name: `${name}/sessions/${sid}`,
    device_id: "device",
    device_name: "",
    create_time: time,
    last_used_time: time,
    client_info: {
      type: "FUTURE_TYPE",
      os: { version: "1" },
      app: { build: "2" },
      browser: { name: "browser" },
      device: { manufacturer: "vendor" },
      extensions: { custom: ["ok"] },
    },
    client_info_update_time: time,
  };
  expect(
    await create(async () =>
      Response.json({ sessions: [session] }),
    ).listSessions(uid),
  ).toEqual({ sessions: [session] });
  await expect(
    create(async () =>
      Response.json({
        sessions: [{ ...session, client_info: { app: { build: 12 } } }],
      }),
    ).listSessions(uid),
  ).rejects.toBeInstanceOf(sdk.AccountAdminProtocolError);
});
it.each([
  "name",
  "state",
  "display_name",
  "avatar_url",
  "create_time",
  "update_time",
  "delete_time",
  "purge_time",
  "freeze",
])("required user field %s cannot be omitted", async (field) => {
  const value: Record<string, unknown> = { ...detail };
  delete value[field];
  await expect(
    create(async () => Response.json(value)).getUser(uid),
  ).rejects.toBeInstanceOf(sdk.AccountAdminProtocolError);
});
it.each([401, 403, 429, 503])(
  "HTTP %i is returned without retries",
  async (status) => {
    let count = 0;
    const client = create(async () => {
      count++;
      return Response.json(
        {
          error: {
            code: status,
            status: "FUTURE_STATUS",
            reason: "FUTURE_REASON",
            message: "safe",
          },
        },
        { status },
      );
    });
    await expect(client.deleteUser(uid)).rejects.toMatchObject({
      httpStatus: status,
      reason: "FUTURE_REASON",
    });
    expect(count).toBe(1);
  },
);
it("response body is consumed once and request ID option overrides generator", async () => {
  const response = Response.json(detail);
  let reads = 0;
  const text = response.text.bind(response);
  response.text = () => {
    reads++;
    return text();
  };
  const client = create(async (_url, init) => {
    expect(new Headers(init?.headers).get("X-Request-Id")).toBe("explicit");
    return response;
  });
  await client.getUser(name, { requestId: "explicit" });
  expect(reads).toBe(1);
});
it("abort after send reaches fetch signal and never retries", async () => {
  const abort = new AbortController();
  let count = 0;
  const client = create(async (_url, init) => {
    count++;
    expect(init?.signal).toBe(abort.signal);
    return new Promise<Response>((_resolve, reject) =>
      init?.signal?.addEventListener("abort", () =>
        reject(new DOMException("abort", "AbortError")),
      ),
    );
  });
  const pending = client.getUser(uid, { signal: abort.signal });
  await new Promise((resolve) => setTimeout(resolve, 0));
  abort.abort();
  await expect(pending).rejects.toBeInstanceOf(sdk.AccountAdminCancelledError);
  expect(count).toBe(1);
});
it("pre-cancelled requests skip provider and fetch", async () => {
  const abort = new AbortController();
  abort.abort();
  let count = 0;
  const client = create(
    async () => {
      count++;
      return Response.json(detail);
    },
    {
      getAccessToken: () => {
        count++;
        return "token";
      },
    },
  );
  await expect(
    client.getUser(uid, { signal: abort.signal }),
  ).rejects.toBeInstanceOf(sdk.AccountAdminCancelledError);
  expect(count).toBe(0);
});
it("all filter fields allow parser-safe special characters", () => {
  for (const field of [
    "identity.phone",
    "identity.phone_prefix",
    "identity.phone_suffix",
    "identity.email",
    "identity.email_prefix",
    "identity.email_domain",
  ] as const)
    expect(sdk.userFilter.eq(field, "a+b\\c@d.example")).toBe(
      `${field} = "a+b\\c@d.example"`,
    );
});
it("response resource names must belong to requested user and nested collection", async () => {
  await expect(
    create(async () =>
      Response.json({ ...detail, name: "users/u_0000000000002" }),
    ).getUser(uid),
  ).rejects.toBeInstanceOf(sdk.AccountAdminProtocolError);
  await expect(
    create(async () =>
      Response.json({
        ...detail,
        identities: [
          {
            name: `users/u_0000000000002/identities/${iid}`,
            kind: "EMAIL",
            masked_subject: "masked",
            create_time: time,
          },
        ],
      }),
    ).getUser(uid),
  ).rejects.toBeInstanceOf(sdk.AccountAdminProtocolError);
  await expect(
    create(async () =>
      Response.json({
        name: `users/u_0000000000002/identities/${iid}`,
        kind: "EMAIL",
        subject: "private",
      }),
    ).revealIdentity(uid, iid),
  ).rejects.toBeInstanceOf(sdk.AccountAdminProtocolError);
});
it("default request ID is generated once and retained on HTTP error", async () => {
  let sent: string | null = null;
  const client = create(
    async (_url, init) => {
      sent = new Headers(init?.headers).get("X-Request-Id");
      return new Response(null, { status: 503 });
    },
    { requestId: undefined },
  );
  await expect(client.getUser(uid)).rejects.toMatchObject({
    requestId: expect.any(String),
  });
  expect(sent).toBeTruthy();
});
it("custom prefix is appended once after reverse proxy path", async () => {
  const client = create(
    async (url) => {
      expect(String(url)).toBe(
        `https://account.example/proxy/custom/admin/${name}`,
      );
      return Response.json(detail);
    },
    { apiPrefix: "/custom/admin/" },
  );
  await client.getUser(uid);
});
it("INVALID_PAGE_TOKEN never triggers hidden first-page recovery", async () => {
  let count = 0;
  const client = create(async () => {
    count++;
    return Response.json(
      {
        error: {
          code: 400,
          status: "INVALID_ARGUMENT",
          reason: "INVALID_PAGE_TOKEN",
          message: "restart explicitly",
        },
      },
      { status: 400 },
    );
  });
  await expect(
    client.listAuditEvents(uid, { page_token: "old cursor" }),
  ).rejects.toMatchObject({ reason: "INVALID_PAGE_TOKEN" });
  expect(count).toBe(1);
});
it("429 wait seconds remain available without retrying", async () => {
  let count = 0;
  const client = create(async () => {
    count++;
    return Response.json(
      {
        error: {
          code: 429,
          status: "RESOURCE_EXHAUSTED",
          reason: "RATE_LIMITED",
          message: "wait",
          retry_after_seconds: 13,
        },
      },
      { status: 429 },
    );
  });
  await expect(client.listUsers()).rejects.toMatchObject({
    httpStatus: 429,
    retryAfterSeconds: 13,
  });
  expect(count).toBe(1);
});
it("API errors preserve explicit server message while default stack and inspection stay safe", async () => {
  const privateMessage = "private-person@example.test";
  const client = create(async () =>
    Response.json(
      {
        error: {
          code: 403,
          status: "PERMISSION_DENIED",
          reason: "FORBIDDEN",
          message: privateMessage,
        },
      },
      { status: 403 },
    ),
  );
  const error = await client
    .listUsers()
    .catch((e) => e as sdk.AccountAdminApiError);
  if (!(error instanceof sdk.AccountAdminApiError))
    throw new Error("Expected an API error");
  expect(error.serverMessage).toBe(privateMessage);
  expect(error.message).not.toContain(privateMessage);
  expect(error.stack).not.toContain(privateMessage);
  expect(String(error)).not.toContain(privateMessage);
  expect(JSON.stringify(error)).not.toContain(privateMessage);
  // Node inspection is what console.log/error use, independently of toString.
  const inspect = Symbol.for("nodejs.util.inspect.custom");
  expect(inspect in error).toBe(true);
  expect(
    String((error as unknown as Record<symbol, () => unknown>)[inspect]()),
  ).not.toContain(privateMessage);
  const nodeUtil = await import(/* @vite-ignore */ "node:" + "util");
  expect(nodeUtil.inspect(error)).not.toContain(privateMessage);
});
it.each(["bad\u0000id", "bad\rid", "bad\nid", "bad\u0100id"])(
  "invalid request ID %j is a local error before provider",
  async (requestId) => {
    let calls = 0;
    const client = create(
      async () => {
        calls++;
        return Response.json(detail);
      },
      {
        getAccessToken: () => {
          calls++;
          return "token";
        },
      },
    );
    await expect(client.listUsers({}, { requestId })).rejects.toBeInstanceOf(
      sdk.AccountAdminArgumentError,
    );
    expect(calls).toBe(0);
    const generated = create(
      async () => {
        calls++;
        return Response.json(detail);
      },
      {
        requestId: () => requestId,
        getAccessToken: () => {
          calls++;
          return "token";
        },
      },
    );
    await expect(generated.listUsers()).rejects.toBeInstanceOf(
      sdk.AccountAdminArgumentError,
    );
    expect(calls).toBe(0);
  },
);
it.each(["2026-11-01T00:00:00", "2026-11-01T00:00:00.1"])(
  "typed filter rejects timestamp without timezone: %s",
  (value) => {
    expect(() => sdk.userFilter.compare("create_time", ">=", value)).toThrow(
      sdk.AccountAdminArgumentError,
    );
  },
);
