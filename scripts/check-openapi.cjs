// 无运行时依赖的契约漂移检查。正式 OpenAPI schema 校验另见 docs/openapi.md。
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const root = path.resolve(__dirname, '..');
const read = file => fs.readFileSync(path.join(root, file), 'utf8');
const methods = new Set(['get', 'post', 'patch', 'delete', 'put', 'options', 'head', 'trace']);

function resolve(doc, pointer) {
  assert.ok(pointer.startsWith('#/'), `只接受文档内引用：${pointer}`);
  return pointer.slice(2).split('/').map(s => s.replace(/~1/g, '/').replace(/~0/g, '~'))
    .reduce((node, key) => {
      assert.ok(node && Object.hasOwn(node, key), `引用不存在：${pointer}`);
      return node[key];
    }, doc);
}

function walkRefs(doc, node) {
  if (!node || typeof node !== 'object') return;
  for (const [key, value] of Object.entries(node)) {
    if (key === '$ref') resolve(doc, value);
    else walkRefs(doc, value);
  }
}

// 当前 DTO 均为简单带 JSON tag 的 struct；若改用其他结构，检查明确失败而不跳过。
function fields(source, name) {
  const match = source.match(new RegExp(`^type ${name} struct \\{\\r?\\n([\\s\\S]*?)^\\}`, 'm'));
  assert.ok(match, `找不到 DTO ${name}`);
  const out = new Map();
  for (let line of match[1].split('\n')) {
    line = line.trim();
    if (!line || line.startsWith('//')) continue;
    const field = line.match(/^\w+\s+(\S+)\s+`json:"([^",]+)(,omitempty)?"`/);
    if (field) out.set(field[2], {nullable: field[1].startsWith('*'), optional: Boolean(field[3])});
    else {
      assert.match(line, /^\w+$/, `无法解析 ${name}：${line}`);
      for (const [key, value] of fields(source, line)) out.set(key, value);
    }
  }
  return out;
}

const mappings = {
  enduser: {meResource:'Me', identityResource:'Identity', sessionResource:'Session', identityList:'IdentityList',
    pendingSignInResponse:'PendingSignIn', challengeResponse:'CodeChallenge', tokenResponse:'TokenPair',
    currentSessionResource:'CurrentSession', authenticationIdentityResource:'AuthenticationIdentity', clientInfoResource:'ClientInfo', profileResource:'Profile'},
  admin: {freezeResource:'Freeze', userResource:'AdminUser', identityResource:'AdminIdentity',
    userDetailResource:'AdminUserDetail', userList:'AdminUserList', revealResource:'RevealIdentity',
    sessionResource:'AdminSession', clientInfoResource:'AdminClientInfo', sessionList:'AdminSessionList', revokeAllResponse:'RevokeAllResult',
    auditEventResource:'AuditEvent', auditEventList:'AuditEventList'},
};

