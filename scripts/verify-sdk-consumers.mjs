import { execFileSync, spawn } from 'node:child_process';
import { mkdir, mkdtemp, readFile, realpath, rm, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { dirname, join, relative, resolve } from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';
import { createSDKSnapshot } from './sdk-git-snapshot.mjs';
import { isFixedRef } from './check-sdk-release.mjs';

const root = resolve(dirname(fileURLToPath(import.meta.url)), '..');
export function dependencySource(sdk, source, ref) {
  if (!isFixedRef(ref, sdk)) throw new Error('a full commit SHA or matching SDK release tag is required');
  const url = new URL(source);
  if (url.username || url.password || url.search || url.hash || !((url.protocol === 'https:') || (url.protocol === 'git:' && ['127.0.0.1','[::1]','localhost'].includes(url.hostname)))) {
    throw new Error('Git URL must use HTTPS or the loopback test daemon, without credentials/query/fragment');
  }
  if (sdk === 'flutter') return { git: { url: source, ref, path: 'sdks/accountkit-flutter' } };
  return `${url.protocol === 'https:' ? 'git+' : ''}${source}#${ref}&path:/sdks/accountkit-admin-ts`;
}
export function parseConsumerOptions(args) {
  const result = { sdk: 'all', platform: 'none' };
  const flags = { '--sdk': 'sdk', '--platform': 'platform', '--git-url': 'url', '--ref': 'ref' };
  for (let i = 0; i < args.length; i += 2) {
    const key = flags[args[i]];
    if (!key || !args[i+1] || args[i+1].startsWith('--')) throw new Error('usage: --sdk all|flutter|admin-ts --platform none|android|ios [--git-url URL --ref FIXED_REF]');
    result[key] = args[i+1];
  }
  if (!['all','flutter','admin-ts'].includes(result.sdk) || !['none','android','ios'].includes(result.platform)) throw new Error('unsupported SDK or platform');
  if (result.sdk === 'admin-ts' && result.platform !== 'none') throw new Error('native platform builds require Flutter');
  if (Boolean(result.url) !== Boolean(result.ref)) throw new Error('remote mode requires both --git-url and --ref');
  return result;
}

let operationSignal;
export function runConsumerCommand(command, args, cwd, signal) {
  if (signal?.aborted) return Promise.reject(new Error('SDK consumer validation cancelled'));
  let windowsVerbatimArguments = false;
  // Windows batch launch is limited to generated arguments; never interpolate Git URLs.
  if (process.platform === 'win32' && /\.(cmd|bat)$/i.test(command)) {
    if (!/[\\/]/.test(command)) command = execFileSync('where.exe',[command],{encoding:'utf8',windowsHide:true}).trim().split(/\r?\n/)[0];
    const values = [command, ...args];
    if (values.some(value => /["%!\r\n]/.test(value))) throw new Error('unsupported Windows tool argument');
    args = ['/d','/v:off','/s','/c', `"${values.map(value => `"${value}"`).join(' ')}"`];
    command = process.env.ComSpec || 'cmd.exe';
    windowsVerbatimArguments = true;
  }
  return new Promise((accept, reject) => {
    const child = spawn(command, args, { cwd, stdio: 'inherit', windowsHide: true, windowsVerbatimArguments, detached:process.platform !== 'win32' });
    let cancelCompletion=Promise.resolve();
    const cancel = () => {
      if (!child.pid || child.exitCode !== null || child.signalCode !== null) return;
      if (process.platform === 'win32') {
        try { execFileSync('taskkill',['/PID',String(child.pid),'/T','/F'],{windowsHide:true,stdio:'ignore'}); }
        catch { child.kill(); }
      } else {
        // A descendant can ignore SIGTERM even after the group leader exits.
        // Keep the group escalation alive and await it before deleting files.
        cancelCompletion=new Promise(done=>setTimeout(()=>{
          try {process.kill(-child.pid,'SIGKILL');} catch { /* group already exited */ }
          done();
        },1000));
        try {process.kill(-child.pid,'SIGTERM');} catch {child.kill();}
      }
    };
    signal?.addEventListener('abort',cancel,{once:true});
    if (signal?.aborted) cancel();
    child.once('error',error=>{signal?.removeEventListener('abort',cancel);reject(error);});
    child.once('exit', async (code, exitSignal) => {
      signal?.removeEventListener('abort',cancel);
      await cancelCompletion;
      if(signal?.aborted) reject(new Error('SDK consumer validation cancelled'));
      else if(code === 0) accept();
      else reject(new Error(`consumer command failed (exit ${code}, signal ${exitSignal ?? 'none'})`));
    });
  });
}
const run=(command,args,cwd)=>runConsumerCommand(command,args,cwd,operationSignal);
const flutter = (...args) => run(process.env.SDK_FLUTTER_BIN || (process.platform === 'win32' ? 'flutter.bat' : 'flutter'), args.slice(1), args[0]);
const pnpm = (cwd, args, versions) => process.env.SDK_PNPM_CLI
  ? run(process.execPath, [process.env.SDK_PNPM_CLI, ...args], cwd)
  : run(process.platform === 'win32' ? 'npm.cmd' : 'npm', ['exec','--yes',`--package=pnpm@${versions.pnpm}`,'--','pnpm',...args], cwd);
const jsonFile = (path, value) => writeFile(path, `${JSON.stringify(value, null, 2)}\n`);

async function adminConsumer(directory, url, ref, versions) {
  await mkdir(directory);
  await jsonFile(join(directory, 'package.json'), {
    name: 'accountkit-admin-isolated-consumer', version: '1.0.0', private: true, type: 'module',
    packageManager: `pnpm@${versions.pnpm}`,
    scripts: { typecheck: 'tsc --noEmit', test: 'vitest run', build: 'vite build' },
    dependencies: { '@accountkit/admin-client': dependencySource('admin-ts', url, ref) },
    devDependencies: { typescript: versions.typescript, vite: versions.vite, vitest: versions.vitest },
  });
  await writeFile(join(directory, 'pnpm-workspace.yaml'), 'allowBuilds:\n  esbuild: true\n');
  await jsonFile(join(directory, 'tsconfig.json'), { compilerOptions: { target:'ES2022', module:'ESNext', moduleResolution:'Bundler', lib:['ES2022','DOM'], strict:true, noEmit:true, isolatedModules:true, skipLibCheck:true }, include:['*.ts'] });
  await writeFile(join(directory, 'index.html'), '<!doctype html><html lang="zh"><title>SDK consumer</title><body><script type="module" src="/main.ts"></script></body></html>\n');
  await writeFile(join(directory, 'main.ts'), `import { createAccountAdminClient } from '@accountkit/admin-client';\nexport const client = createAccountAdminClient({baseUrl:'https://account.example.invalid',getAccessToken:()=> 'synthetic-consumer-token'});\n`);
  await writeFile(join(directory, 'client.test.ts'), `import { expect, test } from 'vitest';
import { createAccountAdminClient, userFilter } from '@accountkit/admin-client';
test('installed Git package executes real protocol client',async()=>{
 let count=0;
 const client=createAccountAdminClient({baseUrl:'https://account.example.invalid',getAccessToken:()=> 'synthetic',fetch:async(url,init)=>{
  count++;expect(new URL(String(url)).pathname).toBe('/admin/v1/users');
  expect(new Headers(init?.headers).get('Authorization')).toBe('Bearer synthetic');
  expect(new URL(String(url)).searchParams.get('filter')).toBe('state = "ACTIVE"');
  return Response.json({users:[],next_page_token:''});
 }});
 expect(await client.listUsers({filter:userFilter.eq('state','ACTIVE')})).toEqual({users:[],next_page_token:''});expect(count).toBe(1);
});
`);
  await pnpm(directory, ['install'], versions);
  await pnpm(directory, ['install','--frozen-lockfile'], versions);
  await pnpm(directory, ['typecheck'], versions);
  await pnpm(directory, ['test'], versions);
  await pnpm(directory, ['build'], versions);
}

async function flutterConsumer(directory, url, ref, platform) {
  await flutter(dirname(directory), 'create','--no-pub','--platforms=android,ios','--project-name=accountkit_sdk_consumer', directory);
  // JSON is valid YAML; pub resolves only this fixed Git subdirectory dependency.
  await jsonFile(join(directory, 'pubspec.yaml'), {
    name:'accountkit_sdk_consumer', version:'1.0.0', publish_to:'none',
    environment:{sdk:'>=3.13.0 <4.0.0'},
    dependencies:{flutter:{sdk:'flutter'},accountkit_flutter:dependencySource('flutter',url,ref)},
    dev_dependencies:{flutter_test:{sdk:'flutter'}}, flutter:{'uses-material-design':true},
  });
  await writeFile(join(directory, 'analysis_options.yaml'), 'analyzer:\n  language:\n    strict-casts: true\n');
  await writeFile(join(directory, 'lib/main.dart'), `import 'package:flutter/material.dart';
import 'package:accountkit_flutter/accountkit_flutter.dart';
void main() => runApp(MaterialApp(home: Scaffold(body: Text(authenticationStateForScope('user') is SignedIn ? 'Account SDK ready' : 'Signed out'))));
`);
  await writeFile(join(directory, 'test/widget_test.dart'), `import 'package:flutter_test/flutter_test.dart';
import 'package:accountkit_flutter/accountkit_flutter.dart';
void main() {
 test('installed Git package preserves scope boundary', () {
  expect(authenticationStateForScope('user'), isA<SignedIn>());
  expect(authenticationStateForScope('user:bind'), isA<NeedsBinding>());
  expect(authenticationStateForScope('user:undelete'), isA<PendingDeletion>());
  expect(() => authenticationStateForScope('unknown'), throwsA(isA<AccountProtocolError>()));
 });
}
`);
  await flutter(directory, 'pub','get');
  await flutter(directory, 'pub','get','--enforce-lockfile');
  await flutter(directory, 'analyze');
  await flutter(directory, 'test');
  if (platform === 'android') await flutter(directory, 'build','apk','--debug');
  if (platform === 'ios') await flutter(directory, 'build','ios','--no-codesign');
}

async function main() {
  const options = parseConsumerOptions(process.argv.slice(2));
  if (options.url) for (const sdk of options.sdk === 'all' ? ['flutter','admin-ts'] : [options.sdk]) dependencySource(sdk, options.url, options.ref);
  const versions = JSON.parse(await readFile(join(root, 'sdks/tool-versions.json'),'utf8'));
  const cancellation = new AbortController();
  operationSignal = cancellation.signal;
  const cancel = () => cancellation.abort();
  process.on('SIGINT',cancel);
  process.on('SIGTERM',cancel);
  let snapshot;
  const directory = await mkdtemp(join(tmpdir(), 'accountkit-sdk-consumers-'));
  const owned = await realpath(directory);
  try {
    if (!options.url) snapshot = await createSDKSnapshot(root);
    const url = options.url || snapshot.url, ref = options.ref || snapshot.ref;
    process.stdout.write(`SDK consumer validation: ${options.url ? 'remote Git' : 'local Git snapshot'}; ref=${ref}; platform=${options.platform}\n`);
    if (options.sdk !== 'flutter') await adminConsumer(join(directory,'admin'),url,ref,versions);
    if (options.sdk !== 'admin-ts') await flutterConsumer(join(directory,'flutter'),url,ref,options.platform);
    process.stdout.write(`SDK isolated consumers passed (${options.sdk}, native platform=${options.platform}).\n`);
  } finally {
    try {
      await snapshot?.dispose();
      if (await realpath(directory) !== owned || !relative(await realpath(tmpdir()),owned).startsWith('accountkit-sdk-consumers-')) throw new Error('refusing to clean unexpected consumer directory');
      await rm(directory,{recursive:true,force:true});
    } finally {
      process.removeListener('SIGINT',cancel);
      process.removeListener('SIGTERM',cancel);
      operationSignal=undefined;
    }
  }
}
if (process.argv[1] && import.meta.url === pathToFileURL(resolve(process.argv[1])).href) {
  main().catch(error=>{process.stderr.write(`SDK consumer validation failed: ${error.message}\n`);process.exitCode=1;});
}
