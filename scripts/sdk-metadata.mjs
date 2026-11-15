// Internal tooling descriptors. SDK runtime packages never import this module.
export const sdkDescriptors = Object.freeze({
  flutter: Object.freeze({ directory: 'sdks/accountkit-flutter', manifest: 'pubspec.yaml', packageName: 'accountkit_flutter', tagPrefix: 'sdk/flutter/v' }),
  'admin-ts': Object.freeze({ directory: 'sdks/accountkit-admin-ts', manifest: 'package.json', packageName: '@accountkit/admin-client', tagPrefix: 'sdk/admin-ts/v' }),
});
export const sdkIds = Object.freeze(Object.keys(sdkDescriptors));
export const compatibilityStates = Object.freeze(['pending', 'partial', 'verified']);
export function sdkTagPrefix(sdk) {
  return Object.hasOwn(sdkDescriptors, sdk) ? sdkDescriptors[sdk].tagPrefix : `sdk/${sdk}/v`;
}
