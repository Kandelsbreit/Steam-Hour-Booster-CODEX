'use strict';
const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const crypto = require('node:crypto');
const { Store } = require('../src/store');
test('store uses encryption adapter, persists renewed token, and refuses plaintext fallback', t => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'agnia-test-')); t.after(() => fs.rmSync(dir, { recursive: true, force: true }));
  const key = crypto.randomBytes(32), iv = crypto.randomBytes(16);
  const secure = { isEncryptionAvailable: () => true,
    encryptString(s) { const c = crypto.createCipheriv('aes-256-cbc', key, iv); return Buffer.concat([c.update(s), c.final()]); },
    decryptString(b) { const c = crypto.createDecipheriv('aes-256-cbc', key, iv); return Buffer.concat([c.update(b), c.final()]).toString(); } };
  const id = crypto.randomUUID(), s = new Store(dir, secure);
  s.saveToken(id, 'secret-refresh-token'); assert.ok(!fs.readFileSync(s.tokenFile(id)).includes('secret-refresh-token'));
  s.saveToken(id, 'renewed-token'); assert.equal(new Store(dir, secure).getToken(id), 'renewed-token');
  secure.isEncryptionAvailable = () => false; assert.throws(() => s.saveToken(id, 'no-encryption'));
  secure.isEncryptionAvailable = () => true; assert.equal(s.getToken(id), 'renewed-token');
  s.forget(id); assert.equal(s.hasToken(id), false);
});
test('corrupt config is preserved instead of overwritten', t => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'agnia-test-')); t.after(() => fs.rmSync(dir, { recursive: true, force: true }));
  const file = path.join(dir, 'settings.json'); fs.writeFileSync(file, '{corrupt');
  assert.throws(() => new Store(dir, {})); assert.equal(fs.readFileSync(file, 'utf8'), '{corrupt');
});
