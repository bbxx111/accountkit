import { readFile } from 'node:fs/promises';
import { dirname, resolve } from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';

const versionPattern = '(?:0|[1-9][0-9]*)\\.(?:0|[1-9][0-9]*)\\.(?:0|[1-9][0-9]*)';
const stableVersion = new RegExp(`^${versionPattern}$`);
const packages = new Set(['flutter', 'admin-ts']);

export function validateSDKRelease(sdk, version, tag) {
  const errors = [];
  if (!packages.has(sdk)) errors.push('unknown SDK; expected flutter or admin-ts');
  if (typeof version !== 'string' || !stableVersion.test(version) || version.includes('\n')) errors.push('manifest version must be a stable SemVer');
  if (tag !== `sdk/${sdk}/v${version}`) errors.push('release tag does not match SDK manifest');
  return errors;
}

export function isFixedRef(ref, sdk) {
  if (!packages.has(sdk) || typeof ref !== 'string' || ref.includes('\n')) return false;
  return /^[a-f0-9]{40}$/.test(ref) || new RegExp(`^sdk/${sdk}/v${versionPattern}$`).test(ref);
}

export function validateCompatibility(matrix,sdk,version,requireVerified=true) {
  if(!Array.isArray(matrix)) return ['compatibility matrix must be an array'];
  const rows=matrix.filter(row=>row?.sdk===sdk&&row.sdkVersion===version);
  if(!rows.length) return ['SDK manifest version is absent from compatibility matrix'];
  const errors=[];
  for(const row of rows){
    if(typeof row.serviceVersion!=='string'||!/^v/.test(row.serviceVersion)||!stableVersion.test(row.serviceVersion.slice(1))) errors.push('compatibility service version must be fixed SemVer');
    if(typeof row.evidence!=='string'||!row.evidence.startsWith('openspec/')||row.evidence.split('/').includes('..')) errors.push('compatibility evidence must identify the change verification record');
    if(!['pending','partial','verified'].includes(row.verification)) errors.push('unknown compatibility verification status');
    if(requireVerified&&row.verification!=='verified') errors.push('service compatibility is not verified; release readiness is blocked');
  }
  return errors;
}

async function main() {
  const [sdk, tag, ...extra] = process.argv.slice(2);
  if (!packages.has(sdk) || !tag || (extra.length && (extra.length!==1||extra[0]!=='--metadata-only'))) throw new Error('usage: node scripts/check-sdk-release.mjs <flutter|admin-ts> <sdk/package/vX.Y.Z> [--metadata-only]');
  const root = resolve(dirname(fileURLToPath(import.meta.url)), '..');
  const manifest = await readFile(resolve(root, sdk === 'flutter' ? 'sdk/accountkit-flutter/pubspec.yaml' : 'sdk/accountkit-admin-ts/package.json'), 'utf8');
  const version = sdk === 'flutter' ? /^version:\s*(\S+)\s*$/m.exec(manifest)?.[1] : JSON.parse(manifest).version;
  const errors = validateSDKRelease(sdk, version, tag);
  const matrix=JSON.parse(await readFile(resolve(root,'sdk/compatibility.json'),'utf8'));
  errors.push(...validateCompatibility(matrix,sdk,version,extra.length===0));
  for(const row of matrix.filter(row=>row.sdk===sdk&&row.sdkVersion===version)) {
    if(typeof row.evidence==='string'&&row.evidence.startsWith('openspec/')&&!row.evidence.split('/').includes('..')) await readFile(resolve(root,row.evidence),'utf8');
  }
  if (errors.length) throw new Error(errors.join('; '));
  process.stdout.write(`SDK manifest/tag/compatibility metadata match: ${tag}. ${extra.length?'Metadata only; service readiness was not asserted. ':''}No tag was created or pushed; native platform and release gates remain separate.\n`);
}
if (process.argv[1] && import.meta.url === pathToFileURL(resolve(process.argv[1])).href) {
  main().catch(error => { process.stderr.write(`SDK release check failed: ${error.message}\n`); process.exitCode = 1; });
}
