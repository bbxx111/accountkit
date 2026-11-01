import { expect, it } from "vitest";
import {
  createAccountAdminClient,
  AccountAdminApiError,
  userFilter,
} from "../../src/index";
import type { AccountAdminClient, AuditEvent } from "../../src/index";

const env = (
  globalThis as unknown as {
    process: { env: Record<string, string | undefined> };
  }
).process.env;
const origin = env.ACCOUNTKIT_ADMIN_INTEGRATION_ORIGIN;
const control = env.ACCOUNTKIT_ADMIN_INTEGRATION_CONTROL_ORIGIN;
let admin: AccountAdminClient, operator: AccountAdminClient;
const run =
  Date.now().toString(36) + "-" + Math.random().toString(36).slice(2, 8);
const target = `admin-sdk-${run}@example.test`;

async function json(
  url: string,
  init?: RequestInit,
): Promise<Record<string, unknown>> {
  const response = await fetch(url, {
    ...init,
    redirect: "error",
    signal: AbortSignal.timeout(10000),
  });
  if (!response.ok) throw new Error(`Fixture HTTP ${response.status}`);
  const value: unknown = await response.json();
  if (!value || typeof value !== "object" || Array.isArray(value))
    throw new Error("Invalid fixture response");
  return value as Record<string, unknown>;
}
async function token(role: string) {
  const value = await json(`${control}/__fixture/admin-token?role=${role}`);
  if (typeof value.access_token !== "string")
    throw new Error("Fixture token missing");
  return value.access_token;
}
async function seed(email: string, device: string) {
  const challenge = await json(`${origin}/v1/users:sendSignInCode`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ channel: "EMAIL", target: email }),
  });
  const captured = await json(
    `${control}/__fixture/code?target=${encodeURIComponent(email)}&purpose=SIGN_IN`,
  );
  if (
    typeof challenge.code_id !== "string" ||
    typeof captured.code !== "string"
  )
    throw new Error("Fixture sign-in challenge missing");
  const result = await json(`${origin}/v1/users:signInWithCode`, {
    method: "POST",
    headers: { "Content-Type": "application/json", "X-Device-Id": device },
    body: JSON.stringify({
      kind: "EMAIL",
      credential: {
        target: email,
        code_id: challenge.code_id,
        code: captured.code,
      },
    }),
  });
  if (
    typeof result.user_id !== "string" ||
    typeof result.access_token !== "string"
  )
    throw new Error("Fixture seed sign-in failed");
  return result.user_id;
}
async function setup() {
  if (!origin || !control)
    throw new Error(
      "ACCOUNTKIT_ADMIN_INTEGRATION_ORIGIN and ACCOUNTKIT_ADMIN_INTEGRATION_CONTROL_ORIGIN are required; integration cannot skip",
    );
  const ready = await json(`${control}/__fixture/ready`);
  expect(ready.mode).toBe("accountsvc-development");
  expect(ready.origin).toBe(origin);
  admin = createAccountAdminClient({
    baseUrl: origin,
    getAccessToken: () => token("super-admin"),
  });
  operator = createAccountAdminClient({
    baseUrl: origin,
    getAccessToken: () => token("operator"),
  });
}

