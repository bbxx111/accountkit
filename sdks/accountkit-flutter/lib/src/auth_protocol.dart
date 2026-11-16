// Internal protocol vocabulary; deliberately absent from public exports.
abstract final class AuthScope {
  static const user = 'user';
  static const bind = 'user:bind';
  static const undelete = 'user:undelete';
}

abstract final class IdentityKind {
  static const phone = 'PHONE';
  static const email = 'EMAIL';
  static const apple = 'APPLE';
  static const google = 'GOOGLE';
  static const oidc = 'OIDC';
  static const wechat = 'WECHAT';
  static const alipay = 'ALIPAY';
  static const qq = 'QQ';
  static const weibo = 'WEIBO';
  static const facebook = 'FACEBOOK';
}

abstract final class AuthProtocol {
  static const completeIdentity = 'COMPLETE_IDENTITY';
  static const bearer = 'Bearer';
  static const refreshGrant = 'refresh_token';
}

abstract final class AuthReason {
  static const reauthenticationRequired = 'REAUTHENTICATION_REQUIRED';
  static const codeExpired = 'CODE_EXPIRED';
  static const completionInvalid = 'SIGN_IN_COMPLETION_INVALID';
  static const alreadyCompleted = 'SIGN_IN_ALREADY_COMPLETED';
}

abstract final class OAuthError {
  static const invalidGrant = 'invalid_grant';
}
