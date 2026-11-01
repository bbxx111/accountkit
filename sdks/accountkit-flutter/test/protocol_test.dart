import 'dart:convert';
import 'dart:typed_data';

import 'package:accountkit_flutter/accountkit_flutter.dart';
import 'package:accountkit_flutter/testing.dart';
import 'package:dio/dio.dart';
import 'package:flutter_test/flutter_test.dart';

const stamp = '2030-01-01T00:00:00Z';
final token = <String, Object?>{
  'access_token': 'access',
  'refresh_token': 'refresh',
  'expires_in': 900,
  'refresh_expires_in': 3600,
  'token_type': 'Bearer',
  'scope': 'user',
  'user_id': 'u_0000000000001',
  'is_new_user': false,
};
final me = <String, Object?>{
  'name': 'users/u_0000000000001',
  'state': 'FUTURE',
  'display_name': 'name',
  'avatar_url': '',
  'create_time': stamp,
  'delete_time': null,
  'purge_time': null,
};
final identity = <String, Object?>{
  'name': 'users/u_0000000000001/identities/i_0000000000001',
  'kind': 'CUSTOM',
  'masked_subject': '',
  'create_time': stamp,
};
final session = <String, Object?>{
  'name': 'users/u_0000000000001/sessions/s_0000000000001',
  'device_id': 'device',
  'device_name': 'name',
  'create_time': stamp,
  'last_used_time': stamp,
  'is_current': true,
  'auth_time': stamp,
  'authentication_identities': [
    {'name': identity['name'], 'kind': 'CUSTOM'},
  ],
};
final challenge = <String, Object?>{'code_id': 'a' * 32, 'expire_time': stamp};

class WireAdapter implements HttpClientAdapter {
  WireAdapter(this.reply);
  Object? reply;
  int status = 200;
  final requests = <RequestOptions>[];
  bool closed = false;
  @override
  Future<ResponseBody> fetch(
    RequestOptions options,
    Stream<Uint8List>? stream,
    Future<void>? cancel,
  ) async {
    requests.add(options);
    return ResponseBody.fromString(
      reply == null ? '' : jsonEncode(reply),
      status,
      headers: {
        'content-type': ['application/json'],
      },
    );
  }

  @override
  void close({bool force = false}) {
    closed = true;
  }
}

