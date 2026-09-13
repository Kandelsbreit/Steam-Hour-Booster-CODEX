'use strict';
const fs = require('node:fs');
const path = require('node:path');

function atomicWrite(file, value) {
  fs.mkdirSync(path.dirname(file), { recursive: true });
  const tmp = file + '.tmp';
  const fd = fs.openSync(tmp, 'w', 0o600);
  try { fs.writeFileSync(fd, value); fs.fsyncSync(fd); } finally { fs.closeSync(fd); }
  fs.renameSync(tmp, file);
}

class Store {
  constructor(dir, secure) {
    this.dir = dir; this.secure = secure;
    this.config = { version: 1, autoLaunch: false, accounts: [] };
    const file = path.join(dir, 'settings.json');
    if (fs.existsSync(file)) {
      const parsed = JSON.parse(fs.readFileSync(file, 'utf8'));
      if (parsed.version !== 1 || !Array.isArray(parsed.accounts) || parsed.accounts.length > 3) {
        throw new Error('Неверный формат settings.json. Файл сохранён без изменений.');
      }
      this.config = parsed;
    }
  }
  save() { atomicWrite(path.join(this.dir, 'settings.json'), JSON.stringify(this.config, null, 2)); }
  tokenFile(id) {
    if (!/^[a-f0-9-]{36}$/.test(id)) throw new Error('Неверный ID аккаунта');
    return path.join(this.dir, 'sessions', id + '.dat');
  }
  hasToken(id) { return fs.existsSync(this.tokenFile(id)); }
  saveToken(id, value) {
    if (!this.secure.isEncryptionAvailable()) throw new Error('Хранилище Windows недоступно: сессия не сохранена.');
    atomicWrite(this.tokenFile(id), this.secure.encryptString(value));
  }
  getToken(id) {
    const file = this.tokenFile(id);
    if (!fs.existsSync(file)) return null;
    if (!this.secure.isEncryptionAvailable()) throw new Error('Хранилище Windows недоступно');
    return this.secure.decryptString(fs.readFileSync(file));
  }
  forget(id) { fs.rmSync(this.tokenFile(id), { force: true }); }
}
module.exports = { Store, atomicWrite };