for (const surface of ['enduser', 'admin']) {
  const doc = JSON.parse(read(`docs/openapi/${surface}.json`));
  assert.equal(doc.openapi, '3.1.0');
  walkRefs(doc, doc);
  const router = read(`httpapi/${surface}/handler.go`).replace(/^\s*\/\/.*$/gm, '');
  assert.ok(!/\.(Route|Mount|Handle|HandleFunc|Method|MethodFunc)\s*\(/.test(router),
    `${surface} 引入新的路由注册形式，须更新提取器`);
  const registrations = [...router.matchAll(/\.(Get|Post|Patch|Delete|Put|Options|Head|Trace)\s*\(/g)];
  const actual = [...router.matchAll(/\.(Get|Post|Patch|Delete|Put|Options|Head|Trace)\s*\("([^"]+)"/g)]
    .map(m => `${m[1].toLowerCase()} ${m[2]}`).sort();
  assert.equal(actual.length, registrations.length, `${surface} 存在非字面量路径，须更新提取器`);
  const declared = [];
  const operationIDs = new Set();
  for (const [url, item] of Object.entries(doc.paths)) {
    for (const [method, operation] of Object.entries(item)) {
      if (!methods.has(method)) continue;
      declared.push(`${method} ${url}`);
      assert.ok(operation.operationId && !operationIDs.has(operation.operationId), 'operationId 缺失或重复');
      operationIDs.add(operation.operationId);
      const params = (operation.parameters || []).map(p => p.$ref ? resolve(doc, p.$ref) : p);
      const pathParams = params.filter(p => p.in === 'path');
      assert.deepEqual(pathParams.map(p => p.name).sort(), [...url.matchAll(/\{([^}]+)\}/g)].map(m => m[1]).sort());
      assert.ok(pathParams.every(p => p.required), `路径参数必须 required：${url}`);
    }
  }
  assert.deepEqual(declared.sort(), actual, `${surface} 路由与文档不一致`);
  // currentSessionResource 在独立文件，仍完整解析嵌入的 sessionResource。
  const source = read(`httpapi/${surface}/response.go`) + (surface === 'enduser' ? '\n' + read('httpapi/enduser/current_session.go') + '\n' + read('httpapi/enduser/profile_attributes.go') : '');
  for (const [dto, schemaName] of Object.entries(mappings[surface])) {
    const dtoFields = fields(source, dto);
    const schema = doc.components.schemas[schemaName];
    assert.deepEqual(Object.keys(schema.properties).sort(), [...dtoFields.keys()].sort(), `${dto} 字段漂移`);
    for (const [name, info] of dtoFields) {
      if (info.optional && ['sessionResource','currentSessionResource','clientInfoResource'].includes(dto)) assert.ok(!schema.required?.includes(name), `${dto}.${name} 应为可省略字段`);
      if (!info.optional) assert.ok(schema.required.includes(name), `${dto}.${name} 应为必需输出字段`);
      if (info.nullable && !info.optional) {
        const property = schema.properties[name];
        assert.ok(property.anyOf?.some(s => s.type === 'null') || property.type?.includes('null'), `${dto}.${name} 应允许 null`);
      }
    }
  }
  console.log(`${surface}: ${declared.length} operations, refs/path parameters/response DTOs OK`);
}

// 新查询、采集入口和原协议边界的明确契约。
const enduser = JSON.parse(read('docs/openapi/enduser.json'));
const current = enduser.paths['/users/me/sessions/current'].get;
assert.deepEqual(Object.keys(current.responses).sort(), ['200','401','403','500','503']);
assert.deepEqual(current['x-allowed-scopes'], ['user','user:bind']);
assert.equal(current.responses['200'].headers['Cache-Control'].schema.const, 'no-store');
const adminSpec = JSON.parse(read('docs/openapi/admin.json'));
assert.equal(adminSpec.paths['/users/{user}/identities/{identity}:reveal'].get.responses['200'].headers['Cache-Control'].schema.const, 'no-store', 'reveal no-store');
assert.equal(enduser.components.schemas.CurrentSession.properties.is_current.const, true);
assert.deepEqual(Object.keys(enduser.components.schemas.AuthenticationIdentity.properties).sort(), ['kind','name']);
const observed = new Set(['signInWithCode','signInWithIdp','completeSignIn','reauthenticate','refreshToken']);
const explicitHeaders = ['X-Client-Type','X-Client-OS-Name','X-Client-OS-Version','X-Client-Browser-Name','X-Client-Browser-Version','X-Client-App-Name','X-Client-App-Version','X-Device-Type','X-Device-Model'];
for (const item of Object.values(enduser.paths)) for (const op of Object.values(item)) {
  if (!op.operationId) continue;
  const headers = (op.parameters || []).map(p => p.$ref ? resolve(enduser,p.$ref) : p).filter(p => p.in === 'header');
  const clientHeaders = headers.filter(p => explicitHeaders.includes(p.name));
  if (observed.has(op.operationId)) {
    assert.deepEqual(clientHeaders.map(p=>p.name).sort(), [...explicitHeaders].sort(), op.operationId+' client headers');
    assert.ok(clientHeaders.every(p=>!p.required));
  } else assert.equal(clientHeaders.length,0,op.operationId+' must not collect client info');
}
for (const name of ['TokenPair','AccessToken','Identity','IdentityList']) {
  assert.ok(!Object.keys(enduser.components.schemas[name].properties).some(k=>/authentication_identities|client_info/.test(k)),name+' leaks session context');
}
for (const item of Object.values(enduser.paths)) for (const op of Object.values(item)) {
  if (!['signInWithCode','signInWithIdp','completeSignIn'].includes(op.operationId)) continue;
  assert.deepEqual(resolve(enduser,op.responses['200'].content['application/json'].schema.$ref).oneOf.map(s=>s.$ref).sort(), ['#/components/schemas/PendingSignIn','#/components/schemas/TokenPair']);
}
console.log('session context: scopes/statuses/optional headers/oneOf/token boundaries OK');
