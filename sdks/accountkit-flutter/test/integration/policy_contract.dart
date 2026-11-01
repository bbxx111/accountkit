// Explicit embedded-policy suite; default flutter test does not discover it.
import 'package:accountkit_flutter/accountkit_flutter.dart';
import 'package:accountkit_flutter/testing.dart';
import 'package:flutter_test/flutter_test.dart';

import 'service_contract.dart' as support;

Future<IdentityCredential> oidc(String origin, String subject) async {
  final envelope = await support.controlJson(
    origin,
    '/__fixture/credential?kind=TEST_OIDC&subject=$subject&nonce=${generateAccountNonce()}',
  );
  final value = (envelope['credential']! as Map).cast<String, Object?>();
  // Deliberately exercise the open custom-kind constructor, not a closed enum.
  return IdentityCredential.custom('TEST_OIDC', value);
}

void main() {
  tearDown(() async {
    for (final client in support.clients) {
      await client.dispose();
    }
    support.clients.clear();
  });
  test('embedded policy evolution produces NeedsBinding and preserves custom profile merge patch', () async {
    final requiredOrigin = support.requiredEnv(
      'ACCOUNTKIT_FLUTTER_POLICY_ORIGIN',
    );
    final control = support.requiredEnv(
      'ACCOUNTKIT_FLUTTER_POLICY_CONTROL_ORIGIN',
    );
    final subject = support.requiredEnv('ACCOUNTKIT_FLUTTER_POLICY_SUBJECT');
    final expectedUserId = support.requiredEnv(
      'ACCOUNTKIT_FLUTTER_POLICY_USER_ID',
    );
    final requiredReady = await support.controlJson(
      control,
      '/__fixture/ready',
    );
    expect(requiredReady['mode'], 'embedded-test-host');
    expect(requiredReady['profile'], 'custom-extended');

    final store = MemoryTokenStore();
    final client = support.account(
      requiredOrigin,
      'policy-${support.run}-required',
      store: store,
      prefix: requiredReady['api_prefix']! as String,
    );
    final controller = AccountController(api: client);
    addTearDown(controller.dispose);
    await controller.initialize();
    expect(controller.session.state, isA<SignedOut>());
    final restricted = await controller.signInWithIdp(
      await oidc(control, subject),
    );
    expect(restricted, isA<TokenPair>());
    expect(client.session.userId, expectedUserId);
    expect(controller.session.state, isA<NeedsBinding>());
    expect(store.record!.scope, 'user:bind');
    await expectLater(
      client.getMyProfile(),
      throwsA(
        isA<AccountApiError>().having(
          (e) => e.reason,
          'reason',
          'INSUFFICIENT_SCOPE',
        ),
      ),
    );
    expect(controller.session.state, isA<NeedsBinding>());
    final target = 'policy-${support.run}@example.test';
    final proof = await support.code(client, control, target, 'BIND');
    final bound = await client.bindIdentity(proof);
    expect(bound.value.kind, 'EMAIL');
    expect(bound.sessionSync, SessionSync.confirmed);
    expect(controller.session.state, isA<SignedIn>());
    expect(store.record!.scope, 'user');
    final patched = await client.patchMyProfile({
      'theme': 'dark',
      'nickname': 'SDK synthetic',
    });
    expect(patched.profileAttributes, {
      'theme': 'dark',
      'nickname': 'SDK synthetic',
    });
    final removed = await client.patchMyProfile({'nickname': null});
    expect(removed.profileAttributes, {'theme': 'dark'});
    expect((await client.getMyProfile()).profileAttributes, {'theme': 'dark'});
    expect((await client.getMe()).name, 'users/$expectedUserId');
    expect(
      (await client.getCurrentSession()).authenticationIdentities.any(
        (i) => i.kind == 'TEST_OIDC',
      ),
      isTrue,
    );
  }, timeout: const Timeout(Duration(seconds: 60)));
}
