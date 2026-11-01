import 'dart:convert';

import 'errors.dart';

typedef JsonObject = Map<String, Object?>;
JsonObject object(Object? value) {
  if (value is! Map<String, Object?>) throw const AccountProtocolError();
  return value;
}

String text(JsonObject j, String key, {bool empty = false}) {
  final v = j[key];
  if (v is! String || (!empty && v.isEmpty)) throw const AccountProtocolError();
  return v;
}

int integer(JsonObject j, String key) {
  final v = j[key];
  if (v is! int || v < 0) throw const AccountProtocolError();
  return v;
}

bool boolean(JsonObject j, String key) {
  final v = j[key];
  if (v is! bool) throw const AccountProtocolError();
  return v;
}

DateTime time(JsonObject j, String key) {
  final raw = text(j, key);
  if (!RegExp(r'^\d{4}-\d{2}-\d{2}T.*(?:Z|[+-]\d{2}:\d{2})$').hasMatch(raw))
    throw const AccountProtocolError();
  final date = raw.substring(0, 10).split('-').map(int.parse).toList();
  final checked = DateTime.utc(date[0], date[1], date[2]);
  if (checked.year != date[0] ||
      checked.month != date[1] ||
      checked.day != date[2])
    throw const AccountProtocolError();
  final value = DateTime.tryParse(raw);
  if (value == null) throw const AccountProtocolError();
  return value.toUtc();
}

DateTime? optionalTime(JsonObject j, String key) =>
    j[key] == null ? null : time(j, key);
List<T> items<T>(JsonObject j, String key, T Function(JsonObject) decode) {
  final value = j[key];
  if (value is! List<Object?>) throw const AccountProtocolError();
  return List.unmodifiable(value.map((e) => decode(object(e))));
}

JsonObject copyJson(JsonObject j) => object(jsonDecode(jsonEncode(j)));
List<String> strings(JsonObject j, String key) {
  final v = j[key];
  if (v is! List<Object?> || v.any((e) => e is! String))
    throw const AccountProtocolError();
  return List.unmodifiable(v.cast<String>());
}

sealed class AuthenticationState {
  const AuthenticationState();
}

final class SignedOut extends AuthenticationState {
  const SignedOut();
}

final class NeedsBinding extends AuthenticationState {
  const NeedsBinding();
}

final class PendingDeletion extends AuthenticationState {
  const PendingDeletion();
}

final class SignedIn extends AuthenticationState {
  const SignedIn();
}

AuthenticationState authenticationStateForScope(String scope) =>
    switch (scope) {
      'user' => const SignedIn(),
      'user:bind' => const NeedsBinding(),
      'user:undelete' => const PendingDeletion(),
      _ => throw const AccountProtocolError(),
    };

final class IdentityCredential {
  IdentityCredential._(this.kind, JsonObject value) : _value = copyJson(value) {
    if (!RegExp(r'^[A-Z][A-Z0-9_]{1,31}$').hasMatch(kind) ||
        utf8.encode(jsonEncode(value)).length > 16384)
      throw const AccountConfigurationError();
  }
  factory IdentityCredential.custom(String kind, JsonObject credential) {
    List<String>? keys = switch (kind) {
      'PHONE' || 'EMAIL' => ['target', 'code_id', 'code'],
      'APPLE' || 'GOOGLE' || 'OIDC' => ['id_token', 'nonce'],
      'WECHAT' || 'ALIPAY' => ['app_id', 'code'],
      'QQ' || 'WEIBO' => ['app_id', 'access_token'],
      'FACEBOOK' =>
        credential.containsKey('access_token')
            ? ['access_token']
            : ['id_token', 'nonce'],
      _ => null,
    };
    if (keys != null &&
        (credential.length != keys.length ||
            keys.any(
              (key) =>
                  credential[key] is! String ||
                  (credential[key]! as String).isEmpty,
            )))
      throw const AccountConfigurationError();
    if ((kind == 'PHONE' || kind == 'EMAIL') &&
        !RegExp(r'^[0-9a-f]{32}$').hasMatch(credential['code_id']! as String))
      throw const AccountConfigurationError();
    try {
      return IdentityCredential._(kind, credential);
    } on AccountError {
      rethrow;
    } catch (_) {
      throw const AccountConfigurationError();
    }
  }
  factory IdentityCredential.email({
    required String target,
    required String codeId,
    required String code,
  }) => IdentityCredential.code(
    'EMAIL',
    target: target,
    codeId: codeId,
    code: code,
  );
  factory IdentityCredential.phone({
    required String target,
    required String codeId,
    required String code,
  }) => IdentityCredential.code(
    'PHONE',
    target: target,
    codeId: codeId,
    code: code,
  );
  factory IdentityCredential.code(
    String channel, {
    required String target,
    required String codeId,
    required String code,
  }) {
    if (!['PHONE', 'EMAIL'].contains(channel) ||
        target.isEmpty ||
        code.isEmpty ||
        !RegExp(r'^[0-9a-f]{32}$').hasMatch(codeId))
      throw const AccountConfigurationError();
    return IdentityCredential._(channel, {
      'target': target,
      'code_id': codeId,
      'code': code,
    });
  }
  static IdentityCredential _fields(String kind, JsonObject value) {
    if (value.values.any((v) => v is! String || v.isEmpty))
      throw const AccountConfigurationError();
    return IdentityCredential._(kind, value);
  }

