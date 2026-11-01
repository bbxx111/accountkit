import 'dart:async';

import 'package:dio/dio.dart';

import 'api.dart';
import 'authenticated_dio.dart';
import 'errors.dart';
import 'models.dart';
import 'platform.dart';
import 'session.dart';
import 'transport.dart';

class AccountClient implements AccountApi {
  AccountClient({
    required String baseUrl,
    String apiPrefix = '/v1',
    bool allowInsecureHttp = false,
    Dio? dio,
    TokenStore? tokenStore,
    DeviceProvider? deviceProvider,
    AccountClock clock = const SystemAccountClock(),
    Duration requestTimeout = const Duration(seconds: 30),
    this.syncBudget = const Duration(seconds: 5),
    this.refreshLeeway = const Duration(seconds: 30),
  }) : _transport = AccountTransport(
         endpoint: AccountEndpoint(
           baseUrl,
           apiPrefix,
           allowInsecureHttp: allowInsecureHttp,
         ),
         dio: dio,
         timeout: requestTimeout,
       ),
       _runtime = SessionRuntime(
         store: tokenStore ?? SecureTokenStore(),
         clock: clock,
       ),
       _devices = deviceProvider ?? PlatformDeviceProvider();
  final AccountTransport _transport;
  final SessionRuntime _runtime;
  final DeviceProvider _devices;
  final Duration syncBudget, refreshLeeway;
  bool _disposed = false;
  final List<Dio> _businessClients = [];
  @override
  SessionSnapshot get session => _runtime.snapshot;
  @override
  Stream<SessionSnapshot> get sessionChanges => _runtime.changes;
  @override
  Future<void> initialize() => _runtime.initialize();
  void _check(AccountRequestOptions? options) {
    if (_disposed) throw const AccountCancelledError();
    options?.check();
  }

  Future<DeviceSnapshot> deviceSnapshot() async {
    if (_disposed) throw const AccountCancelledError();
    final value = await _devices.snapshot();
    if (_disposed) throw const AccountCancelledError();
    return value;
  }

  Future<Object?> _anonymous(
    String path, {
    JsonObject? data,
    AccountRequestOptions? options,
    bool empty = false,
    DeviceSnapshot? device,
  }) {
    _check(options);
    return _transport.send(
      'POST',
      path,
      data: data,
      options: options,
      empty: empty,
      device: device,
    );
  }

  void _channel(String channel, String target) {
    if (!['EMAIL', 'PHONE'].contains(channel) || target.isEmpty)
      throw const AccountConfigurationError();
  }

  Future<CodeChallenge> _code(
    String path,
    String channel,
    String target, {
    String? ticket,
    AccountRequestOptions? options,
    bool authenticated = false,
  }) async {
    _channel(channel, target);
    final data = <String, Object?>{
      'channel': channel,
      'target': target,
      if (ticket != null) 'sign_in_completion_token': ticket,
    };
    final response = authenticated
        ? await _authorized('POST', path, data: data, options: options)
        : await _anonymous(
            path,
            data: data,
            options: options,
            device: options?.device,
          );
    return CodeChallenge.fromJson(object(response));
  }

  @override
  Future<CodeChallenge> sendSignInCode(
    String channel,
    String target, {
    AccountRequestOptions? options,
  }) => _code('/users:sendSignInCode', channel, target, options: options);
  @override
  Future<CodeChallenge> sendSignInCompletionCode(
    String ticket,
    String channel,
    String target, {
    AccountRequestOptions? options,
  }) => _code(
    '/users:sendSignInCompletionCode',
    channel,
    target,
    ticket: ticket,
    options: options,
  );
  Future<SignInResult> _signIn(
    String path,
    IdentityCredential credential, {
    String? ticket,
    AccountRequestOptions? options,
  }) async {
    _check(options);
    await initialize();
    _check(options);
    final e = _runtime.beginSignIn();
    final device = options?.device ?? await deviceSnapshot();
    _check(options);
    final result = SignInResult.fromJson(
      object(
        await _anonymous(
          path,
          data: {
            ...credential.toJson(),
            if (ticket != null) 'sign_in_completion_token': ticket,
          },
          options: options,
          device: device,
        ),
      ),
    );
    _check(options);
    if (e != _runtime.epoch) throw const AccountCancelledError();
    if (result is TokenPair)
      await _runtime.commitSignIn(
        result,
        e,
        isValid: () =>
            !_disposed &&
            !(options?.cancelToken?.isCancelled ?? false) &&
            (options?.isValid?.call() ?? true),
      );
    return result;
  }

