import 'package:accountkit_flutter/accountkit_flutter.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:flutter/foundation.dart';
import 'package:flutter_secure_storage/flutter_secure_storage.dart';

import 'dart:async';

void main() {
  test(
    'client description timeout falls back without waiting for provider',
    () async {
      debugDefaultTargetPlatformOverride = TargetPlatform.android;
      addTearDown(() => debugDefaultTargetPlatformOverride = null);
      FlutterSecureStorage.setMockInitialValues({});
      final gate = Completer<Map<String, String>>();
      final device = await PlatformDeviceProvider(
        clientInfo: () => gate.future,
        descriptionBudget: const Duration(milliseconds: 1),
      ).snapshot();
      expect(device.clientHeaders, isEmpty);
      gate.complete({'X-Client-App-Name': 'late'});
    },
  );
  TestWidgetsFlutterBinding.ensureInitialized();
  test('default identity persists and description failure degrades in bounded time', () async {
    debugDefaultTargetPlatformOverride = TargetPlatform.android;
    addTearDown(() => debugDefaultTargetPlatformOverride = null);
    FlutterSecureStorage.setMockInitialValues({});
    final provider = PlatformDeviceProvider(
      clientInfo: () async => throw StateError('private'),
      descriptionBudget: const Duration(milliseconds: 10),
    );
    final first = await provider.snapshot();
    final second = await PlatformDeviceProvider(
      clientInfo: () async => {'X-Client-App-Version': '1'},
    ).snapshot();
    expect(first.id, second.id);
    expect(first.clientHeaders, isEmpty);
    expect(second.clientHeaders['X-Client-App-Version'], '1');
  });
  test('nonce is random url safe and minimum entropy bounded', () {
    final a = generateAccountNonce(), b = generateAccountNonce();
    expect(a, isNot(b));
    expect(a, matches(RegExp(r'^[A-Za-z0-9_-]{43}$')));
    expect(
      () => generateAccountNonce(bytes: 8),
      throwsA(isA<AccountConfigurationError>()),
    );
  });
  test('device headers only expose explicit bounded metadata', () {
    expect(
      const DeviceSnapshot(
        id: 'device',
        clientHeaders: {'X-Client-App-Version': '1'},
      ).toHeaders()['X-Client-App-Version'],
      '1',
    );
    expect(
      () => const DeviceSnapshot(
        id: 'device',
        clientHeaders: {'Authorization': 'secret'},
      ).toHeaders(),
      throwsA(isA<AccountConfigurationError>()),
    );
    expect(
      () => const DeviceSnapshot(id: 'bad\n').toHeaders(),
      throwsA(isA<AccountConfigurationError>()),
    );
  });
}
