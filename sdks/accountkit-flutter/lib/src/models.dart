import 'dart:convert';

import 'auth_protocol.dart';
import 'errors.dart';
import 'validation_patterns.dart';
import 'wire_fields.dart';

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
      AuthScope.user => const SignedIn(),
      AuthScope.bind => const NeedsBinding(),
      AuthScope.undelete => const PendingDeletion(),
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
      IdentityKind.phone || IdentityKind.email => [
        CredentialField.target,
        CredentialField.codeId,
        CredentialField.code,
      ],
      IdentityKind.apple ||
      IdentityKind.google ||
      IdentityKind.oidc => [CredentialField.idToken, CredentialField.nonce],
      IdentityKind.wechat ||
      IdentityKind.alipay => [CredentialField.appId, CredentialField.code],
      IdentityKind.qq ||
      IdentityKind.weibo => [CredentialField.appId, TokenField.accessToken],
      IdentityKind.facebook =>
        credential.containsKey(TokenField.accessToken)
            ? [TokenField.accessToken]
            : [CredentialField.idToken, CredentialField.nonce],
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
    if ((kind == IdentityKind.phone || kind == IdentityKind.email) &&
        !codeIdPattern.hasMatch(credential[CredentialField.codeId]! as String))
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
    IdentityKind.email,
    target: target,
    codeId: codeId,
    code: code,
  );
  factory IdentityCredential.phone({
    required String target,
    required String codeId,
    required String code,
  }) => IdentityCredential.code(
    IdentityKind.phone,
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
    if (![IdentityKind.phone, IdentityKind.email].contains(channel) ||
        target.isEmpty ||
        code.isEmpty ||
        !codeIdPattern.hasMatch(codeId))
      throw const AccountConfigurationError();
    return IdentityCredential._(channel, {
      CredentialField.target: target,
      CredentialField.codeId: codeId,
      CredentialField.code: code,
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
  }) => _fields(IdentityKind.apple, {
    CredentialField.idToken: idToken,
    CredentialField.nonce: nonce,
  });
  factory IdentityCredential.google({
    required String idToken,
    required String nonce,
  }) => _fields(IdentityKind.google, {
    CredentialField.idToken: idToken,
    CredentialField.nonce: nonce,
  });
  factory IdentityCredential.oidc({
    String kind = IdentityKind.oidc,
    required String idToken,
    required String nonce,
  }) => _fields(kind, {
    CredentialField.idToken: idToken,
    CredentialField.nonce: nonce,
  });
  factory IdentityCredential.wechat({
    required String appId,
    required String code,
  }) => _fields(IdentityKind.wechat, {
    CredentialField.appId: appId,
    CredentialField.code: code,
  });
  factory IdentityCredential.alipay({
    required String appId,
    required String code,
  }) => _fields(IdentityKind.alipay, {
    CredentialField.appId: appId,
    CredentialField.code: code,
  });
  factory IdentityCredential.qq({
    required String appId,
    required String accessToken,
  }) => _fields(IdentityKind.qq, {
    CredentialField.appId: appId,
    TokenField.accessToken: accessToken,
  });
  factory IdentityCredential.weibo({
    required String appId,
    required String accessToken,
  }) => _fields(IdentityKind.weibo, {
    CredentialField.appId: appId,
    TokenField.accessToken: accessToken,
  });
  factory IdentityCredential.facebookIdToken({
    required String idToken,
    required String nonce,
  }) => _fields(IdentityKind.facebook, {
    CredentialField.idToken: idToken,
    CredentialField.nonce: nonce,
  });
  factory IdentityCredential.facebookAccessToken(String accessToken) =>
      _fields(IdentityKind.facebook, {TokenField.accessToken: accessToken});
  final String kind;
  final JsonObject _value;
  bool get isCode => kind == IdentityKind.phone || kind == IdentityKind.email;
  JsonObject toJson() => {
    CredentialField.kind: kind,
    CredentialField.credential: copyJson(_value),
  };
  @override
  String toString() => 'IdentityCredential(redacted)';
}

