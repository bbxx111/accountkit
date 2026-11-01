import 'dart:async';

import 'models.dart';
import 'session.dart';
import 'transport.dart';

abstract interface class AccountApi {
  SessionSnapshot get session;
  Stream<SessionSnapshot> get sessionChanges;
  Future<void> initialize();
  Future<CodeChallenge> sendSignInCode(
    String channel,
    String target, {
    AccountRequestOptions? options,
  });
  Future<SignInResult> signInWithCode(
    IdentityCredential credential, {
    AccountRequestOptions? options,
  });
  Future<SignInResult> signInWithIdp(
    IdentityCredential credential, {
    AccountRequestOptions? options,
  });
  Future<CodeChallenge> sendSignInCompletionCode(
    String ticket,
    String channel,
    String target, {
    AccountRequestOptions? options,
  });
  Future<SignInResult> completeSignIn(
    String ticket,
    IdentityCredential credential, {
    AccountRequestOptions? options,
  });
  Future<void> refreshToken({AccountRequestOptions? options});
  Future<void> revokeToken(String token, {AccountRequestOptions? options});
  Future<SignOutResult> signOut({bool revokeRemote = true});
  Future<AccountUser> getMe({AccountRequestOptions? options});
  Future<AccountUser> updateMe(
    BasicProfilePatch patch, {
    AccountRequestOptions? options,
  });
  Future<AccountUser> deleteMe({AccountRequestOptions? options});
  Future<AccountMutationResult<AccountUser>> undeleteMe({
    AccountRequestOptions? options,
  });
  Future<CodeChallenge> sendReauthenticationCode(
    String channel,
    String target, {
    AccountRequestOptions? options,
  });
  Future<AccessTokenResult> reauthenticate(
    IdentityCredential credential, {
    AccountRequestOptions? options,
  });
  Future<List<AccountSession>> listSessions({AccountRequestOptions? options});
  Future<void> revokeSession(String session, {AccountRequestOptions? options});
  Future<void> revokeOtherSessions({AccountRequestOptions? options});
  Future<CodeChallenge> sendBindCode(
    String channel,
    String target, {
    AccountRequestOptions? options,
  });
  Future<List<AccountIdentity>> listIdentities({
    AccountRequestOptions? options,
  });
  Future<AccountMutationResult<AccountIdentity>> bindIdentity(
    IdentityCredential credential, {
    AccountRequestOptions? options,
  });
  Future<AccountMutationResult<void>> unbindIdentity(
    String identity, {
    AccountRequestOptions? options,
  });
  Future<AccountMutationResult<AccountIdentity>> replaceIdentity(
    String identity,
    IdentityCredential credential, {
    AccountRequestOptions? options,
  });
  Future<CurrentSession> getCurrentSession({AccountRequestOptions? options});
  Future<AccountProfile> getMyProfile({AccountRequestOptions? options});
  Future<AccountProfile> patchMyProfile(
    JsonObject patch, {
    AccountRequestOptions? options,
  });
  Future<void> dispose();
}
