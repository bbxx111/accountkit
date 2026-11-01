import assert from 'node:assert/strict';
import { test } from 'node:test';
import { dependencySource, parseConsumerOptions, runConsumerCommand } from './verify-sdk-consumers.mjs';
import { mkdtemp, readFile, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { setTimeout as delay } from 'node:timers/promises';
const sha = '6e17c77ba1c45a0ccedbf449cfae6b4e69654476';
test('generates pnpm and pub fixed Git subdirectory dependency sources', () => {
  assert.equal(dependencySource('admin-ts', 'https://example.test/accountkit.git', sha), `git+https://example.test/accountkit.git#${sha}&path:/sdks/accountkit-admin-ts`);
  assert.deepEqual(dependencySource('flutter', 'https://example.test/accountkit.git', sha), { git: { url: 'https://example.test/accountkit.git', ref: sha, path: 'sdks/accountkit-flutter' } });
});
test('rejects floating refs, URL credentials and non-local unencrypted sources', () => {
  for (const [url, ref] of [['https://example.test/repo', 'develop'], ['https://token@example.test/repo', sha], ['git://external.test/repo', sha], ['file:///worktree/repo', sha], ['https://example.test/repo#main', sha]]) {
    assert.throws(() => dependencySource('admin-ts', url, ref));
  }
  assert.match(dependencySource('admin-ts', 'git://127.0.0.1:1234/snapshot.git', sha), /path:\/sdks\/accountkit-admin-ts$/);
});
test('remote mode requires both URL and immutable ref and validates platform choices', () => {
  assert.deepEqual(parseConsumerOptions(['--sdk','admin-ts']), {sdk:'admin-ts', platform:'none'});
  assert.throws(() => parseConsumerOptions(['--git-url','https://example.test/repo']));
  assert.throws(() => parseConsumerOptions(['--ref',sha]));
  assert.throws(() => parseConsumerOptions(['--platform','web']));
  assert.throws(() => parseConsumerOptions(['--sdk','admin-ts','--platform','ios']));
});
test('cancelling a running consumer terminates the owned command before cleanup', async () => {
  const directory = await mkdtemp(join(tmpdir(),'accountkit-consumer-cancel-'));
  const marker = join(directory,'pid');
  const controller = new AbortController();
  try {
    const pending = runConsumerCommand(process.execPath,['-e',"require('node:fs').writeFileSync(process.argv[1],String(process.pid));setInterval(()=>{},1000)",marker],directory,controller.signal);
    let pid;
    for(let attempt=0;attempt<100&&!pid;attempt++){try{pid=Number(await readFile(marker,'utf8'));}catch{await delay(20);}}
    assert.ok(pid,'child reached its wait state');
    controller.abort();
    await assert.rejects(pending,/cancelled/);
    assert.throws(()=>process.kill(pid,0),'owned child must have exited');
  } finally {controller.abort();await rm(directory,{recursive:true,force:true});}
});
test('POSIX cancellation escalates when a consumer ignores SIGTERM', {skip:process.platform==='win32'}, async () => {
  const directory=await mkdtemp(join(tmpdir(),'accountkit-consumer-stubborn-'));
  const marker=join(directory,'pid');const controller=new AbortController();
  try {
    const pending=runConsumerCommand(process.execPath,['-e',"process.on('SIGTERM',()=>{});require('node:fs').writeFileSync(process.argv[1],String(process.pid));setInterval(()=>{},1000)",marker],directory,controller.signal);
    let pid;
    for(let i=0;i<100&&!pid;i++){try{pid=Number(await readFile(marker,'utf8'));}catch{await delay(20);}}
    assert.ok(pid);const start=Date.now();controller.abort();
    await assert.rejects(pending,/cancelled/);assert.ok(Date.now()-start<5000);
    assert.throws(()=>process.kill(pid,0));
  } finally {controller.abort();await rm(directory,{recursive:true,force:true});}
});
