import 'dart:async';
import 'dart:convert';
import 'dart:typed_data';

import 'package:accountkit_flutter/accountkit_flutter.dart';
import 'package:accountkit_flutter/testing.dart';
import 'package:dio/dio.dart';
import 'package:flutter_test/flutter_test.dart';

import 'protocol_test.dart' show token, me, session, identity;

class ScriptAdapter implements HttpClientAdapter {
  ScriptAdapter(this.respond);
  final FutureOr<(int, Object?)> Function(RequestOptions) respond;
  final requests = <RequestOptions>[];
  @override
  Future<ResponseBody> fetch(
    RequestOptions o,
    Stream<Uint8List>? stream,
    Future<void>? cancel,
  ) async {
    requests.add(o);
    final (status, body) = await respond(o);
    return ResponseBody.fromString(
      body is String ? body : jsonEncode(body),
      status,
      headers: {
        'content-type': ['application/json'],
        'www-authenticate': ['Bearer'],
        'retry-after': ['7'],
        'x-request-id': ['server-id'],
      },
    );
  }

  @override
  void close({bool force = false}) {}
}

AccountClient buildClient(
  ScriptAdapter wire, {
  MemoryTokenStore? store,
  MutableAccountClock? clock,
}) => AccountClient(
  baseUrl: 'https://account.test',
  dio: Dio()..httpClientAdapter = wire,
  tokenStore: store ?? MemoryTokenStore(),
  clock: clock ?? MutableAccountClock(DateTime.parse('2030-01-01T00:00:00Z')),
  deviceProvider: FixedDeviceProvider(const DeviceSnapshot(id: 'device')),
  syncBudget: const Duration(milliseconds: 30),
);
Future<void> login(AccountClient c) =>
    c.signInWithIdp(IdentityCredential.custom('ACME', {'proof': 'secret'}));
