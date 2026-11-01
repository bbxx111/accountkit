import { readFile } from 'node:fs/promises';
import { dirname, isAbsolute, relative, resolve } from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';

const methods = new Set(['get', 'post', 'put', 'patch', 'delete', 'head', 'options', 'trace']);
const nonempty = value => typeof value === 'string' && value.trim().length > 0;

/** Compare SDK registrations with the actual protocol operation set. */
export function validateCoverage(records, documents) {
  if (!Array.isArray(records)) return ['coverage must be an array'];
  const errors = [];
  const expected = new Map();
  for (const [surface, document] of Object.entries(documents)) {
    for (const [path, operations] of Object.entries(document.paths ?? {})) {
      for (const [method, operation] of Object.entries(operations)) {
        if (!methods.has(method)) continue;
        if (!nonempty(operation.operationId)) {
          errors.push(`OpenAPI operationId missing: ${surface} ${method} ${path}`);
          continue;
        }
        const key = `${surface}:${operation.operationId}`;
        if (expected.has(key)) errors.push(`duplicate OpenAPI operation: ${key}`);
        expected.set(key, { method: method.toUpperCase(), path });
      }
    }
  }
  const seen = new Set();
  for (const record of records) {
    if (!record || typeof record !== 'object') {
      errors.push('coverage record must be an object');
      continue;
    }
    const key = `${record.surface}:${record.operationId}`;
    if (!Object.hasOwn(documents, record.surface)) errors.push(`unknown surface: ${key}`);
    if (seen.has(key)) errors.push(`duplicate operation: ${key}`);
    seen.add(key);
    const operation = expected.get(key);
    if (!operation) errors.push(`extra operation: ${key}`);
    else if (record.method !== operation.method || record.path !== operation.path) {
      errors.push(`route mismatch: ${key}; expected ${operation.method} ${operation.path}`);
    }
    for (const field of ['sdkMethod', 'baseline']) {
      if (!nonempty(record[field])) errors.push(`${key}: ${field} is required`);
    }
    if (!Array.isArray(record.models) || !record.models.length || !record.models.every(nonempty)) {
      errors.push(`${key}: models must identify request/response models (Void for empty bodies)`);
    }
    if (!Array.isArray(record.tests) || !record.tests.length || !record.tests.every(test => test && nonempty(test.file) && nonempty(test.name))) {
      errors.push(`${key}: tests must identify test files and names`);
    }
    if (record.optionalCapability !== null && !nonempty(record.optionalCapability)) {
      errors.push(`${key}: optionalCapability must be null or a reason string`);
    }
  }
  for (const key of expected.keys()) if (!seen.has(key)) errors.push(`missing operation: ${key}`);
  return errors;
}

/** Verify registered source/test links in addition to the protocol operation set. */
export async function validateCoverageLinks(records, root) {
  const errors = [];
  const cache = new Map();
  async function source(path, label) {
    if (!nonempty(path) || isAbsolute(path) || path.includes('\\') || path.split('/').includes('..') || relative(root,resolve(root,path)).startsWith('..')) {
      errors.push(`${label}: unsafe or missing path`); return '';
    }
    if (!cache.has(path)) {
      try { cache.set(path,await readFile(resolve(root,path),'utf8')); }
      catch { errors.push(`${label}: missing file ${path}`); return ''; }
    }
    return cache.get(path);
  }
  function hasSymbol(text, symbol) {
    return typeof symbol === 'string' && /^[A-Za-z][A-Za-z0-9_]*$/.test(symbol) && new RegExp(`\\b${symbol}\\b`).test(text);
  }
  for (const record of records) {
    const key = `${record.surface}:${record.operationId}`;
    const client = await source(record.sdkSourceFile,`${key} SDK source`);
    if (!hasSymbol(client,record.sdkMethod)) errors.push(`${key}: SDK method not found: ${record.sdkMethod}`);
    const models = await source(record.modelSourceFile,`${key} model source`);
    for (const model of record.models ?? []) if (model !== 'void' && !hasSymbol(models,model)) errors.push(`${key}: SDK model not found: ${model}`);
    for (const test of record.tests ?? []) {
      const content = await source(test.file,`${key} test file`);
      if (!nonempty(test.name) || !content.includes(test.name)) errors.push(`${key}: test identifier not found: ${test.name}`);
    }
  }
  return errors;
}

async function main() {
  const root = resolve(dirname(fileURLToPath(import.meta.url)), '..');
  const args = process.argv.slice(2);
  if (args.length > 1) throw new Error('usage: node scripts/check-sdk-coverage.mjs [coverage.json]');
  const readJSON = async path => JSON.parse(await readFile(path, 'utf8'));
  const [records, enduser, admin] = await Promise.all([
    readJSON(args.length ? resolve(args[0]) : resolve(root, 'sdk/http-coverage.json')),
    readJSON(resolve(root, 'docs/openapi/enduser.json')),
    readJSON(resolve(root, 'docs/openapi/admin.json')),
  ]);
  const errors = validateCoverage(records, { enduser, admin });
  if (!errors.length) errors.push(...await validateCoverageLinks(records,root));
  if (errors.length) {
    process.stderr.write(`${errors.join('\n')}\n`);
    process.exitCode = 1;
    return;
  }
  process.stdout.write(`SDK HTTP coverage: ${records.length} operations match OpenAPI.\n`);
}

if (process.argv[1] && import.meta.url === pathToFileURL(resolve(process.argv[1])).href) {
  main().catch(error => { process.stderr.write(`SDK coverage failed: ${error.message}\n`); process.exitCode = 1; });
}
