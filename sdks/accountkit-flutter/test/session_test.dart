import 'dart:async';

import 'package:accountkit_flutter/accountkit_flutter.dart';
import 'package:accountkit_flutter/testing.dart';
import 'package:flutter_test/flutter_test.dart';

TokenPair pair(String id, {String scope = 'user'}) => TokenPair(
  accessToken: 'a$id',
  refreshToken: 'r$id',
  expiresIn: 900,
  refreshExpiresIn: 3600,
  scope: scope,
  userId: id,
  isNewUser: false,
);
void main() {
  test(
    'corrupt persisted record is cleared instead of becoming signed in',
    () async {
      final runtime = SessionRuntime(store: CorruptStore());
      await runtime.initialize();
      expect(runtime.snapshot.state, isA<SignedOut>());
      expect(runtime.snapshot.initialized, true);
      await runtime.dispose();
    },
  );
  test('delayed clear cannot delete subsequent new session', () async {
    final store = MemoryTokenStore();
    final runtime = SessionRuntime(store: store);
    await runtime.initialize();
    await runtime.commitSignIn(pair('old'), runtime.beginSignIn());
    final gate = Completer<void>();
    store.beforeClear = () => gate.future;
    final clear = runtime.end();
    await Future<void>.delayed(Duration.zero);
    final newLogin = runtime.commitSignIn(pair('new'), runtime.beginSignIn());
    gate.complete();
    await expectLater(clear, throwsA(isA<AccountCancelledError>()));
    await newLogin;
    expect(store.record!.userId, 'new');
    expect(runtime.snapshot.userId, 'new');
    await runtime.dispose();
  });
  test(
    'session commits only after storage and merges concurrent refresh',
    () async {
      final store = MemoryTokenStore();
      final runtime = SessionRuntime(store: store);
      await runtime.initialize();
      final epoch = runtime.beginSignIn();
      await runtime.commitSignIn(pair('one'), epoch);
      expect(runtime.snapshot.state, isA<SignedIn>());
      var calls = 0;
      final gate = Completer<TokenPair>();
      final a = runtime.refresh(() {
        calls++;
        return gate.future;
      });
      final b = runtime.refresh(() {
        calls++;
        return gate.future;
      });
      gate.complete(
        TokenPair(
          accessToken: 'atwo',
          refreshToken: 'rtwo',
          expiresIn: 900,
          refreshExpiresIn: 3600,
          scope: 'user',
          userId: 'one',
          isNewUser: false,
        ),
      );
      await Future.wait([a, b]);
      expect(calls, 1);
      expect((await store.read())!.refreshToken, 'rtwo');
    },
  );
  test(
    'late sign in cannot overwrite new session and failed write is visible',
    () async {
      final store = MemoryTokenStore();
      final runtime = SessionRuntime(store: store);
      await runtime.initialize();
      final old = runtime.beginSignIn();
      final current = runtime.beginSignIn();
      await runtime.commitSignIn(pair('new'), current);
      await expectLater(
        runtime.commitSignIn(pair('old'), old),
        throwsA(isA<AccountCancelledError>()),
      );
      expect(runtime.snapshot.userId, 'new');
      store.writeFailure = true;
      await expectLater(
        runtime.commitSignIn(pair('bad'), runtime.beginSignIn()),
        throwsA(isA<AccountStorageError>()),
      );
      expect(runtime.snapshot.userId, 'new');
    },
  );
  test('reauthentication cannot overwrite rotated credentials', () async {
    final runtime = SessionRuntime(store: MemoryTokenStore());
    await runtime.initialize();
    await runtime.commitSignIn(pair('one'), runtime.beginSignIn());
    final epoch = runtime.epoch, revision = runtime.revision;
    await runtime.refresh(
      () async => TokenPair(
        accessToken: 'atwo',
        refreshToken: 'rtwo',
        expiresIn: 900,
        refreshExpiresIn: 3600,
        scope: 'user',
        userId: 'one',
        isNewUser: false,
      ),
    );
    await expectLater(
      runtime.commitAccess(
        AccessTokenResult.fromJson({
          'access_token': 'old',
          'token_type': 'Bearer',
          'expires_in': 900,
          'scope': 'user',
          'user_id': 'one',
        }),
        epoch,
        revision,
      ),
      throwsA(isA<AccountCancelledError>()),
    );
    expect(runtime.record!.refreshToken, 'rtwo');
  });
}

class CorruptStore implements TokenStore {
  @override
  Future<TokenRecord?> read() async => throw const AccountProtocolError();
  @override
  Future<void> clear() async {}
  @override
  Future<void> write(TokenRecord record) async {}
}
