import { AccountAdminArgumentError } from "./errors";
import { isTimestamp } from "./timestamp";
const filterFields = [
  "state",
  "identity.phone",
  "identity.phone_prefix",
  "identity.phone_suffix",
  "identity.email",
  "identity.email_prefix",
  "identity.email_domain",
] as const;
export type UserFilterField = (typeof filterFields)[number];
const fields: ReadonlySet<string> = new Set(filterFields);
const comparisonOperators = [">=", "<"] as const;
function quote(value: string) {
  // The formal parser has no quoted-string escape syntax. Never invent one.
  if (
    typeof value !== "string" ||
    !value ||
    /["\u0000-\u001f\u007f]/u.test(value)
  )
    throw new AccountAdminArgumentError();
  return `"${value}"`;
}
export const userFilter = {
  eq(field: UserFilterField, value: string): string {
    if (!fields.has(field)) throw new AccountAdminArgumentError();
    return `${field} = ${quote(value)}`;
  },
  compare(
    field: "create_time",
    operator: (typeof comparisonOperators)[number],
    value: string,
  ): string {
    if (
      field !== "create_time" ||
      !comparisonOperators.includes(operator) ||
      !isTimestamp(value)
    )
      throw new AccountAdminArgumentError();
    return `${field} ${operator} ${quote(value)}`;
  },
  and(...terms: string[]): string {
    if (!terms.length || terms.some((t) => typeof t !== "string" || !t.trim()))
      throw new AccountAdminArgumentError();
    return terms.join(" AND ");
  },
};
