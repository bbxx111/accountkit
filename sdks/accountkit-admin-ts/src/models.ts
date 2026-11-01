export interface AdminFreeze {
  freeze_time: string;
  reason: string;
  actor_subject: string;
  actor_username: string;
}
export interface AdminUser {
  name: string;
  state: string;
  display_name: string;
  avatar_url: string;
  create_time: string;
  update_time: string;
  delete_time: string | null;
  purge_time: string | null;
  freeze: AdminFreeze | null;
}
export interface AdminIdentity {
  name: string;
  kind: string;
  masked_subject: string;
  create_time: string;
}
export interface AdminUserDetail extends AdminUser {
  identities: AdminIdentity[];
  active_session_count: number;
}
export interface AdminUserList {
  users: AdminUser[];
  next_page_token: string;
}
export interface AdminClientInfo {
  type?: string;
  os?: { name?: string; version?: string };
  device?: { type?: string; manufacturer?: string; model?: string };
  app?: { name?: string; version?: string; build?: string };
  browser?: { name?: string; version?: string };
  extensions?: Record<string, unknown>;
}
export interface AdminSession {
  name: string;
  device_id: string;
  device_name: string;
  create_time: string;
  last_used_time: string;
  client_info?: AdminClientInfo;
  client_info_update_time?: string;
}
export interface AdminSessionList {
  sessions: AdminSession[];
}
export interface RevealIdentity {
  name: string;
  kind: string;
  subject: string;
}
export interface RevokeAllResult {
  revoked_count: number;
}
export interface FreezeRequest {
  reason: string;
}
export interface UnfreezeRequest {
  reason?: string;
}
export interface AuditEvent {
  name: string;
  event_type: string;
  actor_kind: string;
  result: string | null;
  reason: string | null;
  session_id: string | null;
  flow_id?: string | null;
  identity_kind: string | null;
  subject_hint: string | null;
  ip: string | null;
  device_id: string | null;
  request_id: string | null;
  admin_issuer: string | null;
  admin_subject: string | null;
  admin_username: string | null;
  occur_time: string;
}
export interface AuditEventList {
  audit_events: AuditEvent[];
  next_page_token: string;
}
export interface PageOptions {
  page_size?: number;
  page_token?: string;
}
export interface ListUsersOptions extends PageOptions {
  filter?: string;
  show_deleted?: boolean;
}
export interface RequestOptions {
  signal?: AbortSignal;
  requestId?: string;
}
