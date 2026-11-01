import 'dart:async';
import 'dart:convert';
import 'dart:typed_data';

import 'package:dio/dio.dart';

import 'client.dart';
import 'controller.dart';
import 'errors.dart';
import 'models.dart';
import 'session.dart';
import 'platform.dart';

final class FixedDeviceProvider implements DeviceProvider {
  FixedDeviceProvider(this.value);
  DeviceSnapshot value;
  @override
  Future<DeviceSnapshot> snapshot() async => value;
}

final class MemoryTokenStore implements TokenStore {
  MemoryTokenStore([this.record]);
  TokenRecord? record;
  bool writeFailure = false, readFailure = false, clearFailure = false;
  Future<void> Function()? beforeWrite, beforeClear;
  @override
  Future<TokenRecord?> read() async {
    if (readFailure) throw StateError('injected');
    return record;
  }

  @override
  Future<void> write(TokenRecord value) async {
    await beforeWrite?.call();
    if (writeFailure) throw StateError('injected');
    record = value;
  }

  @override
  Future<void> clear() async {
    await beforeClear?.call();
    if (clearFailure) throw StateError('injected');
    record = null;
  }
}

final class MutableAccountClock implements AccountClock {
  MutableAccountClock(this.value);
  DateTime value;
  @override
  DateTime now() => value.toUtc();
  void advance(Duration duration) => value = value.add(duration);
}

/// Scriptable in-process HTTP peer using the same public runtime as the client.
/// No platform channels are used. A callback may return a delayed response or
/// throw a typed AccountError to model failures.
final class FakeAccountApi extends AccountClient {
  factory FakeAccountApi({
    TokenStore? tokenStore,
    AccountClock clock = const SystemAccountClock(),
  }) {
    final peer = _FakePeer(clock);
    final dio = Dio()..httpClientAdapter = peer;
    return FakeAccountApi._(peer, dio, tokenStore ?? MemoryTokenStore(), clock);
  }
  FakeAccountApi._(this._peer, Dio dio, TokenStore store, AccountClock clock)
    : super(
        baseUrl: 'https://fake.accountkit.test',
        dio: dio,
        tokenStore: store,
        clock: clock,
        deviceProvider: FixedDeviceProvider(
          const DeviceSnapshot(id: 'fake-device'),
        ),
      );
  final _FakePeer _peer;
  set nextSignIn(SignInResult value) => _peer.nextSignIn = value;
  set signInHandler(Future<SignInResult> Function(IdentityCredential)? value) =>
      _peer.signInHandler = value;
  set codeHandler(
    Future<CodeChallenge> Function(CodePurpose, String, String)? value,
  ) => _peer.codeHandler = value;
  set requestHandler(Future<Object?> Function(String, JsonObject)? value) =>
      _peer.handler = value;
  List<String> get operations => List.unmodifiable(_peer.operations);
  @override
  Future<void> dispose() async {
    await super.dispose();
    _peer.close();
  }
}

final class _FakePeer implements HttpClientAdapter {
  _FakePeer(this.clock);
  final AccountClock clock;
  SignInResult? nextSignIn;
  Future<SignInResult> Function(IdentityCredential)? signInHandler;
  Future<CodeChallenge> Function(CodePurpose, String, String)? codeHandler;
  Future<Object?> Function(String, JsonObject)? handler;
  final operations = <String>[];
  JsonObject _token(TokenPair p) => {
    'access_token': p.accessToken,
    'refresh_token': p.refreshToken,
    'token_type': 'Bearer',
    'expires_in': p.expiresIn,
    'refresh_expires_in': p.refreshExpiresIn,
    'scope': p.scope,
    'user_id': p.userId,
    'is_new_user': p.isNewUser,
  };
  JsonObject _signIn(SignInResult result) => result is TokenPair
      ? _token(result)
      : {
          'next_action': 'COMPLETE_IDENTITY',
          'sign_in_completion_token': (result as PendingSignIn).completionToken,
          'expire_time': result.expireTime.toIso8601String(),
          'enabled_identity_kinds': result.enabledIdentityKinds,
          'required_identity_kinds': result.requiredIdentityKinds,
        };
  @override
  Future<ResponseBody> fetch(
    RequestOptions options,
    Stream<Uint8List>? stream,
    Future<void>? cancelFuture,
  ) async {
    final path = options.uri.path.replaceFirst('/v1', '');
    operations.add('${options.method} $path');
    JsonObject data = {};
    if (options.data is Map<String, Object?>)
      data = options.data! as JsonObject;
    else if (options.data is String)
      data = object(jsonDecode(options.data! as String));
    Object? response;
    int status = 200;
    if (handler != null) {
      response = await handler!('${options.method} $path', data);
    } else if (path.contains('signInWith') || path.endsWith('completeSignIn')) {
      final credential = IdentityCredential.custom(
        text(data, 'kind'),
        object(data['credential']),
      );
      final result = signInHandler != null
          ? await signInHandler!(credential)
          : nextSignIn ??
                TokenPair(
                  accessToken: 'fake-access',
                  refreshToken: 'fake-refresh',
                  expiresIn: 900,
                  refreshExpiresIn: 3600,
                  scope: 'user',
                  userId: 'u_0000000000001',
                  isNewUser: false,
                );
      response = _signIn(result);
    } else if (path.contains('send') && path.endsWith('Code')) {
      final purpose = path.contains('Completion')
          ? CodePurpose.completion
          : path.contains('Reauthentication')
          ? CodePurpose.reauthentication
          : path.contains('Bind')
          ? CodePurpose.binding
          : CodePurpose.signIn;
      final challenge = codeHandler == null
          ? CodeChallenge.fromJson({
              'code_id': '0' * 32,
              'expire_time': clock
                  .now()
                  .add(const Duration(minutes: 5))
                  .toIso8601String(),
            })
          : await codeHandler!(
              purpose,
              text(data, 'channel'),
              text(data, 'target'),
            );
      response = {
        'code_id': challenge.codeId,
        'expire_time': challenge.expireTime.toIso8601String(),
      };
    } else if (path == '/token') {
      response = _token(
        TokenPair(
          accessToken: 'fake-refreshed-access',
          refreshToken: 'fake-rotated-refresh',
          expiresIn: 900,
          refreshExpiresIn: 3600,
          scope: 'user',
          userId: 'u_0000000000001',
          isNewUser: false,
        ),
      );
    } else if (path == '/revoke') {
      response = null;
    } else if (path.endsWith('/identities') && options.method == 'GET') {
      response = {'identities': <Object?>[]};
    } else if (path.endsWith('/sessions') && options.method == 'GET') {
      response = {'sessions': <Object?>[]};
    } else if (options.method == 'DELETE') {
      status = 204;
    } else if (path.endsWith('revokeOthers')) {
      response = <String, Object?>{};
    } else if (path.endsWith('/profile')) {
      response = {
        'name': 'users/u_0000000000001/profile',
        'profile_attributes': data['profile_attributes'] ?? <String, Object?>{},
      };
    } else if (path == '/users/me' || path == '/users/me:undelete') {
      response = {
        'name': 'users/u_0000000000001',
        'state': 'ACTIVE',
        'display_name': '',
        'avatar_url': '',
        'create_time': clock.now().toIso8601String(),
        'delete_time': null,
        'purge_time': null,
      };
    } else {
      throw const AccountProtocolError();
    }
    return ResponseBody.fromString(
      response == null ? '' : jsonEncode(response),
      status,
      headers: {
        'content-type': ['application/json'],
      },
    );
  }

  @override
  void close({bool force = false}) {}
}