  @override
  Future<SignInResult> signInWithCode(
    IdentityCredential credential, {
    AccountRequestOptions? options,
  }) {
    if (!credential.isCode) throw const AccountConfigurationError();
    return _signIn('/users:signInWithCode', credential, options: options);
  }

  @override
  Future<SignInResult> signInWithIdp(
    IdentityCredential credential, {
    AccountRequestOptions? options,
  }) {
    if (credential.isCode) throw const AccountConfigurationError();
    return _signIn('/users:signInWithIdp', credential, options: options);
  }

  @override
  Future<SignInResult> completeSignIn(
    String ticket,
    IdentityCredential credential, {
    AccountRequestOptions? options,
  }) {
    if (!credential.isCode || ticket.isEmpty)
      throw const AccountConfigurationError();
    return _signIn(
      '/users:completeSignIn',
      credential,
      ticket: ticket,
      options: options,
    );
  }

  Future<TokenRecord> _refresh() async {
    _check(null);
    await initialize();
    final old = _runtime.record;
    if (old == null) throw const AccountApiError(httpStatus: 401);
    return _runtime.refresh(
      () async => TokenPair.fromJson(
        object(
          await _anonymous(
            '/token',
            data: {
              'grant_type': 'refresh_token',
              'refresh_token': old.refreshToken,
            },
          ),
        ),
      ),
    );
  }

  @override
  Future<void> refreshToken({AccountRequestOptions? options}) async {
    _check(options);
    await _refresh();
    _check(options);
  }

  @override
  Future<void> revokeToken(
    String token, {
    AccountRequestOptions? options,
  }) async {
    if (token.isEmpty) throw const AccountConfigurationError();
    final e = _runtime.epoch;
    final matches = _runtime.record?.refreshToken == token;
    await _anonymous(
      '/revoke',
      data: {'token': token},
      options: options,
      empty: true,
    );
    if (matches) await _runtime.end(expectedEpoch: e);
  }

  @override
  Future<SignOutResult> signOut({bool revokeRemote = true}) async {
    final token = _runtime.record?.refreshToken;
    await _runtime.end();
    if (!revokeRemote || token == null) return const SignOutResult();
    try {
      await revokeToken(token);
      return const SignOutResult();
    } on AccountError catch (e) {
      return SignOutResult(remoteError: e);
    }
  }

  Future<Object?> _authorized(
    String method,
    String path, {
    Object? data,
    AccountRequestOptions? options,
    bool replay = false,
    bool empty = false,
    void Function(int epoch, int revision)? onDispatch,
  }) async {
    _check(options);
    await initialize();
    _check(options);
    var current = _runtime.record;
    if (current == null) throw const AccountApiError(httpStatus: 401);
    final e = _runtime.epoch;
    if (!current.accessExpireTime.isAfter(
      _runtime.clock.now().add(refreshLeeway),
    )) {
      current = await _refresh();
      _check(options);
      if (e != _runtime.epoch) throw const AccountCancelledError();
    }
    final revision = _runtime.revision;
    onDispatch?.call(e, revision);
    try {
      return await _transport.send(
        method,
        path,
        data: data,
        access: current.accessToken,
        options: options,
        empty: empty,
      );
    } on AccountApiError catch (error) {
      if (error.httpStatus != 401 || !replay) rethrow;
      _check(options);
      if (e != _runtime.epoch) throw const AccountCancelledError();
      if (revision == _runtime.revision) await _refresh();
      _check(options);
      if (e != _runtime.epoch || _runtime.record == null)
        throw const AccountCancelledError();
      try {
        return await _transport.send(
          method,
          path,
          data: data,
          access: _runtime.record!.accessToken,
          options: options,
          empty: empty,
        );
      } on AccountApiError catch (retryError) {
        if (retryError.httpStatus == 401 && e == _runtime.epoch)
          await _runtime.end(expectedEpoch: e);
        rethrow;
      }
    }
  }

