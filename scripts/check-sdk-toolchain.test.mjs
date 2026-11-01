import { test } from 'node:test';
import assert from 'node:assert/strict';
import { validateToolchain } from './check-sdk-toolchain.mjs';

const versions = {node:'24.21.0',pnpm:'11.24.0',flutter:'3.47.4',dart:'3.13.3'};
const outputs = {node:'v24.21.0',pnpm:'11.24.0',flutter:JSON.stringify({frameworkVersion:'3.47.4',dartSdkVersion:'3.13.3'})};
test('requires exact installed versions', () => {
  assert.doesNotThrow(() => validateToolchain(versions, outputs));
  assert.throws(() => validateToolchain(versions,{...outputs,node:'v24.20.0'}), /node.*24.21.0/);
  assert.throws(() => validateToolchain(versions,{...outputs,pnpm:''}), /pnpm/);
  assert.throws(() => validateToolchain(versions,{...outputs,flutter:'{}'}), /flutter/);
  assert.throws(() => validateToolchain(versions,{...outputs,flutter:JSON.stringify({frameworkVersion:'3.47.4',dartSdkVersion:'3.13.2'})}), /dart/);
});

test('accepts Flutter first-run bootstrap output before machine JSON', () => {
  const bootstrap = '  % Total    % Received % Xferd\r\nDownloading Dart SDK...\r\nBuilding flutter tool...\r\n';
  assert.deepEqual(validateToolchain(versions, {...outputs, flutter:bootstrap + outputs.flutter}), versions);
  assert.throws(() => validateToolchain(versions, {...outputs, flutter:bootstrap + JSON.stringify({frameworkVersion:'3.47.3',dartSdkVersion:'3.13.3'})}), /flutter.*3\.47\.4/);
});

test('bootstrap output cannot substitute for complete version JSON', () => {
  for (const flutter of ['Building flutter tool...', 'Downloading...\n{"frameworkVersion":', 'Downloading...\n{}', outputs.flutter + '\nfailed after version']) {
    assert.throws(() => validateToolchain(versions, {...outputs, flutter}));
  }
});
