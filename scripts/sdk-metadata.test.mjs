import assert from 'node:assert/strict';
import { test } from 'node:test';
import methods from './openapi-methods.cjs';
import { sdkDescriptors, sdkIds, compatibilityStates } from './sdk-metadata.mjs';
import { validateCoverage } from './check-sdk-coverage.mjs';

test('tool descriptors retain independent SDK distribution contracts', () => {
  assert.deepEqual(sdkIds, ['flutter', 'admin-ts']);
  assert.deepEqual(sdkDescriptors, {
    flutter: {directory:'sdks/accountkit-flutter',manifest:'pubspec.yaml',packageName:'accountkit_flutter',tagPrefix:'sdk/flutter/v'},
    'admin-ts': {directory:'sdks/accountkit-admin-ts',manifest:'package.json',packageName:'@accountkit/admin-client',tagPrefix:'sdk/admin-ts/v'},
  });
  assert.deepEqual(compatibilityStates, ['pending','partial','verified']);
  assert.ok(Object.isFrozen(sdkDescriptors.flutter));
});
test('operation catalog retains all HTTP methods and unknown surfaces fail closed', () => {
  assert.deepEqual(methods, ['get','post','put','patch','delete','head','options','trace']);
  assert.ok(Object.isFrozen(methods));
  assert.deepEqual(validateCoverage([], {unknown:{paths:{}}}), ['unknown surface: unknown']);
});
