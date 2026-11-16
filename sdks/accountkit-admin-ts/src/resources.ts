// Internal wire resource grammar; this module is not a public entry point.
export const resource = {
  users: "users",
  identities: "identities",
  sessions: "sessions",
  auditEvents: "auditEvents",
  auditEventsField: "audit_events",
} as const;
export const tsidPattern = "[0-9a-hjkmnp-tv-z]{13}";
export const resourcePattern = {
  user: new RegExp(`^${resource.users}/u_${tsidPattern}$`),
  identity: new RegExp(
    `^${resource.users}/u_${tsidPattern}/${resource.identities}/i_${tsidPattern}$`,
  ),
  session: new RegExp(
    `^${resource.users}/u_${tsidPattern}/${resource.sessions}/s_${tsidPattern}$`,
  ),
  audit: new RegExp(
    `^${resource.users}/u_${tsidPattern}/${resource.auditEvents}/[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`,
  ),
};
