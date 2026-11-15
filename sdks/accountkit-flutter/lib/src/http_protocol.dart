// Internal protocol vocabulary; deliberately absent from public exports.
abstract final class HttpHeader {
  static const accept = 'Accept';
  static const contentType = 'Content-Type';
  static const authorization = 'Authorization';
  static const requestId = 'X-Request-Id';
  static const retryAfter = 'Retry-After';
  static const deviceId = 'X-Device-Id';
  static const deviceName = 'X-Device-Name';
  static const clientType = 'X-Client-Type';
  static const osName = 'X-Client-OS-Name';
  static const osVersion = 'X-Client-OS-Version';
  static const browserName = 'X-Client-Browser-Name';
  static const browserVersion = 'X-Client-Browser-Version';
  static const appName = 'X-Client-App-Name';
  static const appVersion = 'X-Client-App-Version';
  static const deviceModel = 'X-Device-Model';
  static const deviceType = 'X-Device-Type';
}

abstract final class HttpProtocol {
  static const json = 'application/json';
  static const nativeClient = 'NATIVE';
}
