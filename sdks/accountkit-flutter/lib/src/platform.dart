import 'dart:async';
import 'dart:convert';
import 'dart:math';

import 'package:flutter/foundation.dart';
import 'package:flutter_secure_storage/flutter_secure_storage.dart';
import 'package:device_info_plus/device_info_plus.dart';
import 'package:package_info_plus/package_info_plus.dart';

import 'errors.dart';
import 'models.dart';
import 'session.dart';

/// Generate an unguessable original nonce; the application performs provider authorization.
String generateAccountNonce({int bytes = 32}) {
  if (bytes < 16 || bytes > 128) throw const AccountConfigurationError();
  final random = Random.secure();
  return base64UrlEncode(List.generate(bytes, (_) => random.nextInt(256)))
      .replaceAll('=', '');
}

final class SecureTokenStore implements TokenStore {
  SecureTokenStore({
    FlutterSecureStorage? storage,
    this.key = 'accountkit.session',
  }) : _storage = storage ?? const FlutterSecureStorage();
  final FlutterSecureStorage _storage;
  final String key;
  @override
  Future<TokenRecord?> read() async {
    final raw = await _storage.read(key: key);
    if (raw == null) return null;
    try {
      return TokenRecord.fromJson(object(jsonDecode(raw)));
    } catch (_) {
      throw const AccountProtocolError();
    }
  }

  @override
  Future<void> write(TokenRecord record) =>
      _storage.write(key: key, value: jsonEncode(record.toJson()));
  @override
  Future<void> clear() => _storage.delete(key: key);
}

final class DeviceSnapshot {
  const DeviceSnapshot({
    required this.id,
    this.name = '',
    this.clientHeaders = const {},
  });
  final String id, name;
  final Map<String, String> clientHeaders;
  Map<String, String> toHeaders() {
    if (!RegExp(r'^[A-Za-z0-9._-]{1,64}$').hasMatch(id) ||
        name.runes.length > 64 ||
        RegExp(r'[\x00-\x1f\x7f]').hasMatch(name))
      throw const AccountConfigurationError();
    const allowed = {
      'X-Client-Type',
      'X-Client-OS-Name',
      'X-Client-OS-Version',
      'X-Client-Browser-Name',
      'X-Client-Browser-Version',
      'X-Client-App-Name',
      'X-Client-App-Version',
      'X-Device-Model',
      'X-Device-Type',
    };
    if (clientHeaders.entries.any(
          (e) =>
              !allowed.contains(e.key) ||
              e.value.length > 128 ||
              RegExp(r'[\x00-\x1f\x7f]').hasMatch(e.value),
        ) ||
        utf8.encode(jsonEncode(clientHeaders)).length > 2048)
      throw const AccountConfigurationError();
    return {'X-Device-Id': id, 'X-Device-Name': name, ...clientHeaders};
  }

  @override
  String toString() => 'DeviceSnapshot(redacted)';
}

abstract interface class DeviceProvider {
  Future<DeviceSnapshot> snapshot();
}

typedef ClientInfoProvider = Future<Map<String, String>> Function();

final class PlatformDeviceProvider implements DeviceProvider {
  PlatformDeviceProvider({
    FlutterSecureStorage? storage,
    this.key = 'accountkit.device',
    this.clientInfo,
    this.descriptionBudget = const Duration(seconds: 1),
  }) : _storage = storage ?? const FlutterSecureStorage();
  final FlutterSecureStorage _storage;
  final String key;
  final ClientInfoProvider? clientInfo;
  final Duration descriptionBudget;
  Future<String>? _id;
  Future<String> _loadId() async {
    final saved = await _storage.read(key: key);
    if (saved != null && RegExp(r'^[A-Za-z0-9._-]{1,64}$').hasMatch(saved))
      return saved;
    final id = generateAccountNonce();
    await _storage.write(key: key, value: id);
    return id;
  }

  @override
  Future<DeviceSnapshot> snapshot() async {
    if (kIsWeb ||
        ![
          TargetPlatform.android,
          TargetPlatform.iOS,
        ].contains(defaultTargetPlatform))
      throw const AccountConfigurationError();
    String id;
    try {
      id = await (_id ??= _loadId());
    } catch (_) {
      _id = null;
      throw const AccountStorageError();
    }
    Map<String, String> headers = {};
    try {
      headers = await (clientInfo ?? _platformInfo)().timeout(
        descriptionBudget,
      );
      DeviceSnapshot(id: id, clientHeaders: headers).toHeaders();
    } catch (_) {
      headers = {};
    }
    return DeviceSnapshot(id: id, clientHeaders: Map.unmodifiable(headers));
  }

  Future<Map<String, String>> _platformInfo() async {
    final app = await PackageInfo.fromPlatform();
    final plugin = DeviceInfoPlugin();
    final out = <String, String>{
      'X-Client-Type': 'NATIVE',
      'X-Client-App-Name': app.appName,
      'X-Client-App-Version': app.version,
    };
    if (defaultTargetPlatform == TargetPlatform.android) {
      final info = await plugin.androidInfo;
      out.addAll({
        'X-Client-OS-Name': 'Android',
        'X-Client-OS-Version': info.version.release,
        'X-Device-Model': info.model,
      });
    } else {
      final info = await plugin.iosInfo;
      out.addAll({
        'X-Client-OS-Name': info.systemName,
        'X-Client-OS-Version': info.systemVersion,
        'X-Device-Model': info.utsname.machine,
      });
    }
    return out;
  }
}
