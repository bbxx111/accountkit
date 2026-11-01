/// Errors deliberately exclude server bodies and credentials from diagnostics.
sealed class AccountError implements Exception {
  const AccountError();
  @override
  String toString() => runtimeType.toString();
}

final class AccountConfigurationError extends AccountError {
  const AccountConfigurationError();
}

final class AccountProtocolError extends AccountError {
  const AccountProtocolError({this.httpStatus});
  final int? httpStatus;
}

final class AccountStorageError extends AccountError {
  const AccountStorageError();
}

final class AccountCancelledError extends AccountError {
  const AccountCancelledError();
}

final class AccountNetworkError extends AccountError {
  const AccountNetworkError({this.timeout = false});
  final bool timeout;
}

final class AccountReplayRequiredError extends AccountError {
  const AccountReplayRequiredError();
}

final class AccountApiError extends AccountError {
  const AccountApiError({
    required this.httpStatus,
    this.reason,
    this.status,
    this.oauthError,
    this.retryAfterSeconds,
    this.requestId,
  });
  final int httpStatus;
  final String? reason, status, oauthError, requestId;
  final int? retryAfterSeconds;
  bool get requiresReauthentication => reason == 'REAUTHENTICATION_REQUIRED';
}
