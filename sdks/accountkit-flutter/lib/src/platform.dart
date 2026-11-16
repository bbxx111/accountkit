import 'dart:async';
import 'dart:convert';
import 'dart:math';

import 'package:device_info_plus/device_info_plus.dart';
import 'package:flutter_secure_storage/flutter_secure_storage.dart';
import 'package:flutter/foundation.dart';
import 'package:package_info_plus/package_info_plus.dart';

import 'errors.dart';
import 'http_protocol.dart';
import 'models.dart';
import 'session.dart';
import 'validation_patterns.dart';

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
    if (!deviceIdPattern.hasMatch(id) ||
        name.runes.length > 64 ||
        deviceHeaderControlPattern.hasMatch(name))
      throw const AccountConfigurationError();
    const allowed = {
      HttpHeader.clientType,
      HttpHeader.osName,
      HttpHeader.osVersion,
      HttpHeader.browserName,
      HttpHeader.browserVersion,
      HttpHeader.appName,
      HttpHeader.appVersion,
      HttpHeader.deviceModel,
      HttpHeader.deviceType,
    };
    if (clientHeaders.entries.any(
          (e) =>
              !allowed.contains(e.key) ||
              e.value.length > 128 ||
              deviceHeaderControlPattern.hasMatch(e.value),
        ) ||
        utf8.encode(jsonEncode(clientHeaders)).length > 2048)
      throw const AccountConfigurationError();
    return {
      HttpHeader.deviceId: id,
      HttpHeader.deviceName: name,
      ...clientHeaders,
    };
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
    if (saved != null && deviceIdPattern.hasMatch(saved)) return saved;
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
      HttpHeader.clientType: HttpProtocol.nativeClient,
      HttpHeader.appName: app.appName,
      HttpHeader.appVersion: app.version,
    };
    if (defaultTargetPlatform == TargetPlatform.android) {
      final info = await plugin.androidInfo;
      out.addAll({
        HttpHeader.osName: 'Android',
        HttpHeader.osVersion: info.version.release,
        HttpHeader.deviceModel: info.model,
      });
    } else {
      final info = await plugin.iosInfo;
      out.addAll({
        HttpHeader.osName: info.systemName,
        HttpHeader.osVersion: info.systemVersion,
        HttpHeader.deviceModel: info.utsname.machine,
      });
    }
    return out;
  }
}
