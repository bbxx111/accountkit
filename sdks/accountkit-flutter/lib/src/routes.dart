// Internal protocol vocabulary; deliberately absent from public exports.
abstract final class AccountRoute {
  static const sendSignInCode = '/users:sendSignInCode';
  static const signInWithCode = '/users:signInWithCode';
  static const signInWithIdp = '/users:signInWithIdp';
  static const sendCompletionCode = '/users:sendSignInCompletionCode';
  static const completeSignIn = '/users:completeSignIn';
  static const token = '/token';
  static const revoke = '/revoke';
  static const me = '/users/me';
  static const identities = '/users/me/identities';
  static const sessions = '/users/me/sessions';
  static const profile = '/users/me/profile';
  static const anonymous = [
    sendSignInCode,
    signInWithCode,
    signInWithIdp,
    sendCompletionCode,
    completeSignIn,
    token,
    revoke,
  ];
}
