import {readFileSync} from 'node:fs';
import {execFileSync} from 'node:child_process';
import {pathToFileURL} from 'node:url';

export function validateToolchain(versions, outputs) {
  // A fresh Flutter checkout prints SDK/bootstrap progress before --machine JSON.
  // Parse the complete JSON suffix; missing, malformed or trailing output still fails.
  const jsonStart = outputs.flutter.search(/^\s*\{/m);
  if (jsonStart < 0) throw new Error('Flutter machine version JSON missing; SDK checks cannot skip');
  const flutter = JSON.parse(outputs.flutter.slice(jsonStart));
  const actual = {node:outputs.node.trim().replace(/^v/,''),pnpm:outputs.pnpm.trim(),flutter:flutter.frameworkVersion,dart:flutter.dartSdkVersion};
  for (const name of ['node','pnpm','flutter','dart']) {
    if (actual[name] !== versions[name]) throw new Error(`${name}: expected ${versions[name]}, got ${actual[name] ?? 'missing'}; SDK checks cannot skip`);
  }
  return actual;
}
if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  try {
    const versions = JSON.parse(readFileSync(new URL('../sdk/tool-versions.json',import.meta.url),'utf8'));
    const outputs = {node:process.version,pnpm:execFileSync('pnpm',['--version'],{encoding:'utf8'}),flutter:execFileSync('flutter',['--version','--machine'],{encoding:'utf8'})};
    console.log(JSON.stringify(validateToolchain(versions,outputs)));
  } catch (error) { console.error(`SDK toolchain check failed: ${error.message}`); process.exitCode = 1; }
}
