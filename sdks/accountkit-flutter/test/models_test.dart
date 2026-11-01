import 'package:accountkit_flutter/accountkit_flutter.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  test('all four states are scope derived', () {
    expect(authenticationStateForScope('user'), isA<SignedIn>());
    expect(authenticationStateForScope('user:bind'), isA<NeedsBinding>());
    expect(
      authenticationStateForScope('user:undelete'),
      isA<PendingDeletion>(),
    );
    expect(const SignedOut(), isA<AuthenticationState>());
  });
  test('all registered credential constructors follow formal shapes', () {
    for (final credential in [
      IdentityCredential.google(idToken: 'id', nonce: 'n'),
      IdentityCredential.oidc(kind: 'CORP_SSO', idToken: 'id', nonce: 'n'),
      IdentityCredential.facebookIdToken(idToken: 'id', nonce: 'n'),
    ]) {
      expect(credential.toJson()['credential'], {
        'id_token': 'id',
        'nonce': 'n',
      });
    }
    for (final credential in [
      IdentityCredential.wechat(appId: 'app', code: 'code'),
      IdentityCredential.alipay(appId: 'app', code: 'code'),
    ]) {
      expect(credential.toJson()['credential'], {
        'app_id': 'app',
        'code': 'code',
      });
    }
    for (final credential in [
      IdentityCredential.qq(appId: 'app', accessToken: 'access'),
      IdentityCredential.weibo(appId: 'app', accessToken: 'access'),
    ]) {
      expect(credential.toJson()['credential'], {
        'app_id': 'app',
        'access_token': 'access',
      });
    }
    expect(
      IdentityCredential.phone(
        target: '+8613800000000',
        codeId: 'a' * 32,
        code: '123456',
      ).kind,
      'PHONE',
    );
  });
  test('credential shapes and open kind preserve protocol', () {
    final c = IdentityCredential.email(
      target: 'x@example.test',
      codeId: 'a' * 32,
      code: '123456',
    );
    expect(c.toJson(), {
      'kind': 'EMAIL',
      'credential': {
        'target': 'x@example.test',
        'code_id': 'a' * 32,
        'code': '123456',
      },
    });
    expect(c.toString(), isNot(contains('123456')));
    expect(IdentityCredential.custom('ACME', {'proof': 'secret'}).kind, 'ACME');
    expect(
      IdentityCredential.apple(
        idToken: 'id',
        nonce: 'nonce',
      ).toJson()['credential'],
      {'id_token': 'id', 'nonce': 'nonce'},
    );
    expect(
      IdentityCredential.facebookAccessToken('access').toJson()['credential'],
      {'access_token': 'access'},
    );
    expect(
      () => IdentityCredential.custom('../bad', {}),
      throwsA(isA<AccountConfigurationError>()),
    );
  });
  test('token scopes are explicit and malformed fields rejected', () {
    final token = TokenPair.fromJson({
      'access_token': 'a',
      'token_type': 'Bearer',
      'expires_in': 900,
      'refresh_token': 'r',
      'refresh_expires_in': 3600,
      'scope': 'user:bind',
      'user_id': 'u_test',
      'is_new_user': false,
    });
    expect(token.authenticationState, isA<NeedsBinding>());
    expect(token.toString(), isNot(contains('refresh_token')));
    expect(() => TokenPair.fromJson({}), throwsA(isA<AccountProtocolError>()));
    expect(
      () => authenticationStateForScope('other'),
      throwsA(isA<AccountProtocolError>()),
    );
    expect(
      () => authenticationStateForScope('user user:bind'),
      throwsA(isA<AccountProtocolError>()),
    );
  });
  test('partial patch omits missing fields and preserves empty avatar', () {
    expect(const BasicProfilePatch(displayName: 'name').toJson(), {
      'display_name': 'name',
    });
    expect(const BasicProfilePatch(avatarUrl: '').toJson(), {'avatar_url': ''});
  });
  test('pending and token are exclusive and unknown action is recoverable', () {
    final pending = {
      'next_action': 'COMPLETE_IDENTITY',
      'sign_in_completion_token': 'ticket',
      'expire_time': '2030-01-01T00:00:00Z',
      'enabled_identity_kinds': ['EMAIL', 'CUSTOM'],
      'required_identity_kinds': ['EMAIL'],
    };
    expect(SignInResult.fromJson(pending), isA<PendingSignIn>());
    expect(
      SignInResult.fromJson(pending).toString(),
      isNot(contains('ticket')),
    );
    expect(
      () => SignInResult.fromJson({...pending, 'access_token': 'x'}),
      throwsA(isA<AccountProtocolError>()),
    );
    expect(
      () => SignInResult.fromJson({...pending, 'next_action': 'UNKNOWN'}),
      throwsA(isA<AccountProtocolError>()),
    );
  });
}
