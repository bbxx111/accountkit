import { execFileSync, spawn } from 'node:child_process';
import { copyFile, lstat, mkdir, mkdtemp, realpath, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { dirname, isAbsolute, join, relative, resolve } from 'node:path';
import { createConnection, createServer } from 'node:net';
import { setTimeout as delay } from 'node:timers/promises';

function git(cwd, ...args) {
  return execFileSync('git', args, { cwd, encoding: 'utf8', windowsHide: true, stdio: ['ignore', 'pipe', 'pipe'] });
}
async function unusedPort() {
  const server = createServer();
  await new Promise((accept, reject) => {
    server.once('error', reject);
    server.listen(0, '127.0.0.1', accept);
  });
  const port = server.address().port;
  await new Promise((accept, reject) => server.close(error => error ? reject(error) : accept()));
  return port;
}
async function listening(port) {
  return new Promise(accept => {
    const socket = createConnection({ host: '127.0.0.1', port });
    socket.setTimeout(100);
    socket.once('connect', () => { socket.destroy(); accept(true); });
    socket.once('error', () => { socket.destroy(); accept(false); });
    socket.once('timeout', () => { socket.destroy(); accept(false); });
  });
}

/** Creates an owned, loopback-only Git export. It never changes the source index or refs. */
export async function createSDKSnapshot(sourceDirectory) {
  const source = await realpath(sourceDirectory);
  const directory = await mkdtemp(join(tmpdir(), 'accountkit-sdk-snapshot-'));
  const owned = await realpath(directory);
  let daemon;
  let disposed = false;
  async function dispose() {
    if (disposed) return;
    disposed = true;
    if (daemon?.pid && daemon.exitCode === null && daemon.signalCode === null) {
      await new Promise(accept => {
        daemon.once('exit', accept);
        daemon.kill();
      });
    }
    daemon?.stderr?.destroy();
    // Delete only this invocation's verified temporary root, never a caller path.
    if (await realpath(directory) !== owned || !relative(await realpath(tmpdir()), owned).startsWith('accountkit-sdk-snapshot-')) {
      throw new Error('refusing to clean an unexpected snapshot directory');
    }
    await rm(directory, { recursive: true, force: true });
  }
  try {
    const working = join(directory, 'working');
    await mkdir(working);
    const files = git(source, 'ls-files', '--cached', '--others', '--exclude-standard', '-z', '--', 'sdk/').split('\0').filter(Boolean);
    if (!files.length) throw new Error('no SDK files to snapshot');
    for (const file of files) {
      if (isAbsolute(file) || file.split(/[\\/]/).includes('..') || !file.startsWith('sdk/')) throw new Error('unsafe SDK snapshot path');
      const original = resolve(source, file);
      let info;
      try { info = await lstat(original); } catch (error) { if (error.code === 'ENOENT') continue; throw error; }
      if (!info.isFile() || info.isSymbolicLink()) throw new Error('SDK snapshot accepts regular files only');
      const canonical = await realpath(original);
      const rel = relative(source, canonical);
      if (rel.startsWith('..') || isAbsolute(rel)) throw new Error('SDK file resolves outside source');
      const target = join(working, file);
      await mkdir(dirname(target), { recursive: true });
      await copyFile(original, target);
    }
    git(working, 'init', '-q', '-b', 'sdk-verification');
    git(working, 'config', 'user.name', 'accountkit SDK verification');
    git(working, 'config', 'user.email', 'sdk-verification@example.invalid');
    git(working, 'config', 'core.autocrlf', 'false');
    git(working, 'add', '--all');
    git(working, '-c', 'commit.gpgsign=false', 'commit', '-q', '-m', 'test: isolated SDK snapshot');
    const ref = git(working, 'rev-parse', 'HEAD').trim();
    const repository = join(directory, 'snapshot.git');
    git(directory, 'clone', '--bare', '-q', working, repository);
    const port = await unusedPort();
    let spawnError;
    daemon = spawn('git', ['daemon', '--reuseaddr', '--export-all', '--strict-paths', `--base-path=${directory.replaceAll('\\', '/')}`, '--listen=127.0.0.1', `--port=${port}`, repository.replaceAll('\\', '/')], { windowsHide: true, stdio: 'ignore' });
    daemon.once('error', error => { spawnError = error; });
    for (let attempt = 0; attempt < 100; attempt++) {
      if (spawnError) throw spawnError;
      if (daemon.exitCode !== null) throw new Error('local Git daemon exited before readiness');
      if (await listening(port)) return { directory, ref, url: `git://127.0.0.1:${port}/snapshot.git`, dispose };
      await delay(50);
    }
    throw new Error('local Git daemon readiness timed out');
  } catch (error) {
    await dispose();
    throw error;
  }
}
