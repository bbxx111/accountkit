import 'dart:async';

import 'errors.dart';
import 'models.dart';

abstract interface class AccountClock {
  DateTime now();
}

final class SystemAccountClock implements AccountClock {
  const SystemAccountClock();
  @override
  DateTime now() => DateTime.now().toUtc();
}

abstract interface class TokenStore {
  Future<TokenRecord?> read();
  Future<void> write(TokenRecord record);
  Future<void> clear();
}

final class TokenRecord {
  TokenRecord({
    required this.accessToken,
    required this.refreshToken,
    required this.scope,
    required this.userId,
    required this.accessExpireTime,
    required this.refreshExpireTime,
  }) {
    authenticationStateForScope(scope);
    if (accessToken.isEmpty || refreshToken.isEmpty || userId.isEmpty)
      throw const AccountProtocolError();
  }
  factory TokenRecord.fromPair(TokenPair p, DateTime now) => TokenRecord(
    accessToken: p.accessToken,
    refreshToken: p.refreshToken,
    scope: p.scope,
    userId: p.userId,
    accessExpireTime: now.add(Duration(seconds: p.expiresIn)),
    refreshExpireTime: now.add(Duration(seconds: p.refreshExpiresIn)),
  );
  factory TokenRecord.fromJson(JsonObject j) => TokenRecord(
    accessToken: text(j, 'access_token'),
    refreshToken: text(j, 'refresh_token'),
    scope: text(j, 'scope'),
    userId: text(j, 'user_id'),
    accessExpireTime: time(j, 'access_expire_time'),
    refreshExpireTime: time(j, 'refresh_expire_time'),
  );
  final String accessToken, refreshToken, scope, userId;
  final DateTime accessExpireTime, refreshExpireTime;
  JsonObject toJson() => {
    'access_token': accessToken,
    'refresh_token': refreshToken,
    'scope': scope,
    'user_id': userId,
    'access_expire_time': accessExpireTime.toUtc().toIso8601String(),
    'refresh_expire_time': refreshExpireTime.toUtc().toIso8601String(),
  };
  @override
  String toString() => 'TokenRecord(redacted)';
}

final class SessionSnapshot {
  const SessionSnapshot({
    required this.initialized,
    required this.state,
    required this.epoch,
    required this.revision,
    this.userId,
    this.accessExpireTime,
  });
  final bool initialized;
  final AuthenticationState state;
  final int epoch, revision;
  final String? userId;
  final DateTime? accessExpireTime;
}

/// Internal single owner. Exposed only through testing.dart for deterministic tests.
final class SessionRuntime {
  SessionRuntime({
    required this.store,
    this.clock = const SystemAccountClock(),
  });
  final TokenStore store;
  final AccountClock clock;
  TokenRecord? _record;
  TokenRecord? get record => _record;
  int epoch = 0, revision = 0;
  int _pairRevision = 0;
  bool _initialized = false, _disposed = false;
  Future<void> _writes = Future.value();
  Future<void>? _initializing;
  Future<TokenRecord>? _refreshing;
  final _events = StreamController<SessionSnapshot>.broadcast(sync: true);
  Stream<SessionSnapshot> get changes => _events.stream;
  SessionSnapshot get snapshot => SessionSnapshot(
    initialized: _initialized,
    state: _record == null
        ? const SignedOut()
        : authenticationStateForScope(_record!.scope),
    epoch: epoch,
    revision: revision,
    userId: _record?.userId,
    accessExpireTime: _record?.accessExpireTime,
  );
  void _check(int e, [int? r]) {
    if (_disposed || e != epoch || (r != null && r != revision))
      throw const AccountCancelledError();
  }

  void _emit() {
    if (!_disposed) _events.add(snapshot);
  }

  Future<void> _enqueue(Future<void> Function() operation) {
    final next = _writes.then((_) => operation());
    _writes = next.catchError((Object _) {});
    return next;
  }

  Future<void> initialize() {
    if (_disposed) return Future.error(const AccountCancelledError());
    if (_initialized) return Future.value();
    final existing = _initializing;
    if (existing != null) return existing;
    late final Future<void> attempt;
    attempt =
        _enqueue(() async {
          if (_initialized) return;
          final e = epoch;
          TokenRecord? loaded;
          try {
            loaded = await store.read();
          } on AccountProtocolError {
            try {
              await store.clear();
            } catch (_) {
              throw const AccountStorageError();
            }
          } catch (_) {
            throw const AccountStorageError();
          }
          _check(e);
          if (loaded != null &&
              !loaded.refreshExpireTime.isAfter(clock.now())) {
            try {
              await store.clear();
            } catch (_) {
              throw const AccountStorageError();
            }
            loaded = null;
          }
          _record = loaded;
          _initialized = true;
          _emit();
        }).whenComplete(() {
          if (identical(_initializing, attempt)) _initializing = null;
        });
    _initializing = attempt;
    return attempt;
  }

