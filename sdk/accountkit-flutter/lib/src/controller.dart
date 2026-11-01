import 'dart:async';

import 'package:dio/dio.dart';
import 'package:flutter/foundation.dart';

import 'api.dart';
import 'client.dart';
import 'errors.dart';
import 'models.dart';
import 'platform.dart';
import 'session.dart';
import 'transport.dart';

enum CodePurpose { signIn, completion, reauthentication, binding }

final class VerificationCodeRound {
  VerificationCodeRound({
    required this.purpose,
    required this.channel,
    required String target,
    required this.challenge,
    required this.generation,
  }) : _target = target;
  final CodePurpose purpose;
  final String channel, _target;
  final CodeChallenge challenge;
  final int generation;
  IdentityCredential credential(String code) => IdentityCredential.code(
    channel,
    target: _target,
    codeId: challenge.codeId,
    code: code,
  );
  @override
  String toString() => 'VerificationCodeRound(redacted)';
}

final class AccountController extends ChangeNotifier {
  AccountController({
    required this.api,
    this.clock = const SystemAccountClock(),
    DeviceProvider? deviceProvider,
    this.ownsApi = false,
  }) : _deviceProvider = deviceProvider {
    _subscription = api.sessionChanges.listen((_) {
      if (_disposed) return;
      if (_authOperations == 0)
        cancelFlow();
      else
        _notify();
    });
  }
  final AccountApi api;
  final AccountClock clock;
  final bool ownsApi;
  final DeviceProvider? _deviceProvider;
  late final StreamSubscription<SessionSnapshot> _subscription;
  int _generation = 0, _authOperations = 0;
  bool _disposed = false;
  Timer? _expiry;
  final _requests = <CancelToken>{};
  PendingSignIn? _pending;
  DeviceSnapshot? _pendingDevice;
  VerificationCodeRound? _round;
  IdentityCredential? _retryCredential;
  AccountError? lastError;
  DateTime? waitUntil;
  SessionSnapshot get session => api.session;
  PendingSignIn? get pending {
    if (_pending != null && !_pending!.expireTime.isAfter(clock.now())) {
      _invalidate();
    }
    return _pending;
  }

  VerificationCodeRound? get codeRound {
    if (_round != null && !_round!.challenge.expireTime.isAfter(clock.now()))
      _round = null;
    return _round;
  }

  bool get initialized => api.session.initialized;
  Duration get remainingWait {
    final end = waitUntil;
    return end == null || !end.isAfter(clock.now())
        ? Duration.zero
        : end.difference(clock.now());
  }

  void _notify() {
    if (!_disposed) notifyListeners();
  }

  void _check(int generation) {
    if (_disposed || generation != _generation)
      throw const AccountCancelledError();
  }

  void _invalidate() {
    _generation++;
    for (final token in _requests) {
      token.cancel();
    }
    _requests.clear();
    _pending = null;
    _pendingDevice = null;
    _round = null;
    _retryCredential = null;
    _expiry?.cancel();
    _expiry = null;
  }

  void cancelFlow() {
    _invalidate();
    lastError = null;
    waitUntil = null;
    _notify();
  }

  void changeInput() => cancelFlow();
  Future<void> initialize() async {
    if (_disposed) throw const AccountCancelledError();
    await api.initialize();
    if (_disposed) throw const AccountCancelledError();
    _notify();
  }

  Future<DeviceSnapshot> _device() async {
    final provider = _deviceProvider;
    if (provider != null) return provider.snapshot();
    if (api is AccountClient) return (api as AccountClient).deviceSnapshot();
    return PlatformDeviceProvider().snapshot();
  }

  AccountRequestOptions _options(CancelToken token, {DeviceSnapshot? device}) =>
      AccountRequestOptions(cancelToken: token, device: device);
  void _error(AccountError error) {
    lastError = error;
    if (error is AccountApiError) {
      if (error.retryAfterSeconds != null)
        waitUntil = clock.now().add(
          Duration(seconds: error.retryAfterSeconds!),
        );
      if (error.reason == 'CODE_EXPIRED') _round = null;
      if (error.reason == 'SIGN_IN_COMPLETION_INVALID' ||
          error.reason == 'SIGN_IN_ALREADY_COMPLETED')
        _invalidate();
    }
    _notify();
  }

  Future<SignInResult> _start(
    IdentityCredential credential, {
    required bool idp,
    bool preserveRound = false,
  }) async {
    final previousRound = preserveRound ? _round : null;
    cancelFlow();
    final generation = _generation;
    if (previousRound != null)
      _round = VerificationCodeRound(
        purpose: previousRound.purpose,
        channel: previousRound.channel,
        target: previousRound._target,
        challenge: previousRound.challenge,
        generation: generation,
      );
    final token = CancelToken();
    _requests.add(token);
    _authOperations++;
    try {
      final device = await _device();
      _check(generation);
      final result = idp
          ? await api.signInWithIdp(
              credential,
              options: _options(token, device: device),
            )
          : await api.signInWithCode(
              credential,
              options: _options(token, device: device),
            );
      _check(generation);
      _accept(result, device);
      return result;
    } on AccountError catch (e) {
      if (generation == _generation) _error(e);
      rethrow;
    } finally {
      _requests.remove(token);
      _authOperations--;
    }
  }