sealed class SignInResult {
  const SignInResult();
  factory SignInResult.fromJson(JsonObject j) {
    if (j.containsKey(TokenField.accessToken) &&
        j.containsKey(CompletionField.nextAction))
      throw const AccountProtocolError();
    return j.containsKey(TokenField.accessToken)
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
    if (text(j, TokenField.tokenType) != AuthProtocol.bearer)
      throw const AccountProtocolError();
    return TokenPair(
      accessToken: text(j, TokenField.accessToken),
      refreshToken: text(j, TokenField.refreshToken),
      expiresIn: integer(j, TokenField.expiresIn),
      refreshExpiresIn: integer(j, TokenField.refreshExpiresIn),
      scope: text(j, TokenField.scope),
      userId: text(j, TokenField.userId),
      isNewUser: boolean(j, TokenField.isNewUser),
      hintEmail: j[CompletionField.hintEmail] == null
          ? null
          : text(j, CompletionField.hintEmail),
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
    : accessToken = text(j, TokenField.accessToken),
      expiresIn = integer(j, TokenField.expiresIn),
      scope = text(j, TokenField.scope),
      userId = text(j, TokenField.userId) {
    if (text(j, TokenField.tokenType) != AuthProtocol.bearer ||
        j.containsKey(TokenField.refreshToken) ||
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
    : completionToken = text(j, CompletionField.token),
      expireTime = time(j, ResourceField.expireTime),
      enabledIdentityKinds = strings(j, CompletionField.enabledKinds),
      requiredIdentityKinds = strings(j, CompletionField.requiredKinds),
      hintEmail = j[CompletionField.hintEmail] == null
          ? null
          : text(j, CompletionField.hintEmail) {
    if (text(j, CompletionField.nextAction) != AuthProtocol.completeIdentity ||
        !enabledIdentityKinds.any(
          (k) => k == IdentityKind.email || k == IdentityKind.phone,
        ) ||
        j.containsKey(TokenField.refreshToken) ||
        j.containsKey(TokenField.userId) ||
        j.containsKey(TokenField.scope))
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
    : codeId = text(j, CredentialField.codeId),
      expireTime = time(j, ResourceField.expireTime);
  final String codeId;
  final DateTime expireTime;
  @override
  String toString() => 'CodeChallenge(redacted)';
}

final class BasicProfilePatch {
  const BasicProfilePatch({this.displayName, this.avatarUrl});
  final String? displayName, avatarUrl;
  JsonObject toJson() => {
    if (displayName != null) ResourceField.displayName: displayName,
    if (avatarUrl != null) ResourceField.avatarUrl: avatarUrl,
  };
}

final class AccountUser {
  AccountUser.fromJson(JsonObject j)
    : name = text(j, ResourceField.name),
      state = text(j, 'state'),
      displayName = text(j, ResourceField.displayName, empty: true),
      avatarUrl = text(j, ResourceField.avatarUrl, empty: true),
      createTime = time(j, ResourceField.createTime),
      deleteTime = optionalTime(j, 'delete_time'),
      purgeTime = optionalTime(j, 'purge_time');
  final String name, state, displayName, avatarUrl;
  final DateTime createTime;
  final DateTime? deleteTime, purgeTime;
}

final class AccountIdentity {
  AccountIdentity.fromJson(JsonObject j)
    : name = text(j, ResourceField.name),
      kind = text(j, CredentialField.kind),
      maskedSubject = text(j, 'masked_subject', empty: true),
      createTime = time(j, ResourceField.createTime);
  final String name, kind, maskedSubject;
  final DateTime createTime;
}

final class AuthenticationIdentity {
  AuthenticationIdentity.fromJson(JsonObject j)
    : name = text(j, ResourceField.name),
      kind = text(j, CredentialField.kind);
  final String name, kind;
}

class AccountSession {
  AccountSession.fromJson(JsonObject j)
    : name = text(j, ResourceField.name),
      deviceId = text(j, 'device_id', empty: true),
      deviceName = text(j, 'device_name', empty: true),
      createTime = time(j, ResourceField.createTime),
      lastUsedTime = time(j, 'last_used_time'),
      isCurrent = boolean(j, 'is_current'),
      clientInfo = j[ResourceField.clientInfo] == null
          ? null
          : Map.unmodifiable(copyJson(object(j[ResourceField.clientInfo]))),
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
    : name = text(j, ResourceField.name),
      _attributes = copyJson(object(j[ResourceField.profileAttributes]));
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