it("all 11 public admin SDK operations against real accountsvc", async () => {
  await setup();
  const uid = await seed(target, `admin-sdk-${run}-a`);
  const otherTarget = `admin-sdk-${run}-other@example.test`;
  await seed(otherTarget, `admin-sdk-${run}-b`);
  const name = `users/${uid}`;

  // adminListUsers: raw filter, typed filters and genuine opaque continuation.
  const page = await admin.listUsers({
    filter: userFilter.eq("state", "ACTIVE"),
    page_size: 1,
  });
  expect(page.users).toHaveLength(1);
  expect(page.next_page_token).not.toBe("");
  const next = await admin.listUsers({
    filter: 'state = "ACTIVE"',
    page_size: 1,
    page_token: page.next_page_token,
  });
  expect(next.users).toHaveLength(1);
  expect(next.users[0].name).not.toBe(page.users[0].name);
  const matching = await operator.listUsers({
    filter: userFilter.eq("identity.email", target),
    show_deleted: false,
  });
  expect(matching.users.map((u) => u.name)).toEqual([name]);

  // adminGetUser, adminRevealIdentity; actual OIDC verifier enforces both roles.
  const detail = await operator.getUser(uid);
  expect(detail.state).toBe("ACTIVE");
  expect(detail.delete_time).toBeNull();
  expect(detail.purge_time).toBeNull();
  expect(detail.freeze).toBeNull();
  expect(detail.identities).toHaveLength(1);
  const identity = detail.identities[0];
  expect(identity.masked_subject).not.toBe(target);
  await expect(
    operator.revealIdentity(uid, identity.name),
  ).rejects.toMatchObject({ httpStatus: 403 });
  const revealed = await admin.revealIdentity(uid, identity.name);
  expect(revealed.subject).toBe(target);
  const invalid = createAccountAdminClient({
    baseUrl: origin!,
    getAccessToken: () => "invalid-test-token",
  });
  await expect(invalid.getUser(uid)).rejects.toMatchObject({ httpStatus: 401 });

  // adminListSessions, adminRevokeSession, adminRevokeAllSessions.
  const sessions = await operator.listSessions(uid);
  expect(sessions.sessions).toHaveLength(1);
  await operator.revokeSession(uid, sessions.sessions[0].name);
  expect((await operator.listSessions(uid)).sessions).toHaveLength(0);
  await new Promise((resolve) => setTimeout(resolve, 1100));
  expect(await seed(target, `admin-sdk-${run}-c`)).toBe(uid);
  const revoked = await operator.revokeAllSessions(uid);
  expect(revoked.revoked_count).toBe(1);
  expect((await operator.listSessions(uid)).sessions).toHaveLength(0);

  // adminFreezeUser, adminUnfreezeUser, adminDeleteUser, adminUndeleteUser.
  const frozen = await operator.freezeUser(uid, {
    reason: "synthetic integration freeze",
  });
  expect(frozen.state).toBe("FROZEN");
  expect(frozen.freeze?.reason).toBe("synthetic integration freeze");
  expect((await operator.unfreezeUser(uid, {})).state).toBe("ACTIVE");
  await expect(operator.deleteUser(uid)).rejects.toMatchObject({
    httpStatus: 403,
  });
  const deleted = await admin.deleteUser(uid);
  expect(deleted.state).toBe("PENDING_DELETION");
  expect(deleted.delete_time).not.toBeNull();
  expect(
    (
      await admin.listUsers({
        filter: userFilter.eq("identity.email", target),
        show_deleted: true,
      })
    ).users.map((u) => u.name),
  ).toContain(name);
  expect((await admin.undeleteUser(uid)).state).toBe("ACTIVE");

  // adminListAuditEvents: worker is asynchronous; poll reads, never repeat writes.
  let events: AuditEvent[] = [];
  for (let attempt = 0; attempt < 30; attempt++) {
    events = [];
    let cursor = "";
    do {
      const audit = await operator.listAuditEvents(uid, {
        page_size: 2,
        page_token: cursor,
      });
      events.push(...audit.audit_events);
      cursor = audit.next_page_token;
    } while (cursor);
    if (events.some((e) => e.event_type === "USER_UNDELETED")) break;
    await new Promise((resolve) => setTimeout(resolve, 100));
  }
  expect(events.some((e) => e.event_type === "IDENTITY_REVEALED")).toBe(true);
  expect(events.some((e) => e.event_type === "USER_UNDELETED")).toBe(true);
  expect(events.some((e) => e.actor_kind === "ADMIN")).toBe(true);
  expect(events.some((e) => e.result === "SUCCESS")).toBe(true);
  expect(events.some((e) => e.reason === null)).toBe(true);
  expect(events.some((e) => e.session_id === null)).toBe(true);
  expect(events.some((e) => !Object.hasOwn(e, "flow_id"))).toBe(true);
  for (const event of events) {
    expect(event.name.startsWith(name + "/auditEvents/")).toBe(true);
    expect(event).toHaveProperty("admin_subject");
    expect(event).toHaveProperty("identity_kind");
  }
  await expect(
    operator.listAuditEvents(uid, { page_token: "invalid-cursor" }),
  ).rejects.toMatchObject({ httpStatus: 400, reason: "INVALID_PAGE_TOKEN" });
  // An API error remains typed; no consumer sessions are owned by the SDK.
  await expect(invalid.getUser(uid)).rejects.toBeInstanceOf(
    AccountAdminApiError,
  );
});
