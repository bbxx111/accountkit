import 'package:accountkit_flutter/accountkit_flutter.dart';
import 'package:flutter_test/flutter_test.dart';

import 'policy_contract.dart' show oidc;
import 'service_contract.dart' as support;

void main() {
  tearDown(() async {
    for (final client in support.clients) {
      await client.dispose();
    }
    support.clients.clear();
  });
  test('seed real custom host with empty required-kind policy', () async {
    final origin = support.requiredEnv('ACCOUNTKIT_FLUTTER_POLICY_ORIGIN');
    final control = support.requiredEnv(
      'ACCOUNTKIT_FLUTTER_POLICY_CONTROL_ORIGIN',
    );
    final subject = support.requiredEnv('ACCOUNTKIT_FLUTTER_POLICY_SUBJECT');
    final ready = await support.controlJson(control, '/__fixture/ready');
    expect(ready['mode'], 'embedded-test-host');
    expect(ready['profile'], 'custom-extended');
    final client = support.account(
      origin,
      'policy-${support.run}-seed',
      prefix: ready['api_prefix']! as String,
    );
    final seeded = await client.signInWithIdp(await oidc(control, subject));
    expect(seeded, isA<TokenPair>());
    expect(client.session.state, isA<SignedIn>());
    expect((await client.listIdentities()).map((i) => i.kind), ['TEST_OIDC']);
    // Synthetic resource ID only. No token, provider subject, or identity plaintext.
    // ignore: avoid_print
    print('SDK_POLICY_SEED_USER_ID=${client.session.userId}');
  }, timeout: const Timeout(Duration(seconds: 60)));
}
