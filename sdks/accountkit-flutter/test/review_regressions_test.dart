import 'dart:async';

import 'package:accountkit_flutter/accountkit_flutter.dart';
import 'package:accountkit_flutter/testing.dart';
import 'package:dio/dio.dart';
import 'package:flutter_test/flutter_test.dart';

import 'failures_test.dart' show ScriptAdapter, buildClient, login;
import 'protocol_test.dart' show token;

final credential = IdentityCredential.email(
  target: 'x@example.test',
  codeId: 'a' * 32,
  code: '123456',
);

void main() {
  test(
    'R1 reauthentication following its own proactive refresh must succeed',
    () async {
      final clock = MutableAccountClock(DateTime.parse('2030-01-01T00:00:00Z'));
      final store = MemoryTokenStore();
      final wire = ScriptAdapter((r) {
        if (r.uri.path.endsWith(':reauthenticate'))
          return (
            200,
            {
              'access_token': 'reauth-access',
              'token_type': 'Bearer',
              'expires_in': 900,
              'scope': 'user',
              'user_id': token['user_id'],
              'is_new_user': false,
            },
          );
        return (200, token);
      });
      final client = buildClient(wire, clock: clock, store: store);
      addTearDown(client.dispose);
      await login(client);
      clock.advance(const Duration(minutes: 15));
      await client.reauthenticate(credential);
      expect(store.record!.accessToken, 'reauth-access');
    },
  );

  test('R2 delayed write body rebuild must not replay across account switch', () async {
    var nextToken = token;
    final wire = ScriptAdapter((_) => (200, nextToken));
    final client = buildClient(wire);
    addTearDown(client.dispose);
    await login(client);
    final dio = client.createAuthenticatedDio(
      trustedScopes: [
        TrustedRequestScope(origin: 'https://business.test', pathPrefix: '/v1'),
      ],
    );
    var requests = 0;
    final target = ScriptAdapter(
      (_) => ++requests == 1 ? (401, <String, Object?>{}) : (200, {'ok': true}),
    );
    dio.httpClientAdapter = target;
    final entered = Completer<void>();
    final resume = Completer<Object?>();
    final call = dio.post<Object?>(
      'https://business.test/v1/write',
      data: {'owner': 'A'},
      options: Options(
        extra: {
          AuthenticatedReplay.key: AuthenticatedReplay(
            rebuildBody: () {
              entered.complete();
              return resume.future;
            },
          ),
        },
      ),
    );
    await entered.future;
    nextToken = {
      ...token,
      'access_token': 'account-B-access',
      'refresh_token': 'account-B-refresh',
      'user_id': 'u_0000000000002',
    };
    await login(client);
    resume.complete({'owner': 'A'});
    Object? error;
    try {
      await call;
    } catch (e) {
      error = e;
    }
    expect(
      target.requests.length,
      1,
      reason:
          'old request must not use B bearer; actual headers: ${target.requests.map((r) => r.headers["Authorization"]).toList()}',
    );
    expect(error, isA<DioException>());
  });

  test(
    'R3 injected Dio default bearer must not enter anonymous endpoint',
    () async {
      final wire = ScriptAdapter((_) => (200, token));
      final dio = Dio(
        BaseOptions(headers: {'Authorization': 'Bearer inherited'}),
      )..httpClientAdapter = wire;
      final client = AccountClient(
        baseUrl: 'https://account.test',
        dio: dio,
        tokenStore: MemoryTokenStore(),
        deviceProvider: FixedDeviceProvider(const DeviceSnapshot(id: 'd')),
      );
      addTearDown(client.dispose);
      await login(client);
      expect(
        wire.requests.single.headers.keys.where(
          (k) => k.toLowerCase() == 'authorization',
        ),
        isEmpty,
      );
    },
  );

  test(
    'R4 initialization can recover from a transient store failure',
    () async {
      final store = MemoryTokenStore()..readFailure = true;
      final runtime = SessionRuntime(store: store);
      addTearDown(runtime.dispose);
      await expectLater(
        runtime.initialize(),
        throwsA(isA<AccountStorageError>()),
      );
      store.readFailure = false;
      await runtime.initialize();
      expect(runtime.snapshot.initialized, isTrue);
    },
  );

  test('R5 concurrent reauthentication must not discard a rotated refresh token', () async {
    final store = MemoryTokenStore();
    final runtime = SessionRuntime(store: store);
    addTearDown(runtime.dispose);
    await runtime.initialize();
    await runtime.commitSignIn(
      TokenPair.fromJson(token),
      runtime.beginSignIn(),
    );
    final gate = Completer<TokenPair>();
    final refresh = runtime.refresh(() => gate.future);
    await runtime.commitAccess(
      AccessTokenResult.fromJson({
        'access_token': 'reauth-access',
        'token_type': 'Bearer',
        'expires_in': 900,
        'scope': 'user',
        'user_id': token['user_id'],
      }),
      runtime.epoch,
      runtime.revision,
    );
    gate.complete(
      TokenPair.fromJson({
        ...token,
        'access_token': 'rotated-access',
        'refresh_token': 'rotated-refresh',
      }),
    );
    await refresh;
    expect(store.record!.refreshToken, 'rotated-refresh');
    expect(
      store.record!.accessToken,
      'reauth-access',
      reason:
          'an older rotation access must not overwrite newer reauthentication',
    );
  });

  test(
    'R6 completion delayed beyond injected deadline must not persist tokens',
    () async {
      final clock = MutableAccountClock(DateTime.parse('2030-01-01T00:00:00Z'));
      final store = MemoryTokenStore();
      final api = FakeAccountApi(clock: clock, tokenStore: store);
      final controller = AccountController(
        api: api,
        clock: clock,
        deviceProvider: FixedDeviceProvider(const DeviceSnapshot(id: 'd')),
      );
      addTearDown(() async {
        controller.dispose();
        await api.dispose();
      });
      await controller.initialize();
      api.nextSignIn = PendingSignIn.fromJson({
        'next_action': 'COMPLETE_IDENTITY',
        'sign_in_completion_token': 'ticket',
        'expire_time': '2030-01-01T00:05:00Z',
        'enabled_identity_kinds': ['EMAIL'],
        'required_identity_kinds': ['EMAIL'],
      });
      await controller.signInWithIdp(
        IdentityCredential.custom('ACME', {'p': 'x'}),
      );
      final entered = Completer<void>();
      final complete = Completer<SignInResult>();
      api.signInHandler = (_) {
        entered.complete();
        return complete.future;
      };
      final call = controller.completeSignIn(credential);
      await entered.future;
      clock.advance(const Duration(minutes: 6));
      complete.complete(TokenPair.fromJson(token));
      Object? error;
      try {
        await call;
      } catch (e) {
        error = e;
      }
      expect(
        store.record,
        isNull,
        reason: 'late completion must not persist credentials after deadline',
      );
      expect(error, isA<AccountCancelledError>());
    },
  );

  test(
    'R3 injected interceptors cannot add bearer or retry account requests',
    () async {
      final wire = ScriptAdapter((_) => (200, token));
      var interceptorCalls = 0;
      final source = Dio(
        BaseOptions(
          headers: {
            'authorization': 'Bearer inherited',
            'X-Device-Id': 'inherited-device',
          },
        ),
      )..httpClientAdapter = wire;
      source.interceptors.add(
        InterceptorsWrapper(
          onRequest: (request, handler) {
            interceptorCalls++;
            request.headers['Authorization'] = 'Bearer interceptor';
            handler.next(request);
          },
        ),
      );
      final client = AccountClient(
        baseUrl: 'https://account.test',
        dio: source,
        tokenStore: MemoryTokenStore(),
        deviceProvider: FixedDeviceProvider(
          const DeviceSnapshot(id: 'own-device'),
        ),
      );
      addTearDown(client.dispose);
      await login(client);
      expect(interceptorCalls, 0);
      expect(
        wire.requests.single.headers.keys.where(
          (k) => k.toLowerCase() == 'authorization',
        ),
        isEmpty,
      );
      expect(wire.requests.single.headers['X-Device-Id'], 'own-device');
    },
  );

  test('R4 initialize invalidated by signOut can initialize again', () async {
    final store = DelayedReadStore();
    final runtime = SessionRuntime(store: store);
    addTearDown(runtime.dispose);
    final initialization = runtime.initialize();
    await store.entered.future;
    final ending = runtime.end();
    store.gate.complete(null);
    await expectLater(initialization, throwsA(isA<AccountCancelledError>()));
    await ending;
    await runtime.initialize();
    expect(runtime.snapshot.initialized, true);
    expect(runtime.snapshot.state, isA<SignedOut>());
  });

  test(
    'R6 expiry during pending credential storage rolls back the stale write',
    () async {
      final clock = MutableAccountClock(DateTime.parse('2030-01-01T00:00:00Z'));
      final store = MemoryTokenStore();
      final api = FakeAccountApi(clock: clock, tokenStore: store);
      final c = AccountController(
        api: api,
        clock: clock,
        deviceProvider: FixedDeviceProvider(const DeviceSnapshot(id: 'd')),
      );
      addTearDown(() async {
        c.dispose();
        await api.dispose();
      });
      await c.initialize();
      api.nextSignIn = PendingSignIn.fromJson({
        'next_action': 'COMPLETE_IDENTITY',
        'sign_in_completion_token': 'ticket',
        'expire_time': '2030-01-01T00:05:00Z',
        'enabled_identity_kinds': ['EMAIL'],
        'required_identity_kinds': ['EMAIL'],
      });
      await c.signInWithIdp(IdentityCredential.custom('ACME', {'p': 'x'}));
      api.nextSignIn = TokenPair.fromJson(token);
      final entered = Completer<void>(), resume = Completer<void>();
      store.beforeWrite = () {
        entered.complete();
        return resume.future;
      };
      final completion = c.completeSignIn(credential);
      await entered.future;
      clock.advance(const Duration(minutes: 6));
      resume.complete();
      await expectLater(completion, throwsA(isA<AccountCancelledError>()));
      expect(store.record, isNull);
      expect(api.session.state, isA<SignedOut>());
    },
  );
}

class DelayedReadStore implements TokenStore {
  final entered = Completer<void>(), gate = Completer<TokenRecord?>();
  @override
  Future<TokenRecord?> read() {
    entered.complete();
    return gate.future;
  }

  @override
  Future<void> write(TokenRecord record) async {}
  @override
  Future<void> clear() async {}
}
