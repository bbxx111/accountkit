import assert from 'node:assert/strict';
import { test } from 'node:test';
import { validateSDKRelease, isFixedRef, validateCompatibility } from './check-sdk-release.mjs';

test('accepts independent Flutter and admin versions with matching release tags', () => {
  assert.deepEqual(validateSDKRelease('flutter', '1.0.0', 'sdk/flutter/v1.0.0'), []);
  assert.deepEqual(validateSDKRelease('admin-ts', '2.1.3', 'sdk/admin-ts/v2.1.3'), []);
});
test('rejects cross-package tags, service tags and manifest mismatches', () => {
  for (const tag of ['sdk/admin-ts/v1.0.0', 'v1.0.0', 'sdk/flutter/v1.0.1']) {
    assert.notEqual(validateSDKRelease('flutter', '1.0.0', tag).length, 0);
  }
});
test('rejects invalid versions instead of inventing a release name', () => {
  for (const version of ['', '1.0', '01.0.0', '1.0.0\n', '1.0.0/evil']) {
    assert.notEqual(validateSDKRelease('flutter', version, `sdk/flutter/v${version}`).length, 0);
  }
  assert.notEqual(validateSDKRelease('unknown', '1.0.0', 'sdk/unknown/v1.0.0').length, 0);
});
test('allows fixed full commits or package tags, never floating or abbreviated refs', () => {
  assert.equal(isFixedRef('6e17c77ba1c45a0ccedbf449cfae6b4e69654476', 'flutter'), true);
  assert.equal(isFixedRef('sdk/flutter/v1.0.0', 'flutter'), true);
  for (const ref of ['develop', 'main', 'HEAD', '6e17c77', 'sdk/admin-ts/v1.0.0', 'sdk/flutter/v1.0.0&path:/other']) {
    assert.equal(isFixedRef(ref, 'flutter'), false);
  }
});
test('release readiness requires matching verified service compatibility evidence',()=>{
  const row={sdk:'flutter',sdkVersion:'1.0.0',serviceVersion:'v1.0.0',verification:'verified',evidence:'openspec/changes/add-account-client-sdks/verification.md'};
  assert.deepEqual(validateCompatibility([row],'flutter','1.0.0'),[]);
  assert.notEqual(validateCompatibility([{...row,sdkVersion:'2.0.0'}],'flutter','1.0.0').length,0);
  assert.notEqual(validateCompatibility([{...row,verification:'pending'}],'flutter','1.0.0').length,0);
  assert.notEqual(validateCompatibility([{...row,evidence:''}],'flutter','1.0.0').length,0);
  assert.notEqual(validateCompatibility([{...row,serviceVersion:'develop'}],'flutter','1.0.0').length,0);
});
