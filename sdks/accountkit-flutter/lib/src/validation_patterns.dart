// These patterns retain their original validation domains and accepted bytes.
final codeIdPattern = RegExp(r'^[0-9a-f]{32}$');
final deviceIdPattern = RegExp(r'^[A-Za-z0-9._-]{1,64}$');
final deviceHeaderControlPattern = RegExp(r'[\x00-\x1f\x7f]');
final requestWhitespacePattern = RegExp(r'[\x00-\x20\x7f]');
final unsafePathEncodingPattern = RegExp(
  r'%(?:2e|2f|5c|25)',
  caseSensitive: false,
);
