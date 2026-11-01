// Explicit real-service suite: not named *_test.dart, so default tests stay offline.
import 'dart:convert';
import 'dart:io';

import 'package:accountkit_flutter/accountkit_flutter.dart';
import 'package:accountkit_flutter/testing.dart';
import 'package:flutter_test/flutter_test.dart';

final run = DateTime.now().microsecondsSinceEpoch.toString();
final clients = <AccountClient>[];
final lastCode = <String, DateTime>{};
String requiredEnv(String name) {
  final value = Platform.environment[name];
  if (value == null || value.isEmpty) {
    throw StateError('$name is required; real SDK integration cannot skip');
  }
  return value;
}

Future<Map<String, Object?>> controlJson(String origin, String path) async {
  final http = HttpClient();
  try {
    final request = await http.getUrl(Uri.parse(origin + path));
    final response = await request.close().timeout(const Duration(seconds: 10));
    if (response.statusCode != 200) {
      throw StateError('Test control HTTP ${response.statusCode}');
    }
    return (jsonDecode(await utf8.decoder.bind(response).join()) as Map)
        .cast<String, Object?>();
  } finally {
    http.close(force: true);
  }
}

AccountClient account(
  String origin,
  String device, {
  MemoryTokenStore? store,
  String prefix = '/v1',
}) {
  final client = AccountClient(
    baseUrl: origin,
    apiPrefix: prefix,
    allowInsecureHttp: true,
    tokenStore: store ?? MemoryTokenStore(),
    deviceProvider: FixedDeviceProvider(DeviceSnapshot(id: device)),
  );
  clients.add(client);
  return client;
}

Future<void> cooldown(String target) async {
  final previous = lastCode[target];
  if (previous != null) {
    final wait = previous
        .add(const Duration(milliseconds: 1100))
        .difference(DateTime.now());
    if (!wait.isNegative) await Future<void>.delayed(wait);
  }
}

Future<IdentityCredential> code(
  AccountClient client,
  String control,
  String target,
  String purpose,
) async {
  await cooldown(target);
  final challenge = await switch (purpose) {
    'SIGN_IN' => client.sendSignInCode('EMAIL', target),
    'REAUTH' => client.sendReauthenticationCode('EMAIL', target),
    'BIND' => client.sendBindCode('EMAIL', target),
    _ => throw StateError('Unsupported test code purpose'),
  };
  lastCode[target] = DateTime.now();
  final captured = await controlJson(
    control,
    '/__fixture/code?target=${Uri.encodeQueryComponent(target)}&purpose=$purpose',
  );
  return IdentityCredential.email(
    target: target,
    codeId: challenge.codeId,
    code: captured['code']! as String,
  );
}

Future<TokenPair> login(
  AccountClient client,
  String control,
  String target,
) async {
  final result = await client.signInWithCode(
    await code(client, control, target, 'SIGN_IN'),
  );
  expect(result, isA<TokenPair>());
  return result as TokenPair;
}

Future<T> checked<T>(String stage, Future<T> Function() action) async {
  try {
    return await action();
  } on AccountApiError catch (error) {
    throw StateError(
      '$stage: HTTP ${error.httpStatus}, reason ${error.reason}',
    );
  }
}

IdentityCredential providerCredential(
  String fixtureKind,
  Map<String, Object?> envelope,
) {
  final value = (envelope['credential']! as Map).cast<String, Object?>();
  String field(String key) => value[key]! as String;
  return switch (fixtureKind) {
    'APPLE' => IdentityCredential.apple(
      idToken: field('id_token'),
      nonce: field('nonce'),
    ),
    'GOOGLE' => IdentityCredential.google(
      idToken: field('id_token'),
      nonce: field('nonce'),
    ),
    'TEST_OIDC' => IdentityCredential.oidc(
      kind: 'TEST_OIDC',
      idToken: field('id_token'),
      nonce: field('nonce'),
    ),
    'WECHAT' || 'WECHAT_MINI' => IdentityCredential.wechat(
      appId: field('app_id'),
      code: field('code'),
    ),
    'ALIPAY' => IdentityCredential.alipay(
      appId: field('app_id'),
      code: field('code'),
    ),
    'QQ' => IdentityCredential.qq(
      appId: field('app_id'),
      accessToken: field('access_token'),
    ),
    'WEIBO' => IdentityCredential.weibo(
      appId: field('app_id'),
      accessToken: field('access_token'),
    ),
    'FACEBOOK' => IdentityCredential.facebookIdToken(
      idToken: field('id_token'),
      nonce: field('nonce'),
    ),
    'FACEBOOK_CLASSIC' => IdentityCredential.facebookAccessToken(
      field('access_token'),
    ),
    _ => IdentityCredential.custom(envelope['kind']! as String, value),
  };
}