  @override
  Future<AccountUser> getMe({AccountRequestOptions? options}) async =>
      AccountUser.fromJson(
        object(
          await _authorized('GET', '/users/me', options: options, replay: true),
        ),
      );
  @override
  Future<AccountUser> updateMe(
    BasicProfilePatch patch, {
    AccountRequestOptions? options,
  }) async => AccountUser.fromJson(
    object(
      await _authorized(
        'PATCH',
        '/users/me',
        data: patch.toJson(),
        options: options,
      ),
    ),
  );
  @override
  Future<AccountUser> deleteMe({AccountRequestOptions? options}) async {
    final e = _runtime.epoch;
    final result = AccountUser.fromJson(
      object(await _authorized('DELETE', '/users/me', options: options)),
    );
    await _runtime.end(expectedEpoch: e);
    return result;
  }

  Future<AccountMutationResult<T>> _sync<T>(
    T value,
    int e, {
    required bool refresh,
  }) async {
    if (e != _runtime.epoch)
      return AccountMutationResult(
        value,
        SessionSync.unconfirmed,
        syncError: const AccountCancelledError(),
      );
    final cancel = CancelToken();
    final options = AccountRequestOptions(cancelToken: cancel);
    try {
      if (refresh) {
        await refreshToken(options: options).timeout(syncBudget);
      } else {
        await getCurrentSession(options: options).timeout(syncBudget);
      }
      if (e != _runtime.epoch)
        return AccountMutationResult(
          value,
          _runtime.record == null
              ? SessionSync.sessionEnded
              : SessionSync.unconfirmed,
        );
      return AccountMutationResult(value, SessionSync.confirmed);
    } on TimeoutException {
      cancel.cancel();
      return AccountMutationResult(
        value,
        SessionSync.unconfirmed,
        syncError: const AccountNetworkError(timeout: true),
      );
    } on AccountError catch (error) {
      if (e != _runtime.epoch && _runtime.record == null)
        return AccountMutationResult(
          value,
          SessionSync.sessionEnded,
          syncError: error,
        );
      return AccountMutationResult(
        value,
        SessionSync.unconfirmed,
        syncError: error,
      );
    }
  }

  @override
  Future<AccountMutationResult<AccountUser>> undeleteMe({
    AccountRequestOptions? options,
  }) async {
    final e = _runtime.epoch;
    final result = AccountUser.fromJson(
      object(await _authorized('POST', '/users/me:undelete', options: options)),
    );
    return _sync(result, e, refresh: true);
  }

  @override
  Future<CodeChallenge> sendReauthenticationCode(
    String channel,
    String target, {
    AccountRequestOptions? options,
  }) => _code(
    '/users/me:sendReauthenticationCode',
    channel,
    target,
    options: options,
    authenticated: true,
  );
  @override
  Future<AccessTokenResult> reauthenticate(
    IdentityCredential credential, {
    AccountRequestOptions? options,
  }) async {
    if (!credential.isCode) throw const AccountConfigurationError();
    await initialize();
    late int e, r;
    final value = AccessTokenResult.fromJson(
      object(
        await _authorized(
          'POST',
          '/users/me:reauthenticate',
          data: credential.toJson(),
          options: options,
          onDispatch: (epoch, revision) {
            e = epoch;
            r = revision;
          },
        ),
      ),
    );
    await _runtime.commitAccess(
      value,
      e,
      r,
      isValid: () =>
          !_disposed &&
          !(options?.cancelToken?.isCancelled ?? false) &&
          (options?.isValid?.call() ?? true),
    );
    return value;
  }

  @override
  Future<List<AccountSession>> listSessions({
    AccountRequestOptions? options,
  }) async => items(
    object(
      await _authorized(
        'GET',
        '/users/me/sessions',
        options: options,
        replay: true,
      ),
    ),
    'sessions',
    AccountSession.fromJson,
  );
  @override
  Future<void> revokeSession(
    String session, {
    AccountRequestOptions? options,
  }) async {
    final id = resourceId(session, 'sessions', 's', this.session.userId);
    final e = _runtime.epoch;
    await _authorized(
      'DELETE',
      '/users/me/sessions/$id',
      options: options,
      empty: true,
    );
    await _sync(null, e, refresh: false);
  }

