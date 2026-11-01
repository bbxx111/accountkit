import assert from 'node:assert/strict';
import { test } from 'node:test';
import { mkdtemp, mkdir, writeFile, readFile, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { execFileSync } from 'node:child_process';
import { createSDKSnapshot } from './sdk-git-snapshot.mjs';

test('exports uncommitted SDK content by fixed Git SHA without touching source branch or tags', async () => {
  const source = await mkdtemp(join(tmpdir(), 'accountkit-sdk-source-test-'));
  let snapshot;
  const git = (...args) => execFileSync('git', args, { cwd: source, encoding: 'utf8', windowsHide: true }).trim();
  try {
    git('init', '-q', '-b', 'source');
    await mkdir(join(source, 'sdks/sample'), { recursive: true });
    await writeFile(join(source, 'sdks/sample/package.json'), '{"name":"sample","version":"1.0.0"}\n');
    await writeFile(join(source, '.gitignore'), 'sdks/sample/node_modules/\n');
    await mkdir(join(source, 'sdks/sample/node_modules'), { recursive: true });
    await writeFile(join(source, 'sdks/sample/node_modules/not-distributed'), 'ignored');
    const before = git('status', '--porcelain');
    snapshot = await createSDKSnapshot(source);
    assert.match(snapshot.ref, /^[a-f0-9]{40}$/);
    assert.match(snapshot.url, /^git:\/\/127\.0\.0\.1:\d+\/snapshot\.git$/);
    assert.equal(git('branch', '--show-current'), 'source');
    assert.equal(git('status', '--porcelain'), before);
    assert.equal(git('tag', '--list'), '');
    const consumer = join(snapshot.directory, 'consumer');
    execFileSync('git', ['clone', '-q', snapshot.url, consumer], { windowsHide: true });
    const actual = execFileSync('git', ['rev-parse', 'HEAD'], { cwd: consumer, encoding: 'utf8', windowsHide: true }).trim();
    assert.equal(actual, snapshot.ref);
    assert.match(await readFile(join(consumer, 'sdks/sample/package.json'), 'utf8'), /sample/);
    await assert.rejects(readFile(join(consumer, 'sdks/sample/node_modules/not-distributed')));
  } finally {
    await snapshot?.dispose();
    await rm(source, { recursive: true, force: true });
  }
});