  factory IdentityCredential.apple({
    required String idToken,
    required String nonce,
  }) => _fields('APPLE', {'id_token': idToken, 'nonce': nonce});
  factory IdentityCredential.google({
    required String idToken,
    required String nonce,
  }) => _fields('GOOGLE', {'id_token': idToken, 'nonce': nonce});
  factory IdentityCredential.oidc({
    String kind = 'OIDC',
    required String idToken,
    required String nonce,
  }) => _fields(kind, {'id_token': idToken, 'nonce': nonce});
  factory IdentityCredential.wechat({
    required String appId,
    required String code,
  }) => _fields('WECHAT', {'app_id': appId, 'code': code});
  factory IdentityCredential.alipay({
    required String appId,
    required String code,
  }) => _fields('ALIPAY', {'app_id': appId, 'code': code});
  factory IdentityCredential.qq({
    required String appId,
    required String accessToken,
  }) => _fields('QQ', {'app_id': appId, 'access_token': accessToken});
  factory IdentityCredential.weibo({
    required String appId,
    required String accessToken,
  }) => _fields('WEIBO', {'app_id': appId, 'access_token': accessToken});
  factory IdentityCredential.facebookIdToken({
    required String idToken,
    required String nonce,
  }) => _fields('FACEBOOK', {'id_token': idToken, 'nonce': nonce});
  factory IdentityCredential.facebookAccessToken(String accessToken) =>
      _fields('FACEBOOK', {'access_token': accessToken});
  final String kind;
  final JsonObject _value;
  bool get isCode => kind == 'PHONE' || kind == 'EMAIL';
  JsonObject toJson() => {'kind': kind, 'credential': copyJson(_value)};
  @override
  String toString() => 'IdentityCredential(redacted)';
}

sealed class SignInResult {
  const SignInResult();
  factory SignInResult.fromJson(JsonObject j) {
    if (j.containsKey('access_token') && j.containsKey('next_action'))
      throw const AccountProtocolError();
    return j.containsKey('access_token')
        ? TokenPair.fromJson(j)
        : PendingSignIn.fromJson(j);
  }
}

final class TokenPair extends SignInResult {
  TokenPair({
    required this.accessToken,
    required this.refreshToken,
    required this.expiresIn,
    required this.refreshExpiresIn,
    required this.scope,
    required this.userId,
    required this.isNewUser,
    this.hintEmail,
  }) {
    authenticationStateForScope(scope);
    if (accessToken.isEmpty ||
        refreshToken.isEmpty ||
        expiresIn <= 0 ||
        refreshExpiresIn <= 0 ||
        userId.isEmpty)
      throw const AccountProtocolError();
  }
  factory TokenPair.fromJson(JsonObject j) {
    if (text(j, 'token_type') != 'Bearer') throw const AccountProtocolError();
    return TokenPair(
      accessToken: text(j, 'access_token'),
      refreshToken: text(j, 'refresh_token'),
      expiresIn: integer(j, 'expires_in'),
      refreshExpiresIn: integer(j, 'refresh_expires_in'),
      scope: text(j, 'scope'),
      userId: text(j, 'user_id'),
      isNewUser: boolean(j, 'is_new_user'),
      hintEmail: j['hint_email'] == null ? null : text(j, 'hint_email'),
    );
  }
  final String accessToken, refreshToken, scope, userId;
  final int expiresIn, refreshExpiresIn;
  final bool isNewUser;
  final String? hintEmail;
  AuthenticationState get authenticationState =>
      authenticationStateForScope(scope);
  @override
  String toString() => 'TokenPair(redacted)';
}

final class AccessTokenResult {
  AccessTokenResult.fromJson(JsonObject j)
    : accessToken = text(j, 'access_token'),
      expiresIn = integer(j, 'expires_in'),
      scope = text(j, 'scope'),
      userId = text(j, 'user_id') {
    if (text(j, 'token_type') != 'Bearer' ||
        j.containsKey('refresh_token') ||
        expiresIn <= 0)
      throw const AccountProtocolError();
    authenticationStateForScope(scope);
  }
  final String accessToken, scope, userId;
  final int expiresIn;
  @override
  String toString() => 'AccessTokenResult(redacted)';
}