  @override
  Future<void> revokeOtherSessions({AccountRequestOptions? options}) async {
    object(
      await _authorized(
        'POST',
        '/users/me/sessions:revokeOthers',
        options: options,
      ),
    );
  }

  @override
  Future<CodeChallenge> sendBindCode(
    String channel,
    String target, {
    AccountRequestOptions? options,
  }) => _code(
    '/users/me:sendBindCode',
    channel,
    target,
    options: options,
    authenticated: true,
  );
  @override
  Future<List<AccountIdentity>> listIdentities({
    AccountRequestOptions? options,
  }) async => items(
    object(
      await _authorized(
        'GET',
        '/users/me/identities',
        options: options,
        replay: true,
      ),
    ),
    'identities',
    AccountIdentity.fromJson,
  );
  @override
  Future<AccountMutationResult<AccountIdentity>> bindIdentity(
    IdentityCredential credential, {
    AccountRequestOptions? options,
  }) async {
    final e = _runtime.epoch;
    final value = AccountIdentity.fromJson(
      object(
        await _authorized(
          'POST',
          '/users/me/identities',
          data: credential.toJson(),
          options: options,
        ),
      ),
    );
    return _sync(value, e, refresh: true);
  }

  @override
  Future<AccountMutationResult<void>> unbindIdentity(
    String identity, {
    AccountRequestOptions? options,
  }) async {
    final id = resourceId(identity, 'identities', 'i', session.userId);
    final e = _runtime.epoch;
    await _authorized(
      'DELETE',
      '/users/me/identities/$id',
      options: options,
      empty: true,
    );
    return _sync(null, e, refresh: false);
  }

  @override
  Future<AccountMutationResult<AccountIdentity>> replaceIdentity(
    String identity,
    IdentityCredential credential, {
    AccountRequestOptions? options,
  }) async {
    if (!credential.isCode) throw const AccountConfigurationError();
    final id = resourceId(identity, 'identities', 'i', session.userId);
    final e = _runtime.epoch;
    final value = AccountIdentity.fromJson(
      object(
        await _authorized(
          'POST',
          '/users/me/identities/$id:replace',
          data: credential.toJson(),
          options: options,
        ),
      ),
    );
    return _sync(value, e, refresh: false);
  }

  @override
  Future<CurrentSession> getCurrentSession({
    AccountRequestOptions? options,
  }) async => CurrentSession.fromJson(
    object(
      await _authorized(
        'GET',
        '/users/me/sessions/current',
        options: options,
        replay: true,
      ),
    ),
  );
  @override
  Future<AccountProfile> getMyProfile({AccountRequestOptions? options}) async =>
      AccountProfile.fromJson(
        object(
          await _authorized(
            'GET',
            '/users/me/profile',
            options: options,
            replay: true,
          ),
        ),
      );
  @override
  Future<AccountProfile> patchMyProfile(
    JsonObject patch, {
    AccountRequestOptions? options,
  }) async => AccountProfile.fromJson(
    object(
      await _authorized(
        'PATCH',
        '/users/me/profile',
        data: {'profile_attributes': copyJson(patch)},
        options: options,
      ),
    ),
  );

