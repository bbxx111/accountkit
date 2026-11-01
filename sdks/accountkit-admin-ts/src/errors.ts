export class AccountAdminArgumentError extends Error {
  override name = "AccountAdminArgumentError";
  constructor() {
    super("Invalid account API argument");
  }
}
export class AccountAdminTokenProviderError extends Error {
  override name = "AccountAdminTokenProviderError";
  constructor() {
    super("Access token provider failed");
  }
}
export class AccountAdminCancelledError extends Error {
  override name = "AccountAdminCancelledError";
  constructor() {
    super("Account API request cancelled");
  }
}
export class AccountAdminTransportError extends Error {
  override name = "AccountAdminTransportError";
  constructor() {
    super("Account API transport failed");
  }
}
export class AccountAdminProtocolError extends Error {
  override name = "AccountAdminProtocolError";
  constructor() {
    super("Invalid account API response");
  }
}
export class AccountAdminApiError extends Error {
  override name = "AccountAdminApiError";
  readonly httpStatus: number;
  readonly status: string;
  readonly reason?: string;
  readonly retryAfterSeconds?: number;
  readonly requestId?: string;
  declare readonly serverMessage: string;
  constructor(data: {
    httpStatus: number;
    status: string;
    reason?: string;
    message: string;
    retryAfterSeconds?: number;
    requestId?: string;
  }) {
    super("Account API request failed");
    this.httpStatus = data.httpStatus;
    this.status = data.status;
    this.reason = data.reason;
    this.retryAfterSeconds = data.retryAfterSeconds;
    this.requestId = data.requestId;
    Object.defineProperty(this, "serverMessage", {
      value: data.message,
      enumerable: false,
      writable: false,
    });
  }
  override toString() {
    return `${this.name}: HTTP ${this.httpStatus}`;
  }
  [Symbol.for("nodejs.util.inspect.custom")]() {
    return this.toString();
  }
}
