import 'package:accountkit_flutter/accountkit_flutter.dart';
import 'package:accountkit_flutter/testing.dart';
import 'package:dio/dio.dart';
import 'package:flutter_test/flutter_test.dart';

import 'protocol_test.dart' show WireAdapter, token;
import 'failures_test.dart' show ScriptAdapter;

void main() {
  test('one shot stream is never automatically replayed', () async {
    final wire = ScriptAdapter((_) => (200, token));
    final c = AccountClient(
      baseUrl: 'https://account.test',
      dio: Dio()..httpClientAdapter = wire,
      tokenStore: MemoryTokenStore(),
      deviceProvider: FixedDeviceProvider(const DeviceSnapshot(id: 'd')),
    );
    await c.signInWithIdp(IdentityCredential.custom('ACME', {'p': 'x'}));
    final business = c.createAuthenticatedDio(
      trustedScopes: [
        TrustedRequestScope(origin: 'https://business.test', pathPrefix: '/v1'),
      ],
    );
    final server = ScriptAdapter((_) => (401, {'error': 'denied'}));
    business.httpClientAdapter = server;
    await expectLater(
      business.post<Object?>(
        'https://business.test/v1/upload',
        data: Stream<List<int>>.fromIterable([
          [1, 2, 3],
        ]),
      ),
      throwsA(
        isA<DioException>().having(
          (e) => e.error,
          'typed replay error',
          isA<AccountReplayRequiredError>(),
        ),
      ),
    );
    expect(server.requests.length, 1);
    await c.dispose();
  });
  test(
    'explicit write replay rebuilds body once after authentication failure',
    () async {
      final wire = ScriptAdapter((_) => (200, token));
      final c = AccountClient(
        baseUrl: 'https://account.test',
        dio: Dio()..httpClientAdapter = wire,
        tokenStore: MemoryTokenStore(),
        deviceProvider: FixedDeviceProvider(const DeviceSnapshot(id: 'd')),
      );
      await c.signInWithIdp(IdentityCredential.custom('ACME', {'p': 'x'}));
      final business = c.createAuthenticatedDio(
        trustedScopes: [
          TrustedRequestScope(
            origin: 'https://business.test',
            pathPrefix: '/v1',
          ),
        ],
      );
      var count = 0, rebuilds = 0;
      final server = ScriptAdapter(
        (_) => ++count == 1 ? (401, <String, Object?>{}) : (200, {'ok': true}),
      );
      business.httpClientAdapter = server;
      await business.post<Object?>(
        'https://business.test/v1/x',
        data: {'x': 1},
        options: Options(
          extra: {
            AuthenticatedReplay.key: AuthenticatedReplay(
              rebuildBody: () {
                rebuilds++;
                return {'x': 1};
              },
            ),
          },
        ),
      );
      expect(server.requests.length, 2);
      expect(rebuilds, 1);
      await c.dispose();
    },
  );
  test(
    'GET retries once and a second 401 ends only its original session',
    () async {
      var refreshes = 0;
      final wire = ScriptAdapter((r) {
        if (r.uri.path.endsWith('/token')) refreshes++;
        return (200, token);
      });
      final c = AccountClient(
        baseUrl: 'https://account.test',
        dio: Dio()..httpClientAdapter = wire,
        tokenStore: MemoryTokenStore(),
        deviceProvider: FixedDeviceProvider(const DeviceSnapshot(id: 'd')),
      );
      await c.initialize();
      await c.signInWithIdp(IdentityCredential.custom('ACME', {'p': 'x'}));
      final business = c.createAuthenticatedDio(
        trustedScopes: [
          TrustedRequestScope(
            origin: 'https://business.test',
            pathPrefix: '/v1',
          ),
        ],
      );
      final denied = ScriptAdapter((_) => (401, {'error': 'denied'}));
      business.httpClientAdapter = denied;
      await expectLater(
        business.get<Object?>('https://business.test/v1/x'),
        throwsA(isA<DioException>()),
      );
      expect(denied.requests.length, 2);
      expect(refreshes, 1);
      expect(c.session.state, isA<SignedOut>());
      await c.dispose();
    },
  );
  test(
    'writes require rebuild contract and network 5xx never replay',
    () async {
      final wire = ScriptAdapter((_) => (200, token));
      final c = AccountClient(
        baseUrl: 'https://account.test',
        dio: Dio()..httpClientAdapter = wire,
        tokenStore: MemoryTokenStore(),
        deviceProvider: FixedDeviceProvider(const DeviceSnapshot(id: 'd')),
      );
      await c.signInWithIdp(IdentityCredential.custom('ACME', {'p': 'x'}));
      final business = c.createAuthenticatedDio(
        trustedScopes: [
          TrustedRequestScope(
            origin: 'https://business.test',
            pathPrefix: '/v1',
          ),
        ],
      );
      final denied = ScriptAdapter((_) => (401, {'error': 'denied'}));
      business.httpClientAdapter = denied;
      await expectLater(
        business.post<Object?>('https://business.test/v1/x', data: {'x': 1}),
        throwsA(
          isA<DioException>().having(
            (e) => e.error,
            'typed error',
            isA<AccountReplayRequiredError>(),
          ),
        ),
      );
      expect(denied.requests.length, 1);
      expect(c.session.state, isA<SignedIn>());
      final server = ScriptAdapter((_) => (503, {'error': 'unavailable'}));
      business.httpClientAdapter = server;
      await expectLater(
        business.post<Object?>(
          'https://business.test/v1/x',
          data: {'x': 1},
          options: Options(
            extra: {
              AuthenticatedReplay.key: AuthenticatedReplay(
                rebuildBody: () => {'x': 1},
              ),
            },
          ),
        ),
        throwsA(isA<DioException>()),
      );
      expect(server.requests.length, 1);
      await c.dispose();
    },
  );
  test('business trust is origin and complete path segments', () async {
    final wire = WireAdapter(token);
    final accountDio = Dio()..httpClientAdapter = wire;
    final c = AccountClient(
      baseUrl: 'https://account.test',
      dio: accountDio,
      tokenStore: MemoryTokenStore(),
      deviceProvider: FixedDeviceProvider(const DeviceSnapshot(id: 'device')),
    );
    await c.initialize();
    await c.signInWithIdp(IdentityCredential.custom('ACME', {'p': 'x'}));
    final business = c.createAuthenticatedDio(
      trustedScopes: [
        TrustedRequestScope(origin: 'https://business.test', pathPrefix: '/v1'),
      ],
    );
    final target = WireAdapter({'ok': true});
    business.httpClientAdapter = target;
    await business.get<Object?>('https://business.test/v1/items');
    expect(target.requests.single.headers['Authorization'], 'Bearer access');
    await expectLater(
      business.get<Object?>('https://business.test/v10/items'),
      throwsA(isA<DioException>()),
    );
    await expectLater(
      business.get<Object?>('https://other.test/v1/items'),
      throwsA(isA<DioException>()),
    );
    expect(target.requests.length, 1);
    await c.dispose();
    expect(wire.closed, false);
  });
}