void main() {
  test('direct revokeToken ends matching session after success', () async {
    final wire = ScriptAdapter(
      (r) => r.uri.path.endsWith('signInWithIdp') ? (200, token) : (200, ''),
    );
    final c = buildClient(wire);
    await login(c);
    await c.revokeToken('refresh');
    expect(c.session.state, isA<SignedOut>());
    await c.dispose();
  });
  test('invalid built-in custom credential and invalid RFC3339 rejected', () {
    expect(
      () => IdentityCredential.custom('FACEBOOK', {
        'id_token': 'x',
        'nonce': 'n',
        'access_token': 'a',
      }),
      throwsA(isA<AccountConfigurationError>()),
    );
    expect(
      () => CodeChallenge.fromJson({
        'code_id': 'a' * 32,
        'expire_time': '2030-02-31T00:00:00Z',
      }),
      throwsA(isA<AccountProtocolError>()),
    );
  });
  test(
    'AIP and OAuth errors preserve classification but never print body',
    () async {
      final wire = ScriptAdapter(
        (r) => r.uri.path.endsWith('signInWithIdp')
            ? (200, token)
            : (
                503,
                {
                  'error': {
                    'code': 503,
                    'status': 'UNAVAILABLE',
                    'reason': 'SESSION_QUERY_NOT_CONFIGURED',
                    'message': 'secret',
                  },
                },
              ),
      );
      final c = buildClient(wire);
      await login(c);
      try {
        await c.getCurrentSession();
        fail('error expected');
      } on AccountApiError catch (e) {
        expect(e.reason, 'SESSION_QUERY_NOT_CONFIGURED');
        expect(e.requestId, 'server-id');
        expect(e.retryAfterSeconds, 7);
        expect(e.toString(), isNot(contains('secret')));
      }
      await c.dispose();
    },
  );
  test('concurrent 401 shares one refresh and retries once', () async {
    var refreshes = 0;
    final gate = Completer<void>();
    final wire = ScriptAdapter((r) async {
      if (r.uri.path.endsWith('signInWithIdp')) return (200, token);
      if (r.uri.path.endsWith('/token')) {
        refreshes++;
        await gate.future;
        return (
          200,
          {...token, 'access_token': 'new', 'refresh_token': 'new-r'},
        );
      }
      return r.headers['Authorization'] == 'Bearer new'
          ? (200, me)
          : (
              401,
              {
                'error': {'reason': 'TOKEN_INVALID'},
              },
            );
    });
    final c = buildClient(wire);
    await login(c);
    final a = c.getMe(), b = c.getMe();
    await Future<void>.delayed(const Duration(milliseconds: 10));
    gate.complete();
    await Future.wait([a, b]);
    expect(refreshes, 1);
    expect(
      wire.requests.where((r) => r.uri.path.endsWith('/users/me')).length,
      4,
    );
    await c.dispose();
  });
  test(
    'ordinary 403 and reauthentication required do not refresh or sign out',
    () async {
      final wire = ScriptAdapter(
        (r) => r.uri.path.endsWith('signInWithIdp')
            ? (200, token)
            : (
                403,
                {
                  'error': {'reason': 'INSUFFICIENT_SCOPE'},
                },
              ),
      );
      final c = buildClient(wire);
      await login(c);
      await expectLater(c.getMe(), throwsA(isA<AccountApiError>()));
      expect(c.session.state, isA<SignedIn>());
      expect(wire.requests.length, 2);
      await c.dispose();
    },
  );
  test('invalid_grant ends original session and temporary refresh error retains it', () async {
    var invalid = false;
    final wire = ScriptAdapter(
      (r) => r.uri.path.endsWith('signInWithIdp')
          ? (200, token)
          : invalid
          ? (403, {'error': 'invalid_grant', 'reason': 'USER_FROZEN'})
          : (503, {'error': 'temporarily_unavailable'}),
    );
    final c = buildClient(wire);
    await login(c);
    await expectLater(c.refreshToken(), throwsA(isA<AccountApiError>()));
    expect(c.session.state, isA<SignedIn>());
    invalid = true;
    await expectLater(c.refreshToken(), throwsA(isA<AccountApiError>()));
    expect(c.session.state, isA<SignedOut>());
    await c.dispose();
  });
  test(
    'mutation succeeds even if confirmation times out and writes only once',
    () async {
      final gate = Completer<(int, Object?)>();
      final wire = ScriptAdapter((r) {
        if (r.uri.path.endsWith('signInWithIdp')) return (200, token);
        if (r.method == 'POST' && r.uri.path.endsWith(':replace'))
          return (200, identity);
        return gate.future;
      });
      final c = buildClient(wire);
      await login(c);
      final result = await c.replaceIdentity(
        'i_0000000000001',
        IdentityCredential.email(
          target: 'x@example.test',
          codeId: 'a' * 32,
          code: '123456',
        ),
      );
      expect(result.sessionSync, SessionSync.unconfirmed);
      expect(result.value.kind, 'CUSTOM');
      expect(
        wire.requests
            .where((r) => r.method == 'POST' && r.uri.path.endsWith(':replace'))
            .length,
        1,
      );
      gate.complete((200, session));
      await c.dispose();
    },
  );
  test('logout clears local state before remote revoke failure', () async {
    final store = MemoryTokenStore();
    final wire = ScriptAdapter(
      (r) => r.uri.path.endsWith('signInWithIdp')
          ? (200, token)
          : (503, {'error': 'temporarily_unavailable'}),
    );
    final c = buildClient(wire, store: store);
    await login(c);
    final result = await c.signOut();
    expect(result.remoteError, isA<AccountApiError>());
    expect(store.record, isNull);
    expect(c.session.state, isA<SignedOut>());
    await c.dispose();
  });
  test('cancel while refresh waits sends no business request', () async {
    final gate = Completer<(int, Object?)>();
    final clock = MutableAccountClock(DateTime.parse('2030-01-01T00:00:00Z'));
    final wire = ScriptAdapter(
      (r) => r.uri.path.endsWith('signInWithIdp') ? (200, token) : gate.future,
    );
    final c = buildClient(wire, clock: clock);
    await login(c);
    clock.advance(const Duration(minutes: 15));
    final cancel = CancelToken();
    final call = c.getMe(options: AccountRequestOptions(cancelToken: cancel));
    await Future<void>.delayed(Duration.zero);
    cancel.cancel();
    gate.complete((200, token));
    await expectLater(call, throwsA(isA<AccountCancelledError>()));
    expect(
      wire.requests.where((r) => r.uri.path.endsWith('/users/me')),
      isEmpty,
    );
    await c.dispose();
  });
  test('late storage write cannot resurrect cancelled login', () async {
    final store = MemoryTokenStore();
    final gate = Completer<void>();
    store.beforeWrite = () => gate.future;
    final runtime = SessionRuntime(store: store);
    await runtime.initialize();
    final old = runtime.commitSignIn(
      TokenPair.fromJson(token),
      runtime.beginSignIn(),
    );
    await Future<void>.delayed(Duration.zero);
    runtime.beginSignIn();
    gate.complete();
    await expectLater(old, throwsA(isA<AccountCancelledError>()));
    expect(store.record, isNull);
    await runtime.dispose();
  });
}