void main() {
  late AccountClient client;
  late WireAdapter wire;
  late Dio dio;
  final cred = IdentityCredential.email(
    target: 'x@example.test',
    codeId: 'a' * 32,
    code: '123456',
  );
  setUp(() async {
    wire = WireAdapter(token);
    dio = Dio()..httpClientAdapter = wire;
    client = AccountClient(
      baseUrl: 'http://localhost:8080/base',
      apiPrefix: '/custom',
      allowInsecureHttp: true,
      dio: dio,
      tokenStore: MemoryTokenStore(),
      deviceProvider: FixedDeviceProvider(const DeviceSnapshot(id: 'device')),
    );
    await client.initialize();
    await client.signInWithCode(cred);
    wire.requests.clear();
  });
  tearDown(() async => client.dispose());
  test('sendSignInCode', () async {
    wire.reply = challenge;
    await client.sendSignInCode('EMAIL', 'x@example.test');
    expect(wire.requests.single.path, endsWith('/custom/users:sendSignInCode'));
    expect(wire.requests.single.headers['Authorization'], isNull);
  });
  test('signInWithCode', () async {
    wire.reply = token;
    await client.signInWithCode(cred);
    expect(wire.requests.single.data, cred.toJson());
    expect(wire.requests.single.headers['X-Device-Id'], 'device');
  });
  test('signInWithIdp', () async {
    wire.reply = token;
    await client.signInWithIdp(
      IdentityCredential.custom('ACME', {'proof': 'x'}),
    );
    expect(wire.requests.single.path, endsWith('users:signInWithIdp'));
  });
  test('sendSignInCompletionCode', () async {
    wire.reply = challenge;
    await client.sendSignInCompletionCode('ticket', 'EMAIL', 'x@example.test');
    expect(
      (wire.requests.single.data as Map)['sign_in_completion_token'],
      'ticket',
    );
    expect(wire.requests.single.headers['Authorization'], isNull);
  });
  test('completeSignIn', () async {
    wire.reply = token;
    await client.completeSignIn('ticket', cred);
    expect(wire.requests.single.path, endsWith('users:completeSignIn'));
    expect(wire.requests.single.headers['Authorization'], isNull);
  });
  test('refreshToken', () async {
    wire.reply = token;
    await client.refreshToken();
    expect(wire.requests.single.data, {
      'grant_type': 'refresh_token',
      'refresh_token': 'refresh',
    });
    expect(wire.requests.single.headers['Authorization'], isNull);
  });
  test('revokeToken', () async {
    wire.reply = null;
    await client.revokeToken('refresh');
    expect(wire.requests.single.data, {'token': 'refresh'});
  });
  test('getMe', () async {
    wire.reply = me;
    expect((await client.getMe()).state, 'FUTURE');
    expect(wire.requests.single.headers['Authorization'], 'Bearer access');
  });
  test('updateMe', () async {
    wire.reply = me;
    await client.updateMe(const BasicProfilePatch(avatarUrl: ''));
    expect(wire.requests.single.method, 'PATCH');
    expect(wire.requests.single.data, {'avatar_url': ''});
  });
  test('deleteMe', () async {
    wire.reply = me;
    await client.deleteMe();
    expect(client.session.state, isA<SignedOut>());
  });
  test('undeleteMe', () async {
    wire.reply = me;
    final value = await client.undeleteMe();
    expect(value.value.name, me['name']);
    expect(value.sessionSync, SessionSync.unconfirmed);
  });
  test('sendReauthenticationCode', () async {
    wire.reply = challenge;
    await client.sendReauthenticationCode('EMAIL', 'x@example.test');
    expect(
      wire.requests.single.path,
      endsWith('users/me:sendReauthenticationCode'),
    );
  });
  test('reauthenticate', () async {
    wire.reply = {...token}
      ..remove('refresh_token')
      ..remove('refresh_expires_in');
    await client.reauthenticate(cred);
    expect(client.session.state, isA<SignedIn>());
  });
  test('listSessions', () async {
    wire.reply = {
      'sessions': [session],
    };
    expect((await client.listSessions()).single.isCurrent, true);
  });
  test('revokeSession', () async {
    wire.reply = null;
    wire.status = 204;
    await client.revokeSession('s_0000000000002');
    expect(wire.requests.first.method, 'DELETE');
    expect(wire.requests.where((r) => r.method == 'DELETE').length, 1);
  });
  test('revokeOtherSessions', () async {
    wire.reply = {};
    await client.revokeOtherSessions();
    expect(wire.requests.single.path, endsWith('sessions:revokeOthers'));
  });
  test('sendBindCode', () async {
    wire.reply = challenge;
    await client.sendBindCode('EMAIL', 'x@example.test');
    expect(wire.requests.single.path, endsWith('users/me:sendBindCode'));
  });
  test('listIdentities', () async {
    wire.reply = {
      'identities': [identity],
    };
    expect((await client.listIdentities()).single.kind, 'CUSTOM');
  });
  test('bindIdentity', () async {
    wire.reply = identity;
    final result = await client.bindIdentity(cred);
    expect(result.value.kind, 'CUSTOM');
    expect(result.sessionSync, SessionSync.unconfirmed);
    expect(
      wire.requests.where((r) => r.path.endsWith('/identities')).length,
      1,
    );
  });
  test('unbindIdentity', () async {
    wire.reply = null;
    wire.status = 204;
    final result = await client.unbindIdentity('i_0000000000001');
    expect(result.sessionSync, SessionSync.unconfirmed);
    expect(wire.requests.where((r) => r.method == 'DELETE').length, 1);
  });
  test('replaceIdentity', () async {
    wire.reply = identity;
    final result = await client.replaceIdentity('i_0000000000001', cred);
    expect(result.value.name, identity['name']);
  });
  test('getCurrentSession', () async {
    wire.reply = session;
    final result = await client.getCurrentSession();
    expect(result.authenticationIdentities.single.kind, 'CUSTOM');
    expect(result.authTime, DateTime.parse(stamp));
  });
  test('getMyProfile', () async {
    wire.reply = {
      'name': 'users/u_0000000000001/profile',
      'profile_attributes': {'n': null},
    };
    expect((await client.getMyProfile()).profileAttributes, {'n': null});
  });
  test('patchMyProfile', () async {
    wire.reply = {
      'name': 'users/u_0000000000001/profile',
      'profile_attributes': <String, Object?>{},
    };
    await client.patchMyProfile({'n': null});
    expect(wire.requests.single.data, {
      'profile_attributes': {'n': null},
    });
  });
  test('URL escape and parent mismatch rejected before network', () async {
    expect(
      () => AccountClient(baseUrl: 'https://example.test/a/../b'),
      throwsA(isA<AccountConfigurationError>()),
    );
    await expectLater(
      client.unbindIdentity('users/u_0000000000002/identities/i_0000000000001'),
      throwsA(isA<AccountConfigurationError>()),
    );
    expect(wire.requests, isEmpty);
  });
  test('caller network ownership retained', () async {
    await client.dispose();
    expect(wire.closed, false);
  });
}
