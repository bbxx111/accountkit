import { AccountAdminProtocolError } from "./errors";
import { isTimestamp } from "./timestamp";
type Decoder = (value: unknown) => unknown;
const fail = (): never => {
  throw new AccountAdminProtocolError();
};
const str: Decoder = (v) => (typeof v === "string" ? v : fail());
const integer: Decoder = (v) =>
  typeof v === "number" && Number.isSafeInteger(v) && v >= 0 ? v : fail();
const pattern =
  (re: RegExp): Decoder =>
  (v) =>
    typeof v === "string" && re.test(v) ? v : fail();
const time: Decoder = (v) => (isTimestamp(v) ? v : fail());
const nullable =
  (d: Decoder): Decoder =>
  (v) =>
    v === null ? null : d(v);
const array =
  (d: Decoder): Decoder =>
  (v) =>
    Array.isArray(v) ? v.map(d) : fail();
const obj =
  (
    required: Record<string, Decoder>,
    optional: Record<string, Decoder> = {},
  ): Decoder =>
  (v) => {
    if (!v || typeof v !== "object" || Array.isArray(v)) return fail();
    const source = v as Record<string, unknown>,
      out: Record<string, unknown> = {};
    for (const [key, d] of Object.entries(required)) out[key] = d(source[key]);
    for (const [key, d] of Object.entries(optional))
      if (Object.hasOwn(source, key)) out[key] = d(source[key]);
    return out;
  };
const id = "[0-9a-hjkmnp-tv-z]{13}";
const userName = pattern(new RegExp(`^users/u_${id}$`));
const identityName = pattern(new RegExp(`^users/u_${id}/identities/i_${id}$`));
const sessionName = pattern(new RegExp(`^users/u_${id}/sessions/s_${id}$`));
const enumString = pattern(/^[A-Z][A-Z0-9_]*$/);
const userFields = {
  name: userName,
  state: enumString,
  display_name: str,
  avatar_url: str,
  create_time: time,
  update_time: time,
  delete_time: nullable(time),
  purge_time: nullable(time),
  freeze: nullable(
    obj({
      freeze_time: time,
      reason: str,
      actor_subject: str,
      actor_username: str,
    }),
  ),
};
const identity = obj({
  name: identityName,
  kind: enumString,
  masked_subject: str,
  create_time: time,
});
const software = obj({}, { name: str, version: str });
const extensions: Decoder = (v) =>
  v && typeof v === "object" && !Array.isArray(v) ? structuredClone(v) : fail();
const info = obj(
  {},
  {
    type: enumString,
    os: software,
    browser: software,
    device: obj({}, { type: enumString, manufacturer: str, model: str }),
    app: obj({}, { name: str, version: str, build: str }),
    extensions,
  },
);
const session = obj(
  {
    name: sessionName,
    device_id: str,
    device_name: str,
    create_time: time,
    last_used_time: time,
  },
  { client_info: info, client_info_update_time: time },
);
const audit = obj(
  {
    name: pattern(
      new RegExp(
        `^users/u_${id}/auditEvents/[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`,
      ),
    ),
    event_type: enumString,
    actor_kind: enumString,
    result: nullable(enumString),
    reason: nullable(str),
    session_id: nullable(pattern(new RegExp(`^s_${id}$`))),
    identity_kind: nullable(enumString),
    subject_hint: nullable(str),
    ip: nullable(str),
    device_id: nullable(str),
    request_id: nullable(str),
    admin_issuer: nullable(str),
    admin_subject: nullable(str),
    admin_username: nullable(str),
    occur_time: time,
  },
  { flow_id: nullable(pattern(new RegExp(`^f_${id}$`))) },
);
export const decode = {
  user: obj(userFields),
  detail: obj({
    ...userFields,
    identities: array(identity),
    active_session_count: integer,
  }),
  users: obj({ users: array(obj(userFields)), next_page_token: str }),
  sessions: obj({ sessions: array(session) }),
  reveal: obj({ name: identityName, kind: enumString, subject: str }),
  revokeAll: obj({ revoked_count: integer }),
  audit: obj({ audit_events: array(audit), next_page_token: str }),
};