  int beginSignIn() {
    if (_disposed) throw const AccountCancelledError();
    epoch++;
    _refreshing = null;
    return epoch;
  }

  Future<void> commitSignIn(
    TokenPair pair,
    int e, {
    bool Function()? isValid,
  }) => _commit(
    TokenRecord.fromPair(pair, clock.now()),
    e,
    isValid: isValid,
    rotatesPair: true,
  );
  Future<void> _commit(
    TokenRecord record,
    int e, {
    int? expectedRevision,
    int? expectedPairRevision,
    bool rotatesPair = false,
    TokenRecord Function()? resolveRecord,
    bool Function()? isValid,
  }) => _enqueue(() async {
    _check(e, expectedRevision);
    if (expectedPairRevision != null && expectedPairRevision != _pairRevision)
      throw const AccountCancelledError();
    if (isValid != null && !isValid()) throw const AccountCancelledError();
    final committed = resolveRecord?.call() ?? record;
    try {
      await store.write(committed);
    } catch (_) {
      throw const AccountStorageError();
    }
    // A newer clear/write is queued behind this write. Never publish stale data.
    if (_disposed ||
        e != epoch ||
        (expectedRevision != null && expectedRevision != revision) ||
        (isValid != null && !isValid())) {
      try {
        if (_record == null) {
          await store.clear();
        } else {
          await store.write(_record!);
        }
      } catch (_) {
        throw const AccountStorageError();
      }
      throw const AccountCancelledError();
    }
    _record = committed;
    if (rotatesPair) _pairRevision++;
    revision++;
    _initialized = true;
    _emit();
  });
  Future<void> commitAccess(
    AccessTokenResult access,
    int e,
    int r, {
    bool Function()? isValid,
  }) async {
    _check(e, r);
    final old = _record;
    if (old == null || old.userId != access.userId)
      throw const AccountProtocolError();
    await _commit(
      TokenRecord(
        accessToken: access.accessToken,
        refreshToken: old.refreshToken,
        scope: access.scope,
        userId: access.userId,
        accessExpireTime: clock.now().add(Duration(seconds: access.expiresIn)),
        refreshExpireTime: old.refreshExpireTime,
      ),
      e,
      expectedRevision: r,
      isValid: isValid,
    );
  }

  Future<TokenRecord> refresh(Future<TokenPair> Function() load) {
    final existing = _refreshing;
    if (existing != null) return existing;
    final e = epoch, r = revision, pairRevision = _pairRevision;
    final old = _record;
    if (old == null)
      return Future.error(const AccountApiError(httpStatus: 401));
    late final Future<TokenRecord> current;
    current = (() async {
      try {
        final pair = await load();
        _check(e);
        if (pairRevision != _pairRevision) throw const AccountCancelledError();
        if (pair.userId != old.userId) throw const AccountProtocolError();
        final rotated = TokenRecord.fromPair(pair, clock.now());
        await _commit(
          rotated,
          e,
          expectedPairRevision: pairRevision,
          rotatesPair: true,
          resolveRecord: () {
            if (revision == r) return rotated;
            // An access-only reauthentication may commit while rotation waits.
            // Keep its newer access but always persist the rotated refresh.
            final latest = _record!;
            return TokenRecord(
              accessToken: latest.accessToken,
              refreshToken: rotated.refreshToken,
              scope: latest.scope,
              userId: latest.userId,
              accessExpireTime: latest.accessExpireTime,
              refreshExpireTime: rotated.refreshExpireTime,
            );
          },
        );
        return _record!;
      } on AccountApiError catch (error) {
        if (error.oauthError == 'invalid_grant' && e == epoch)
          await end(expectedEpoch: e);
        rethrow;
      } finally {
        if (identical(_refreshing, current)) _refreshing = null;
      }
    })();
    _refreshing = current;
    return current;
  }

  Future<void> end({int? expectedEpoch}) {
    if (expectedEpoch != null && expectedEpoch != epoch) return Future.value();
    if (_disposed) return Future.error(const AccountCancelledError());
    final e = ++epoch;
    _refreshing = null;
    return _enqueue(() async {
      _check(e);
      try {
        await store.clear();
      } catch (_) {
        throw const AccountStorageError();
      }
      _check(e);
      _record = null;
      revision++;
      _initialized = true;
      _emit();
    });
  }

  Future<void> dispose() async {
    if (_disposed) return;
    _disposed = true;
    epoch++;
    await _events.close();
  }
}
