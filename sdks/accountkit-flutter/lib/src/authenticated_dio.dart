import 'dart:async';

import 'errors.dart';

/// Authentication is allowed only for this origin and whole path subtree.
final class TrustedRequestScope {
  TrustedRequestScope({required String origin, required this.pathPrefix})
    : origin = Uri.parse(origin) {
    if (this.origin.host.isEmpty ||
        this.origin.userInfo.isNotEmpty ||
        this.origin.hasQuery ||
        this.origin.hasFragment ||
        this.origin.path.isNotEmpty && this.origin.path != '/' ||
        !['https', 'http'].contains(this.origin.scheme) ||
        !safeRequestPath(pathPrefix) ||
        !pathPrefix.startsWith('/') ||
        pathPrefix.startsWith('//'))
      throw const AccountConfigurationError();
  }
  final Uri origin;
  final String pathPrefix;
  bool allows(Uri target) {
    final prefix = pathPrefix.replaceFirst(RegExp(r'/+$'), '');
    return target.origin == origin.origin &&
        (target.path == prefix || target.path.startsWith('$prefix/'));
  }
}

bool safeRequestPath(String raw) =>
    !raw.contains('\\') &&
    !RegExp(r'[\x00-\x20\x7f]').hasMatch(raw) &&
    !RegExp(r'%(?:2e|2f|5c|25)', caseSensitive: false).hasMatch(raw) &&
    !RegExp(r'(^|/)\.{1,2}(/|$)').hasMatch(raw);

/// For writes set Options(extra: {AuthenticatedReplay.key: AuthenticatedReplay(...)}).
/// The factory must create a fresh body. Never reuse consumed streams/FormData.
final class AuthenticatedReplay {
  const AuthenticatedReplay({required this.rebuildBody});
  static const key = 'accountkit.replay';
  final FutureOr<Object?> Function() rebuildBody;
}
