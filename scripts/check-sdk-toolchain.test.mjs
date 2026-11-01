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
