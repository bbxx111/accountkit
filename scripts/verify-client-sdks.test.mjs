import {test} from 'node:test';
import assert from 'node:assert/strict';
import {readFile,mkdtemp,writeFile,rm} from 'node:fs/promises';
import {tmpdir} from 'node:os';
import {join} from 'node:path';
import {createServer} from 'node:net';
import {spawn,execFileSync} from 'node:child_process';

const bash = process.platform === 'win32' ? 'C:/Program Files/Git/bin/bash.exe' : 'bash';
async function definitions() {
  const source = await readFile(new URL('./verify-client-sdks.sh', import.meta.url), 'utf8');
  return source.slice(source.indexOf("service_pid=''"), source.indexOf('# env -u'));
}
function run(source, timeout = 5000) {
  return new Promise((resolve, reject) => {
    const child = spawn(bash, ['-c',source], {stdio:['ignore','pipe','pipe'],detached:process.platform !== 'win32'});
    let output = '';
    child.stdout.on('data',data=>{output+=data;});
    child.stderr.on('data',data=>{output+=data;});
    const timer = setTimeout(()=>{
      // Clean the whole test-owned process tree if a regression hangs the helper.
      if (process.platform === 'win32') {
        try {execFileSync('taskkill',['/PID',String(child.pid),'/T','/F'],{stdio:'ignore'});} catch {child.kill('SIGKILL');}
      } else {
        try {process.kill(-child.pid,'SIGKILL');} catch {child.kill('SIGKILL');}
      }
      reject(new Error(`bounded helper timed out: ${output}`));
    },timeout);
    child.once('error',error=>{clearTimeout(timer);reject(error);});
    child.once('exit',(code)=>{clearTimeout(timer);resolve({code,output});});
  });
}
test('policy seed uses validated readiness metadata and preserves the final legacy marker match', async () => {
  const directory = await mkdtemp(join(tmpdir(),'accountkit-seed-marker-'));
  const quote = value => `'${value.replaceAll('\\','/').replaceAll("'", "'\\''")}'`;
  try {
    const metadata = join(directory,'ready.json');
    const log = join(directory,'seed.log');
    await writeFile(metadata,JSON.stringify({policy_seed_marker:'SDK_POLICY_SEED_USER_ID='}));
    await writeFile(log,'ignored\nSDK_POLICY_SEED_USER_ID=u_0000000000001\nprefix SDK_POLICY_SEED_USER_ID=u_0000000000002 suffix\n');
    const setup = `set -eu\noutput=unused\n${await definitions()}\n`;
    const result = await run(`${setup}marker=$(read_policy_seed_marker ${quote(metadata)})\nextract_policy_seed_user_id "$marker" ${quote(log)}`);
    assert.equal(result.code,0,result.output);
    assert.equal(result.output,'u_0000000000002\n');
    for (const value of ['unexpected.*','SDK_POLICY_SEED_USER_ID=\n',null,42]) {
      await writeFile(metadata,JSON.stringify({policy_seed_marker:value}));
      const invalid = await run(`${setup}read_policy_seed_marker ${quote(metadata)}`);
      assert.notEqual(invalid.code,0);
    }
  } finally { await rm(directory,{recursive:true,force:true}); }
});
test('silent HTTP peer cannot hang readiness and TERM cancels the in-flight probe', async () => {
  const sockets = new Set();
  const server = createServer(socket=>{sockets.add(socket);socket.on('error',()=>{});socket.on('close',()=>sockets.delete(socket));});
  await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
  try {
    const origin = `http://127.0.0.1:${server.address().port}`;
    const setup = `set -eu\noutput=unused\n${await definitions()}\n`;
    const failed = await run(`${setup}wait_ready '${origin}' $$ 1 0.2 0.1`);
    assert.equal(failed.code,1);
    assert.match(failed.output,/readiness timeout/);
    const cancelled = await run(`${setup}(sleep 0.2; kill -TERM $$) &\nwait_ready '${origin}' $$ 120 30 1`);
    assert.equal(cancelled.code,143);
  } finally {
    for (const socket of sockets) socket.destroy();
    await new Promise(resolve=>server.close(resolve));
  }
});
test('fixture teardown escalates an owned child ignoring TERM', async () => {
  const setup = `set -eu\noutput=unused\n${await definitions()}\n`;
  const result = await run(`${setup}bash -c 'trap "" TERM; while :; do sleep 0.1; done' &\nowned=$!\nsleep 0.2\nstop_fixture "$owned" 2 0.1\n! kill -0 "$owned" 2>/dev/null`);
  assert.equal(result.code,0,result.output);
});