final class PendingSignIn extends SignInResult {
  PendingSignIn.fromJson(JsonObject j)
    : completionToken = text(j, 'sign_in_completion_token'),
      expireTime = time(j, 'expire_time'),
      enabledIdentityKinds = strings(j, 'enabled_identity_kinds'),
      requiredIdentityKinds = strings(j, 'required_identity_kinds'),
      hintEmail = j['hint_email'] == null ? null : text(j, 'hint_email') {
    if (text(j, 'next_action') != 'COMPLETE_IDENTITY' ||
        !enabledIdentityKinds.any((k) => k == 'EMAIL' || k == 'PHONE') ||
        j.containsKey('refresh_token') ||
        j.containsKey('user_id') ||
        j.containsKey('scope'))
      throw const AccountProtocolError();
  }
  final String completionToken;
  final DateTime expireTime;
  final List<String> enabledIdentityKinds, requiredIdentityKinds;
  final String? hintEmail;
  @override
  String toString() => 'PendingSignIn(redacted)';
}

final class CodeChallenge {
  CodeChallenge.fromJson(JsonObject j)
    : codeId = text(j, 'code_id'),
      expireTime = time(j, 'expire_time');
  final String codeId;
  final DateTime expireTime;
  @override
  String toString() => 'CodeChallenge(redacted)';
}

final class BasicProfilePatch {
  const BasicProfilePatch({this.displayName, this.avatarUrl});
  final String? displayName, avatarUrl;
  JsonObject toJson() => {
    if (displayName != null) 'display_name': displayName,
    if (avatarUrl != null) 'avatar_url': avatarUrl,
  };
}

final class AccountUser {
  AccountUser.fromJson(JsonObject j)
    : name = text(j, 'name'),
      state = text(j, 'state'),
      displayName = text(j, 'display_name', empty: true),
      avatarUrl = text(j, 'avatar_url', empty: true),
      createTime = time(j, 'create_time'),
      deleteTime = optionalTime(j, 'delete_time'),
      purgeTime = optionalTime(j, 'purge_time');
  final String name, state, displayName, avatarUrl;
  final DateTime createTime;
  final DateTime? deleteTime, purgeTime;
}

final class AccountIdentity {
  AccountIdentity.fromJson(JsonObject j)
    : name = text(j, 'name'),
      kind = text(j, 'kind'),
      maskedSubject = text(j, 'masked_subject', empty: true),
      createTime = time(j, 'create_time');
  final String name, kind, maskedSubject;
  final DateTime createTime;
}

final class AuthenticationIdentity {
  AuthenticationIdentity.fromJson(JsonObject j)
    : name = text(j, 'name'),
      kind = text(j, 'kind');
  final String name, kind;
}

class AccountSession {
  AccountSession.fromJson(JsonObject j)
    : name = text(j, 'name'),
      deviceId = text(j, 'device_id', empty: true),
      deviceName = text(j, 'device_name', empty: true),
      createTime = time(j, 'create_time'),
      lastUsedTime = time(j, 'last_used_time'),
      isCurrent = boolean(j, 'is_current'),
      clientInfo = j['client_info'] == null
          ? null
          : Map.unmodifiable(copyJson(object(j['client_info']))),
      clientInfoUpdateTime = optionalTime(j, 'client_info_update_time');
  final String name, deviceId, deviceName;
  final DateTime createTime, lastUsedTime;
  final bool isCurrent;
  final JsonObject? clientInfo;
  final DateTime? clientInfoUpdateTime;
}

final class CurrentSession extends AccountSession {
  CurrentSession.fromJson(super.j)
    : authTime = time(j, 'auth_time'),
      authenticationIdentities = items(
        j,
        'authentication_identities',
        AuthenticationIdentity.fromJson,
      ),
      super.fromJson() {
    if (!isCurrent || authenticationIdentities.isEmpty)
      throw const AccountProtocolError();
  }
  final DateTime authTime;
  final List<AuthenticationIdentity> authenticationIdentities;
}

final class AccountProfile {
  AccountProfile.fromJson(JsonObject j)
    : name = text(j, 'name'),
      _attributes = copyJson(object(j['profile_attributes']));
  final String name;
  final JsonObject _attributes;
  JsonObject get profileAttributes => copyJson(_attributes);
}

enum SessionSync { confirmed, sessionEnded, unconfirmed }

final class AccountMutationResult<T> {
  const AccountMutationResult(this.value, this.sessionSync, {this.syncError});
  final T value;
  final SessionSync sessionSync;
  final AccountError? syncError;
}

final class SignOutResult {
  const SignOutResult({this.remoteError});
  final AccountError? remoteError;
}
