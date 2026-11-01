import assert from 'node:assert/strict';
import { test } from 'node:test';
import { validateCoverage, validateCoverageLinks } from './check-sdk-coverage.mjs';
import { mkdtemp, writeFile, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';

const documents = {
  enduser: { paths: { '/users/me': { get: { operationId: 'getMe' } } } },
  admin: { paths: { '/users': { get: { operationId: 'adminListUsers' } } } },
};
const records = [
  { surface: 'enduser', operationId: 'getMe', method: 'GET', path: '/users/me', sdkMethod: 'getMe', models: ['User'], tests: [{ file: 'test/client_test.dart', name: 'getMe' }], baseline: 'v1.0.0', optionalCapability: null },
  { surface: 'admin', operationId: 'adminListUsers', method: 'GET', path: '/users', sdkMethod: 'listUsers', models: ['ListUsersResponse'], tests: [{ file: 'test/client.test.ts', name: 'adminListUsers' }], baseline: 'v1.0.0', optionalCapability: null },
];

test('accepts exact coverage of independently supplied OpenAPI operations', () => {
  assert.deepEqual(validateCoverage(records, documents), []);
});
test('reports a removed operation instead of accepting an endpoint count', () => {
  assert.match(validateCoverage(records.slice(1), documents).join('\n'), /missing.*getMe/);
});
test('reports an extra operation even when another operation is missing', () => {
  const altered = structuredClone(records);
  altered[0].operationId = 'invented';
  const errors = validateCoverage(altered, documents).join('\n');
  assert.match(errors, /extra.*invented/);
  assert.match(errors, /missing.*getMe/);
});
test('rejects changed verbs, paths, unknown surfaces and duplicate operations', () => {
  const altered = structuredClone(records);
  altered[0].method = 'POST';
  altered[0].path = '/users/elsewhere';
  assert.match(validateCoverage(altered, documents).join('\n'), /route mismatch.*getMe/);
  assert.match(validateCoverage([...records, records[0]], documents).join('\n'), /duplicate.*getMe/);
  altered[0].surface = 'internal';
  assert.match(validateCoverage(altered, documents).join('\n'), /unknown surface/);
});
test('requires model, method, test and baseline links instead of generic coverage', () => {
  for (const [key, value] of [['models', []], ['tests', []], ['sdkMethod', ''], ['baseline', '']]) {
    const altered = structuredClone(records);
    altered[0][key] = value;
    assert.notEqual(validateCoverage(altered, documents).length, 0, key);
  }
});
test('discovers newly added OpenAPI routes without a fixed operation count', () => {
  const changed = structuredClone(documents);
  changed.admin.paths['/future'] = { post: { operationId: 'adminFuture' } };
  assert.match(validateCoverage(records, changed).join('\n'), /missing.*adminFuture/);
});
test('ignores path metadata and fails malformed coverage safely', () => {
  const changed = structuredClone(documents);
  changed.admin.paths['/users'].parameters = [{ name: 'page_token' }];
  assert.deepEqual(validateCoverage(records, changed), []);
  assert.notEqual(validateCoverage(null, documents).length, 0);
  assert.notEqual(validateCoverage([null], documents).length, 0);
});
test('checks actual method, model and test associations rather than accepting invented names', async () => {
  const directory = await mkdtemp(join(tmpdir(),'accountkit-coverage-'));
  try {
    await writeFile(join(directory,'client.ts'),'export interface User {}\nexport function getMe():User { return {}; }\n');
    await writeFile(join(directory,'test.ts'),"test('getMe',()=>{});\n");
    const record = {...records[0],sdkSourceFile:'client.ts',modelSourceFile:'client.ts',tests:[{file:'test.ts',name:'getMe'}]};
    assert.deepEqual(await validateCoverageLinks([record],directory),[]);
    assert.match((await validateCoverageLinks([{...record,sdkMethod:'doesNotExist'}],directory)).join('\n'),/SDK method/);
    assert.match((await validateCoverageLinks([{...record,models:['MissingModel']}],directory)).join('\n'),/model/);
    assert.match((await validateCoverageLinks([{...record,tests:[{file:'missing.ts',name:'getMe'}]}],directory)).join('\n'),/test file/);
    assert.match((await validateCoverageLinks([{...record,tests:[{file:'test.ts',name:'missingCase'}]}],directory)).join('\n'),/test identifier/);
    assert.match((await validateCoverageLinks([{...record,sdkSourceFile:'../outside.ts'}],directory)).join('\n'),/path/);
  } finally { await rm(directory,{recursive:true,force:true}); }
});