  /// Creates an owned Dio using the same session and refresh coordinator.
  Dio createAuthenticatedDio({
    required List<TrustedRequestScope> trustedScopes,
  }) {
    _check(null);
    if (trustedScopes.isEmpty) throw const AccountConfigurationError();
    final scopes = List<TrustedRequestScope>.unmodifiable(trustedScopes);
    final dio = Dio(
      BaseOptions(
        followRedirects: false,
        maxRedirects: 0,
        connectTimeout: _transport.timeout,
        receiveTimeout: _transport.timeout,
        sendTimeout: _transport.timeout,
      ),
    );
    const anonymous = [
      '/users:sendSignInCode',
      '/users:signInWithCode',
      '/users:signInWithIdp',
      '/users:sendSignInCompletionCode',
      '/users:completeSignIn',
      '/token',
      '/revoke',
    ];
    dio.interceptors.add(
      InterceptorsWrapper(
        onRequest: (request, handler) async {
          try {
            final options = AccountRequestOptions(
              cancelToken: request.cancelToken,
            );
            _check(options);
            if (!safeRequestPath(request.path) ||
                !scopes.any((s) => s.allows(request.uri)) ||
                request.uri.userInfo.isNotEmpty ||
                anonymous.any((p) {
                  final anonymousUri = Uri.parse(_transport.endpoint.url(p));
                  return request.uri.origin == anonymousUri.origin &&
                      request.uri.path == anonymousUri.path;
                }))
              throw const AccountConfigurationError();
            await initialize();
            _check(options);
            final e = _runtime.epoch;
            // Replays remain owned by the original session across every await.
            if (request.extra['accountkit.retried'] == true &&
                request.extra['accountkit.epoch'] != e)
              throw const AccountCancelledError();
            var record = _runtime.record;
            if (record == null) throw const AccountApiError(httpStatus: 401);
            if (!record.accessExpireTime.isAfter(
              _runtime.clock.now().add(refreshLeeway),
            ))
              record = await _refresh();
            _check(options);
            if (e != _runtime.epoch) throw const AccountCancelledError();
            request.headers = Map<String, Object?>.fromEntries(
              request.headers.entries.where(
                (entry) => ![
                  'authorization',
                  'x-device-id',
                  'x-device-name',
                ].contains(entry.key.toLowerCase()),
              ),
            );
            request.headers['Authorization'] = 'Bearer ${record.accessToken}';
            request.followRedirects = false;
            request.maxRedirects = 0;
            request.extra['accountkit.epoch'] = e;
            request.extra['accountkit.revision'] = _runtime.revision;
            handler.next(request);
          } on AccountError catch (error) {
            handler.reject(DioException(requestOptions: request, error: error));
          }
        },
        onError: (error, handler) async {
          final request = error.requestOptions;
          if (error.response?.statusCode != 401) {
            handler.next(error);
            return;
          }
          final e = request.extra['accountkit.epoch'];
          if (request.extra['accountkit.retried'] == true) {
            if (e == _runtime.epoch) {
              try {
                await _runtime.end(expectedEpoch: e as int);
              } on AccountError catch (storage) {
                handler.reject(
                  DioException(requestOptions: request, error: storage),
                );
                return;
              }
            }
            handler.next(error);
            return;
          }
          try {
            _check(AccountRequestOptions(cancelToken: request.cancelToken));
            if (e != _runtime.epoch) throw const AccountCancelledError();
            final replay = request.extra[AuthenticatedReplay.key];
            final safe =
                ['GET', 'HEAD'].contains(request.method.toUpperCase()) &&
                request.data == null;
            if (!safe && replay is! AuthenticatedReplay)
              throw const AccountReplayRequiredError();
            if (request.extra['accountkit.revision'] == _runtime.revision)
              await _refresh();
            _check(AccountRequestOptions(cancelToken: request.cancelToken));
            if (e != _runtime.epoch) throw const AccountCancelledError();
            Object? body = request.data;
            if (replay is AuthenticatedReplay) {
              body = await replay.rebuildBody();
              if ((body is Stream || body is FormData) &&
                  identical(body, request.data))
                throw const AccountReplayRequiredError();
            }
            _check(AccountRequestOptions(cancelToken: request.cancelToken));
            if (e != _runtime.epoch) throw const AccountCancelledError();
            final response = await dio.fetch<Object?>(
              request.copyWith(
                data: body,
                extra: {...request.extra, 'accountkit.retried': true},
              ),
            );
            handler.resolve(response);
          } on AccountError catch (failure) {
            handler.reject(
              DioException(requestOptions: request, error: failure),
            );
          } on DioException catch (failure) {
            handler.next(failure);
          }
        },
      ),
    );
    _businessClients.add(dio);
    return dio;
  }

  @override
  Future<void> dispose() async {
    if (_disposed) return;
    _disposed = true;
    for (final dio in _businessClients) {
      dio.close(force: true);
    }
    _transport.dispose();
    await _runtime.dispose();
  }
}
