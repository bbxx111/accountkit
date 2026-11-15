// Internal protocol vocabulary; deliberately absent from public exports.
abstract final class CredentialField {
  static const target = 'target';
  static const codeId = 'code_id';
  static const code = 'code';
  static const idToken = 'id_token';
  static const nonce = 'nonce';
  static const appId = 'app_id';
  static const kind = 'kind';
  static const credential = 'credential';
  static const channel = 'channel';
}

abstract final class TokenField {
  static const accessToken = 'access_token';
  static const refreshToken = 'refresh_token';
  static const tokenType = 'token_type';
  static const expiresIn = 'expires_in';
  static const refreshExpiresIn = 'refresh_expires_in';
  static const scope = 'scope';
  static const userId = 'user_id';
  static const isNewUser = 'is_new_user';
  static const grantType = 'grant_type';
  static const token = 'token';
}

abstract final class CompletionField {
  static const token = 'sign_in_completion_token';
  static const nextAction = 'next_action';
  static const hintEmail = 'hint_email';
  static const enabledKinds = 'enabled_identity_kinds';
  static const requiredKinds = 'required_identity_kinds';
}

abstract final class StoredTokenField {
  static const accessExpireTime = 'access_expire_time';
  static const refreshExpireTime = 'refresh_expire_time';
}

abstract final class ResourceField {
  static const expireTime = 'expire_time';
  static const displayName = 'display_name';
  static const avatarUrl = 'avatar_url';
  static const name = 'name';
  static const createTime = 'create_time';
  static const clientInfo = 'client_info';
  static const profileAttributes = 'profile_attributes';
}

abstract final class ErrorField {
  static const error = 'error';
  static const reason = 'reason';
  static const status = 'status';
  static const retryAfterSeconds = 'retry_after_seconds';
}