void main() {
  tearDown(() async {
    for (final client in clients) {
      await client.dispose();
    }
    clients.clear();
  });

  test('real accountsvc EMAIL controller, rotation, reauth, deletion and recovery', () async {
    final control = requiredEnv('ACCOUNTKIT_FLUTTER_CONTROL_ORIGIN');
    final origin = requiredEnv('ACCOUNTKIT_FLUTTER_SERVICE_ORIGIN');
    final ready = await controlJson(control, '/__fixture/ready');
    expect(ready['mode'], 'accountsvc-development');
    expect(ready['origin'], origin);
    final store = MemoryTokenStore();
    final client = account(origin, 'flutter-$run-a', store: store);
    final controller = AccountController(api: client);
    addTearDown(controller.dispose);
    await controller.initialize();
    expect(controller.session.state, isA<SignedOut>());
    final target = 'flutter-$run-a@example.test';
    await controller.sendCode(CodePurpose.signIn, 'EMAIL', target);
    lastCode[target] = DateTime.now();
    final captured = await controlJson(
      control,
      '/__fixture/code?target=${Uri.encodeQueryComponent(target)}&purpose=SIGN_IN',
    );
    final result = await controller.submitCode(captured['code']! as String);
    expect(result, isA<TokenPair>());
    expect(controller.session.state, isA<SignedIn>());
    expect(store.record, isNotNull);
    final userId = client.session.userId;
    final beforeRefresh = store.record!;
    await client.refreshToken();
    expect(store.record!.refreshToken == beforeRefresh.refreshToken, isFalse);
    final beforeReauth = store.record!;
    await client.reauthenticate(await code(client, control, target, 'REAUTH'));
    expect(store.record!.refreshToken == beforeReauth.refreshToken, isTrue);
    expect(store.record!.refreshExpireTime, beforeReauth.refreshExpireTime);
    expect((await client.getMe()).name, 'users/$userId');
    final deleted = await client.deleteMe();
    expect(deleted.state, 'PENDING_DELETION');
    expect(client.session.state, isA<SignedOut>());
    expect(store.record, isNull);
    await login(client, control, target);
    expect(client.session.state, isA<PendingDeletion>());
    final restored = await client.undeleteMe();
    expect(restored.value.state, 'ACTIVE');
    expect(restored.sessionSync, SessionSync.confirmed);
    expect(client.session.state, isA<SignedIn>());
    expect(client.session.userId, userId);
    final signedOut = await client.signOut();
    expect(signedOut.remoteError, isNull);
    expect(client.session.state, isA<SignedOut>());
  }, timeout: const Timeout(Duration(seconds: 90)));

  test('real accountsvc profile, identities, sessions and successful replacement ending source session', () async {
    final control = requiredEnv('ACCOUNTKIT_FLUTTER_CONTROL_ORIGIN');
    final origin = requiredEnv('ACCOUNTKIT_FLUTTER_SERVICE_ORIGIN');
    final client = account(origin, 'flutter-$run-b');
    final target = 'flutter-$run-b@example.test';
    await login(client, control, target);
    final me = await client.updateMe(
      BasicProfilePatch(
        displayName: 'SDK synthetic',
        avatarUrl: 'https://example.test/avatar.png',
      ),
    );
    expect(me.displayName, 'SDK synthetic');
    final cleared = await client.updateMe(BasicProfilePatch(avatarUrl: ''));
    expect(cleared.avatarUrl, '');
    expect(cleared.displayName, 'SDK synthetic');
    await expectLater(
      client.getMyProfile(),
      throwsA(
        isA<AccountApiError>().having(
          (e) => e.reason,
          'reason',
          'PROFILE_ATTRIBUTES_NOT_CONFIGURED',
        ),
      ),
    );
    await expectLater(
      client.patchMyProfile({'theme': 'dark'}),
      throwsA(
        isA<AccountApiError>().having(
          (e) => e.reason,
          'reason',
          'PROFILE_ATTRIBUTES_NOT_CONFIGURED',
        ),
      ),
    );
    expect(client.session.state, isA<SignedIn>());
    final current = await checked('current session', client.getCurrentSession);
    expect(current.isCurrent, isTrue);
    expect(current.authenticationIdentities, isNotEmpty);
    final other = account(origin, 'flutter-$run-other');
    await checked('other login', () => login(other, control, target));
    final sessions = await client.listSessions();
    expect(sessions.length, 2);
    await checked(
      'revoke session',
      () => client.revokeSession(sessions.firstWhere((s) => !s.isCurrent).name),
    );
    expect((await client.listSessions()).length, 1);
    await checked('other relogin', () => login(other, control, target));
    await checked('revoke others', client.revokeOtherSessions);
    expect((await client.listSessions()).length, 1);
    final retained = 'flutter-$run-retained@example.test';
    final retainedProof = await code(client, control, retained, 'BIND');
    await expectLater(
      client.bindIdentity(retainedProof),
      throwsA(
        isA<AccountApiError>().having(
          (e) => e.reason,
          'reason',
          'IDENTITY_KIND_LIMIT',
        ),
      ),
    );
    expect((await client.listIdentities()).length, 1);
    final original = (await client.listIdentities()).single;
    final replacement = 'flutter-$run-replacement@example.test';
    final replaced = await checked(
      'replace identity',
      () async => client.replaceIdentity(
        original.name,
        await code(client, control, replacement, 'BIND'),
      ),
    );
    expect(replaced.value.kind, 'EMAIL');
    expect(replaced.value.name == original.name, isFalse);
    expect(replaced.sessionSync, SessionSync.sessionEnded);
    expect(client.session.state, isA<SignedOut>());
    await login(client, control, replacement);
    expect((await client.listIdentities()).map((i) => i.name), [
      replaced.value.name,
    ]);
  }, timeout: const Timeout(Duration(seconds: 90)));

  test('embedded fixture verifies every controlled IdP credential through public SDK', () async {
    final control = requiredEnv('ACCOUNTKIT_FLUTTER_CONTROL_ORIGIN');
    final embedded = requiredEnv('ACCOUNTKIT_FLUTTER_EMBEDDED_ORIGIN');
    final kinds = [
      'APPLE',
      'GOOGLE',
      'TEST_OIDC',
      'WECHAT',
      'WECHAT_MINI',
      'QQ',
      'WEIBO',
      'ALIPAY',
      'FACEBOOK',
      'FACEBOOK_CLASSIC',
    ];
    for (final kind in kinds) {
      final client = account(embedded, 'flutter-$run-$kind');
      final envelope = await controlJson(
        control,
        '/__fixture/credential?kind=$kind&subject=$run&nonce=${generateAccountNonce()}',
      );
      final credential = providerCredential(kind, envelope);
      final controller = AccountController(api: client);
      addTearDown(controller.dispose);
      await controller.initialize();
      var result = await controller.signInWithIdp(credential);
      if (result is PendingSignIn) {
        expect(controller.pending, isNotNull, reason: kind);
        expect(client.session.state, isA<SignedOut>(), reason: kind);
        final completionTarget =
            'flutter-$run-${kind.toLowerCase()}@example.test';
        await controller.sendCode(
          CodePurpose.completion,
          'EMAIL',
          completionTarget,
        );
        final proof = await controlJson(
          control,
          '/__fixture/code?target=${Uri.encodeQueryComponent(completionTarget)}&purpose=SIGN_IN_COMPLETE',
        );
        result = await controller.submitCode(proof['code']! as String);
        expect(controller.pending, isNull, reason: kind);
      }
      expect(result, isA<TokenPair>(), reason: kind);
      expect(client.session.state, isA<SignedIn>(), reason: kind);
      expect(
        (await client.listIdentities()).any((i) => i.kind == envelope['kind']),
        isTrue,
        reason: kind,
      );
      if (kind == 'APPLE') {
        final extraEnvelope = await controlJson(
          control,
          '/__fixture/credential?kind=GOOGLE&subject=${run}extra&nonce=${generateAccountNonce()}',
        );
        final bound = await client.bindIdentity(
          providerCredential('GOOGLE', extraEnvelope),
        );
        expect(bound.value.kind, 'GOOGLE');
        expect(bound.sessionSync, SessionSync.confirmed);
        expect(
          (await client.listIdentities()).any(
            (i) => i.name == bound.value.name,
          ),
          isTrue,
        );
        final unbound = await client.unbindIdentity(bound.value.name);
        expect(unbound.sessionSync, SessionSync.confirmed);
        expect(
          (await client.listIdentities()).any(
            (i) => i.name == bound.value.name,
          ),
          isFalse,
        );
      }
      expect(
        (await client.getCurrentSession()).authenticationIdentities.any(
          (i) => i.kind == envelope['kind'],
        ),
        isTrue,
        reason: kind,
      );
    }
  }, timeout: const Timeout(Duration(seconds: 90)));
}