  Future<SignInResult> signInWithIdp(IdentityCredential credential) =>
      _start(credential, idp: true);
  Future<SignInResult> signInWithCode(IdentityCredential credential) =>
      _start(credential, idp: false);
  void _accept(SignInResult result, DeviceSnapshot device) {
    if (result is PendingSignIn) {
      final previous = _pending;
      _pending = result;
      _pendingDevice = device;
      if (previous != null && result.expireTime.isAfter(previous.expireTime))
        throw const AccountProtocolError();
      _expiry?.cancel();
      final duration = result.expireTime.difference(clock.now());
      if (duration <= Duration.zero) {
        _invalidate();
        throw const AccountCancelledError();
      }
      _expiry = Timer(duration, () {
        if (!_disposed) {
          _invalidate();
          _notify();
        }
      });
    } else {
      _pending = null;
      _pendingDevice = null;
      _retryCredential = null;
      _round = null;
      _expiry?.cancel();
      _expiry = null;
    }
    lastError = null;
    _notify();
  }

  Future<CodeChallenge> sendCode(
    CodePurpose purpose,
    String channel,
    String target,
  ) async {
    if (_disposed) throw const AccountCancelledError();
    if (remainingWait > Duration.zero)
      throw AccountApiError(
        httpStatus: 429,
        retryAfterSeconds: remainingWait.inSeconds + 1,
      );
    if (purpose == CodePurpose.completion && pending == null)
      throw const AccountCancelledError();
    // A new request owns the round; previous code responses can no longer attach.
    final generation = ++_generation;
    for (final old in _requests) {
      old.cancel();
    }
    _requests.clear();
    _round = null;
    _retryCredential = null;
    final token = CancelToken();
    _requests.add(token);
    final normalized = channel == 'EMAIL'
        ? target.trim().toLowerCase()
        : target.trim();
    try {
      final options = _options(
        token,
        device: purpose == CodePurpose.completion ? _pendingDevice : null,
      );
      final response = await switch (purpose) {
        CodePurpose.signIn => api.sendSignInCode(
          channel,
          normalized,
          options: options,
        ),
        CodePurpose.completion => api.sendSignInCompletionCode(
          pending!.completionToken,
          channel,
          normalized,
          options: options,
        ),
        CodePurpose.reauthentication => api.sendReauthenticationCode(
          channel,
          normalized,
          options: options,
        ),
        CodePurpose.binding => api.sendBindCode(
          channel,
          normalized,
          options: options,
        ),
      };
      _check(generation);
      _round = VerificationCodeRound(
        purpose: purpose,
        channel: channel,
        target: normalized,
        challenge: response,
        generation: generation,
      );
      lastError = null;
      _notify();
      return response;
    } on AccountError catch (e) {
      if (generation == _generation) _error(e);
      rethrow;
    } finally {
      _requests.remove(token);
    }
  }

  Future<SignInResult> submitCode(String code) async {
    final round = codeRound;
    if (round == null || round.generation != _generation)
      throw const AccountCancelledError();
    final credential = round.credential(code);
    if (round.purpose == CodePurpose.signIn)
      return _start(credential, idp: false, preserveRound: true);
    if (round.purpose == CodePurpose.completion)
      return completeSignIn(credential);
    throw const AccountConfigurationError();
  }

  Future<SignInResult> completeSignIn(IdentityCredential credential) =>
      _complete(credential, retry: false);
  Future<SignInResult> retryCompletion() async {
    final credential = _retryCredential;
    if (credential == null) throw const AccountCancelledError();
    return _complete(credential, retry: true);
  }

  Future<SignInResult> _complete(
    IdentityCredential credential, {
    required bool retry,
  }) async {
    final flow = pending, device = _pendingDevice;
    if (flow == null || device == null) throw const AccountCancelledError();
    if (!retry) _retryCredential = null;
    final generation = _generation;
    final token = CancelToken();
    _requests.add(token);
    _authOperations++;
    try {
      final result = await api.completeSignIn(
        flow.completionToken,
        credential,
        options: AccountRequestOptions(
          cancelToken: token,
          device: device,
          isValid: () =>
              !_disposed &&
              generation == _generation &&
              clock.now().isBefore(flow.expireTime),
        ),
      );
      _check(generation);
      _accept(result, device);
      return result;
    } on AccountError catch (e) {
      if (generation == _generation) {
        if (e is AccountNetworkError ||
            e is AccountApiError && e.httpStatus >= 500)
          _retryCredential = credential;
        else
          _retryCredential = null;
        _error(e);
      }
      rethrow;
    } finally {
      _requests.remove(token);
      _authOperations--;
    }
  }

  Future<void> reauthenticateCode(String code) async {
    final round = codeRound;
    if (round == null || round.purpose != CodePurpose.reauthentication)
      throw const AccountCancelledError();
    final generation = _generation;
    final token = CancelToken();
    _requests.add(token);
    _authOperations++;
    try {
      await api.reauthenticate(
        round.credential(code),
        options: _options(token),
      );
      _check(generation);
      _round = null;
    } on AccountError catch (e) {
      if (generation == _generation) _error(e);
      rethrow;
    } finally {
      _requests.remove(token);
      _authOperations--;
    }
  }

  Future<AccountMutationResult<AccountIdentity>> bindCode(String code) async {
    final round = codeRound;
    if (round == null || round.purpose != CodePurpose.binding)
      throw const AccountCancelledError();
    return api.bindIdentity(round.credential(code));
  }

  @override
  void dispose() {
    if (_disposed) return;
    _invalidate();
    _disposed = true;
    unawaited(_subscription.cancel());
    if (ownsApi) unawaited(api.dispose());
    super.dispose();
  }
}
