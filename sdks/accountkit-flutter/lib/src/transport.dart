import 'dart:async';
import 'dart:convert';
import 'dart:typed_data';

import 'package:dio/dio.dart';

import 'errors.dart';
import 'platform.dart';

final class AccountRequestOptions {
  const AccountRequestOptions({
    this.cancelToken,
    this.requestId,
    this.device,
    this.isValid,
  });
  final CancelToken? cancelToken;
  final String? requestId;
  final DeviceSnapshot? device;

  /// Optional synchronous lifecycle guard, checked before sending and committing.
  /// It must remain side effect free; cancellation does not roll back the server.
  final bool Function()? isValid;
  void check() {
    if ((cancelToken?.isCancelled ?? false) || !(isValid?.call() ?? true))
      throw const AccountCancelledError();
  }
}

final class AccountEndpoint {
  AccountEndpoint(
    String baseUrl,
    String apiPrefix, {
    bool allowInsecureHttp = false,
  }) {
    _safePath(baseUrl);
    _safePath(apiPrefix);
    final uri = Uri.tryParse(baseUrl);
    if (uri == null ||
        uri.host.isEmpty ||
        uri.userInfo.isNotEmpty ||
        uri.hasQuery ||
        uri.hasFragment ||
        !(uri.scheme == 'https' ||
            (allowInsecureHttp && uri.scheme == 'http')) ||
        !apiPrefix.startsWith('/') ||
        apiPrefix.startsWith('//') ||
        apiPrefix.contains('?') ||
        apiPrefix.contains('#'))
      throw const AccountConfigurationError();
    base = baseUrl.replaceFirst(RegExp(r'/+$'), '');
    prefix = apiPrefix.replaceFirst(RegExp(r'/+$'), '');
  }
  late final String base, prefix;
  String url(String path) => '$base$prefix$path';
  static void _safePath(String value) {
    if (value.contains('\\') ||
        RegExp(r'[\x00-\x20\x7f]').hasMatch(value) ||
        RegExp(r'%(?:2e|2f|5c|25)', caseSensitive: false).hasMatch(value) ||
        Uri.tryParse(value)?.path
                .split('/')
                .any((s) => s == '.' || s == '..') ==
            true ||
        value.contains('/./') ||
        value.contains('/../'))
      throw const AccountConfigurationError();
  }
}

String resourceId(
  String value,
  String collection,
  String prefix,
  String? userId,
) {
  final pattern = RegExp('^${prefix}_[0-9a-hjkmnp-tv-z]{13}\$');
  if (pattern.hasMatch(value)) return value;
  final parts = value.split('/');
  if (parts.length != 4 ||
      parts[0] != 'users' ||
      parts[1] != userId ||
      parts[2] != collection ||
      !pattern.hasMatch(parts[3]))
    throw const AccountConfigurationError();
  return parts[3];
}

final class AccountTransport {
  AccountTransport({
    required this.endpoint,
    Dio? dio,
    this.timeout = const Duration(seconds: 30),
  }) : dio = _isolatedDio(dio);
  final AccountEndpoint endpoint;
  final Dio dio;
  final Duration timeout;
  bool _disposed = false;
  final _active = <CancelToken>{};
  Future<Object?> send(
    String method,
    String path, {
    Object? data,
    String? access,
    DeviceSnapshot? device,
    AccountRequestOptions? options,
    bool empty = false,
  }) async {
    if (_disposed) throw const AccountCancelledError();
    options?.check();
    final requestId = options?.requestId ?? generateAccountNonce(bytes: 16);
    if (requestId.isEmpty ||
        requestId.length > 128 ||
        RegExp(r'[\x00-\x20\x7f]').hasMatch(requestId))
      throw const AccountConfigurationError();
    final cancel = CancelToken();
    void cancelRequest(Object? reason) {
      if (!cancel.isCancelled) cancel.cancel();
    }

    _active.add(cancel);
    if (options?.cancelToken != null)
      unawaited(options!.cancelToken!.whenCancel.then(cancelRequest));
    try {
      final response = await dio
          .request<Object?>(
            endpoint.url(path),
            data: data,
            cancelToken: cancel,
            options: Options(
              method: method,
              headers: {
                'Accept': 'application/json',
                'X-Request-Id': requestId,
                if (data != null) 'Content-Type': 'application/json',
                if (access != null) 'Authorization': 'Bearer $access',
                ...?device?.toHeaders(),
              },
              followRedirects: false,
              maxRedirects: 0,
              responseType: ResponseType.plain,
              validateStatus: (_) => true,
              sendTimeout: timeout,
              receiveTimeout: timeout,
            ),
          )
          .timeout(
            timeout,
            onTimeout: () {
              cancel.cancel();
              throw const AccountNetworkError(timeout: true);
            },
          );
      options?.check();
      if (_disposed) throw const AccountCancelledError();
      final status = response.statusCode ?? 0;
      Object? decoded;
      if (response.data is String && (response.data! as String).isNotEmpty) {
        try {
          decoded = jsonDecode(response.data! as String);
        } catch (_) {
          if (status >= 200 && status < 300)
            throw AccountProtocolError(httpStatus: status);
        }
      } else if (response.data is Map<String, Object?>) {
        decoded = response.data;
      }
      if (status < 200 || status >= 300) {
        final body = decoded is Map<String, Object?>
            ? decoded
            : <String, Object?>{};
        final detail = body['error'];
        final oauth = detail is String ? detail : null;
        final api = detail is Map<String, Object?> ? detail : body;
        final wait = api['retry_after_seconds'];
        final headerWait = int.tryParse(
          response.headers.value('retry-after') ?? '',
        );
        throw AccountApiError(
          httpStatus: status,
          reason: api['reason'] is String ? api['reason']! as String : null,
          status: api['status'] is String ? api['status']! as String : null,
          oauthError: oauth,
          retryAfterSeconds: wait is int ? wait : headerWait,
          requestId: response.headers.value('x-request-id') ?? requestId,
        );
      }
      if (empty) return null;
      if (decoded == null) throw AccountProtocolError(httpStatus: status);
      return decoded;
    } on DioException catch (error) {
      if (CancelToken.isCancel(error)) throw const AccountCancelledError();
      if (error.error is AccountError) throw error.error! as AccountError;
      throw AccountNetworkError(
        timeout: [
          DioExceptionType.connectionTimeout,
          DioExceptionType.receiveTimeout,
          DioExceptionType.sendTimeout,
        ].contains(error.type),
      );
    } finally {
      _active.remove(cancel);
    }
  }

  void dispose() {
    if (_disposed) return;
    _disposed = true;
    for (final token in _active) {
      token.cancel();
    }
    _active.clear();
    dio.close(force: true);
  }
}

// Borrow only the injected transport adapter. Account requests deliberately do
// not inherit defaults, interceptors or retry behavior from a business Dio.
Dio _isolatedDio(Dio? source) {
  final isolated = Dio();
  if (source != null)
    isolated.httpClientAdapter = _BorrowedAdapter(source.httpClientAdapter);
  return isolated;
}

final class _BorrowedAdapter implements HttpClientAdapter {
  _BorrowedAdapter(this.adapter);
  final HttpClientAdapter adapter;
  @override
  Future<ResponseBody> fetch(
    RequestOptions options,
    Stream<Uint8List>? requestStream,
    Future<void>? cancelFuture,
  ) => adapter.fetch(options, requestStream, cancelFuture);
  @override
  void close({bool force = false}) {}
}
