import 'dart:async';

import 'package:accountkit_flutter/accountkit_flutter.dart';
import 'package:accountkit_flutter/testing.dart';
import 'package:flutter_test/flutter_test.dart';

PendingSignIn pending() => PendingSignIn.fromJson({
  'next_action': 'COMPLETE_IDENTITY',
  'sign_in_completion_token': 'secret',
  'expire_time': '2030-01-01T00:05:00Z',
  'enabled_identity_kinds': ['EMAIL'],
  'required_identity_kinds': ['EMAIL'],
});
final credential = IdentityCredential.email(
  target: 'x@example.test',
  codeId: 'a' * 32,
  code: '123456',
);
void main() {
  test('controller own reauthentication commit is not treated as external cancellation', () async {
    final api = FakeAccountApi();
    await api.signInWithIdp(IdentityCredential.custom('ACME', {'p': 'x'}));
    final c = AccountController(
      api: api,
      deviceProvider: FixedDeviceProvider(const DeviceSnapshot(id: 'd')),
    );
    await c.initialize();
    await c.sendCode(CodePurpose.reauthentication, 'EMAIL', 'x@example.test');
    api.requestHandler = (_, _) async => {
      'access_token': 'new-access',
      'token_type': 'Bearer',
      'expires_in': 900,
      'scope': 'user',
      'user_id': 'u_0000000000001',
      'is_new_user': false,
    };
    await c.reauthenticateCode('123456');
    expect(c.codeRound, isNull);
    expect(c.session.state, isA<SignedIn>());
    c.dispose();
    await api.dispose();
  });
  test('CODE_INVALID keeps round and CODE_EXPIRED ends it', () async {
    final api = FakeAccountApi();
    final c = AccountController(
      api: api,
      deviceProvider: FixedDeviceProvider(const DeviceSnapshot(id: 'd')),
    );
    await c.initialize();
    await c.sendCode(CodePurpose.signIn, 'EMAIL', 'x@example.test');
    api.signInHandler = (_) async =>
        throw const AccountApiError(httpStatus: 400, reason: 'CODE_INVALID');
    await expectLater(c.submitCode('wrong'), throwsA(isA<AccountApiError>()));
    expect(c.codeRound, isNotNull);
    api.signInHandler = (_) async =>
        throw const AccountApiError(httpStatus: 400, reason: 'CODE_EXPIRED');
    await expectLater(c.submitCode('expired'), throwsA(isA<AccountApiError>()));
    expect(c.codeRound, isNull);
    c.dispose();
    await api.dispose();
  });
  test('dispose while device information waits sends no late login', () async {
    final api = FakeAccountApi();
    final provider = DelayedDevice();
    final c = AccountController(api: api, deviceProvider: provider);
    await c.initialize();
    final call = c.signInWithIdp(IdentityCredential.custom('ACME', {'p': 'x'}));
    c.dispose();
    provider.gate.complete(const DeviceSnapshot(id: 'd'));
    await expectLater(call, throwsA(isA<AccountCancelledError>()));
    expect(api.operations, isEmpty);
    await api.dispose();
  });
  test(
    'completion manual retry uses same credential ticket and device',
    () async {
      final clock = MutableAccountClock(DateTime.parse('2030-01-01T00:00:00Z'));
      final api = FakeAccountApi(clock: clock)..nextSignIn = pending();
      final device = FixedDeviceProvider(const DeviceSnapshot(id: 'original'));
      final c = AccountController(
        api: api,
        clock: clock,
        deviceProvider: device,
      );
      await c.initialize();
      await c.signInWithIdp(IdentityCredential.custom('ACME', {'p': 'x'}));
      var calls = 0;
      final received = <JsonObject>[];
      api.signInHandler = (value) async {
        calls++;
        received.add(value.toJson());
        if (calls == 1) throw const AccountNetworkError();
        return TokenPair(
          accessToken: 'a',
          refreshToken: 'r',
          expiresIn: 900,
          refreshExpiresIn: 3600,
          scope: 'user',
          userId: 'u_0000000000001',
          isNewUser: false,
        );
      };
      await expectLater(
        c.completeSignIn(credential),
        throwsA(isA<AccountNetworkError>()),
      );
      device.value = const DeviceSnapshot(id: 'changed');
      await c.retryCompletion();
      expect(received[0], received[1]);
      expect(c.pending, isNull);
      expect(api.session.state, isA<SignedIn>());
      c.dispose();
      await api.dispose();
    },
  );
  test(
    'server waits are bounded and CODE_INVALID does not imply attempts',
    () async {
      final clock = MutableAccountClock(DateTime.parse('2030-01-01T00:00:00Z'));
      final api = FakeAccountApi(clock: clock);
      final c = AccountController(
        api: api,
        clock: clock,
        deviceProvider: FixedDeviceProvider(const DeviceSnapshot(id: 'd')),
      );
      await c.initialize();
      api.codeHandler = (_, _, _) async => throw const AccountApiError(
        httpStatus: 429,
        reason: 'TARGET_VERIFY_LIMIT',
        retryAfterSeconds: 7,
      );
      await expectLater(
        c.sendCode(CodePurpose.signIn, 'EMAIL', 'x@example.test'),
        throwsA(isA<AccountApiError>()),
      );
      expect(c.remainingWait, const Duration(seconds: 7));
      expect(api.session.state, isA<SignedOut>());
      await expectLater(
        c.sendCode(CodePurpose.signIn, 'EMAIL', 'x@example.test'),
        throwsA(isA<AccountApiError>()),
      );
      expect(api.operations.length, 1);
      c.dispose();
      await api.dispose();
    },
  );
  test('pending cancel prevents late success from committing', () async {
    final api = FakeAccountApi();
    final clock = MutableAccountClock(DateTime.parse('2030-01-01T00:00:00Z'));
    final controller = AccountController(
      api: api,
      clock: clock,
      deviceProvider: FixedDeviceProvider(const DeviceSnapshot(id: 'device')),
    );
    await controller.initialize();
    api.nextSignIn = pending();
    await controller.signInWithIdp(
      IdentityCredential.custom('ACME', {'proof': 'x'}),
    );
    expect(controller.pending, isNotNull);
    expect(controller.session.state, isA<SignedOut>());
    final gate = Completer<SignInResult>();
    api.signInHandler = (_) => gate.future;
    final work = controller.completeSignIn(credential);
    await Future<void>.delayed(Duration.zero);
    controller.cancelFlow();
    gate.complete(
      TokenPair(
        accessToken: 'a',
        refreshToken: 'r',
        expiresIn: 900,
        refreshExpiresIn: 3600,
        scope: 'user',
        userId: 'u',
        isNewUser: false,
      ),
    );
    await expectLater(work, throwsA(isA<AccountCancelledError>()));
    expect(api.session.state, isA<SignedOut>());
    controller.dispose();
  });
  test(
    'code generation rejects old response and expiration ends round',
    () async {
      final api = FakeAccountApi();
      final clock = MutableAccountClock(DateTime.parse('2030-01-01T00:00:00Z'));
      final c = AccountController(
        api: api,
        clock: clock,
        deviceProvider: FixedDeviceProvider(const DeviceSnapshot(id: 'device')),
      );
      await c.initialize();
      final gate = Completer<CodeChallenge>();
      api.codeHandler = (_, _, _) => gate.future;
      final old = c.sendCode(CodePurpose.signIn, 'EMAIL', 'a@example.test');
      await Future<void>.delayed(Duration.zero);
      c.changeInput();
      gate.complete(
        CodeChallenge.fromJson({
          'code_id': 'a' * 32,
          'expire_time': '2030-01-01T00:01:00Z',
        }),
      );
      await expectLater(old, throwsA(isA<AccountCancelledError>()));
      expect(c.codeRound, isNull);
      c.dispose();
    },
  );
  test('completion expiry does not move on retry', () async {
    final clock = MutableAccountClock(DateTime.parse('2030-01-01T00:00:00Z'));
    final api = FakeAccountApi()..nextSignIn = pending();
    final c = AccountController(
      api: api,
      clock: clock,
      deviceProvider: FixedDeviceProvider(const DeviceSnapshot(id: 'device')),
    );
    await c.initialize();
    await c.signInWithIdp(IdentityCredential.custom('ACME', {'p': 'x'}));
    clock.advance(const Duration(minutes: 6));
    await expectLater(
      c.completeSignIn(credential),
      throwsA(isA<AccountCancelledError>()),
    );
    expect(c.pending, isNull);
    c.dispose();
  });
}

class DelayedDevice implements DeviceProvider {
  final gate = Completer<DeviceSnapshot>();
  @override
  Future<DeviceSnapshot> snapshot() => gate.future;
}
