'use strict';
const { EventEmitter } = require('node:events');
const { randomUUID } = require('node:crypto');

function parseIds(value) {
  const items = Array.isArray(value) ? value : String(value || '').trim().split(/[\s,;]+/).filter(Boolean);
  if (items.length > 10000) throw new Error('Максимум 10 000 игр в списке');
  const ids = items.map(v => {
    if (!/^\d+$/.test(String(v))) throw new Error('AppID должны быть числами, через пробел или запятую');
    const n = Number(v);
    if (!Number.isSafeInteger(n) || n < 1 || n > 4294967295) throw new Error('Неверный AppID: ' + v);
    return n;
  });
  return [...new Set(ids)];
}
function settings(input) {
  const batchSize = Number(input.batchSize), rotationMinutes = Number(input.rotationMinutes);
  if (!Number.isInteger(batchSize) || batchSize < 1 || batchSize > 32) throw new Error('Размер партии: от 1 до 32');
  if (!Number.isInteger(rotationMinutes) || rotationMinutes < 1 || rotationMinutes > 1440) throw new Error('Ротация: от 1 до 1440 минут');
  return { appids: parseIds(input.appids), batchSize, rotationMinutes, autoStart: !!input.autoStart };
}

class Engine extends EventEmitter {
  constructor(store, createClient, clock = () => Date.now()) {
    super(); this.store = store; this.createClient = createClient; this.clock = clock;
    this.live = new Map(); this.logs = [];
    for (const a of store.config.accounts) { Object.assign(a, settings(a)); this.live.set(a.id, this.fresh()); }
  }
  fresh() {
    return { client: null, online: false, desired: false, status: 'Остановлено', blocked: false,
      guard: null, guardCallback: null, retryAt: 0, failures: 0, readyAt: 0,
      index: 0, batchElapsed: 0, activeMs: 0, gameMs: 0, lastTick: this.clock(), sent: [], library: [], generation: 0 };
  }
  account(id) { const a = this.store.config.accounts.find(a => a.id === id); if (!a) throw new Error('Аккаунт не найден'); return a; }
  runtime(id) { this.account(id); return this.live.get(id); }
  log(id, message) {
    this.logs.push({ time: this.clock(), account: id ? this.account(id).name : 'Программа', message });
    if (this.logs.length > 250) this.logs.shift();
    this.emit('change');
  }
  snapshot() {
    return { autoLaunch: this.store.config.autoLaunch, logs: this.logs, accounts: this.store.config.accounts.map(a => {
      const r = this.runtime(a.id);
      return { ...a, hasToken: this.store.hasToken(a.id), status: r.status, online: r.online, desired: r.desired,
        blocked: r.blocked, guard: r.guard, activeMs: r.activeMs, gameMs: r.gameMs, current: r.sent,
        batchIndex: r.index, batchCount: Math.ceil(a.appids.length / a.batchSize),
        remainingMs: Math.max(0, a.rotationMinutes * 60000 - r.batchElapsed), library: r.library };
    }) };
  }
  add(name) {
    name = String(name || '').trim();
    if (!/^[a-zA-Z0-9_]{2,64}$/.test(name)) throw new Error('Введи логин Steam, а не имя профиля или ссылку');
    if (this.store.config.accounts.length >= 3) throw new Error('Можно добавить до трёх аккаунтов');
    if (this.store.config.accounts.some(a => a.name.toLowerCase() === name.toLowerCase())) throw new Error('Этот аккаунт уже добавлен');
    const a = { id: randomUUID(), name, appids: [], batchSize: 32, rotationMinutes: 60, autoStart: false };
    this.store.config.accounts.push(a); this.live.set(a.id, this.fresh()); this.store.save(); return a.id;
  }
  update(id, input) {
    const a = this.account(id), r = this.runtime(id), next = settings(input);
    const changed = JSON.stringify(a.appids) !== JSON.stringify(next.appids) || a.batchSize !== next.batchSize || a.rotationMinutes !== next.rotationMinutes;
    Object.assign(a, next); this.store.save();
    if (changed) { r.index = 0; r.batchElapsed = 0; this.clearGames(r); this.apply(id); }
  }
  remove(id) {
    this.stop(id); this.store.forget(id); this.store.config.accounts = this.store.config.accounts.filter(a => a.id !== id);
    this.live.delete(id); this.store.save();
  }
  forget(id) { this.stop(id); this.store.forget(id); this.log(id, 'Сохранённая сессия удалена с этого компьютера'); }
  start(id, password) {
    const r = this.runtime(id);
    if (r.desired) throw new Error('Сначала останови текущий вход или сессию');
    r.desired = true; r.failures = 0; r.retryAt = 0;
    try { this.connect(id, password); } catch (e) { r.desired = false; r.status = 'Нужен вход'; throw e; }
  }
  detach(r) {
    const old = r.client;
    r.client = null; r.online = false; r.generation++;
    r.guard = null; r.guardCallback = null; r.sent = [];
    if (old) { try { old.logOff(); } catch {} }
  }
  connect(id, password) {
    const a = this.account(id), r = this.runtime(id);
    this.detach(r);
    const token = password ? null : this.store.getToken(id);
    if (!token && !password) throw new Error('Для первого входа введи пароль Steam');
    const c = this.createClient(); r.client = c;
    const current = () => r.client === c;
    r.status = 'Подключение…'; r.retryAt = 0; r.blocked = false; r.lastTick = this.clock();
    c.on('refreshToken', token => {
      if (!current()) return;
      try { this.store.saveToken(id, token); this.log(id, 'Сессия сохранена в защищённом хранилище'); }
      catch { this.log(id, 'Не удалось сохранить сессию. Проверь доступ к папке данных и хранилищу Windows.'); }
    });
    c.on('steamGuard', (domain, callback, lastCodeWrong) => {
      if (!current()) return;
      r.guardCallback = callback;
      r.guard = { kind: domain ? 'email' : 'mobile', wrong: !!lastCodeWrong,
        waitUntil: lastCodeWrong ? this.clock() + 30000 : 0 };
      r.status = lastCodeWrong ? 'Неверный код: дождись нового' : domain ? 'Нужен код из почты' : 'Нужен код Steam Guard';
      this.emit('change');
    });
    c.on('loggedOn', () => {
      if (!current()) return;
      r.online = true; r.failures = 0; r.guard = null; r.guardCallback = null;
      r.readyAt = this.clock() + 5000; r.lastTick = this.clock(); r.status = 'Проверка игровой сессии…';
      this.log(id, 'Вход выполнен');
      this.refreshLibrary(id).catch(() => { if (current()) this.log(id, 'Список игр пока недоступен. Можно ввести AppID вручную.'); });
    });
    c.on('playingState', (blocked) => {
      if (!current()) return;
      if (blocked) {
        r.blocked = true; r.status = 'Пауза: игра на другом компьютере';
        this.clearGames(r); this.emit('change');
      } else if (r.blocked) {
        r.blocked = false; r.readyAt = this.clock() + 15000;
        r.status = 'Игра закрыта, возобновление через 15 с'; this.emit('change');
      }
    });
    c.on('error', err => { if (current()) this.failure(id, err.eresult); });
    c.on('disconnected', code => { if (current()) this.failure(id, code); });
    try {
      c.logOn(token ? { refreshToken: token, logonID: this.logonId(id) } :
        { accountName: a.name, password, logonID: this.logonId(id), machineName: 'Agnia Steam Hours' });
    } catch (e) { this.detach(r); throw new Error('Не удалось начать вход. Проверь сохранённую сессию или введи пароль заново.'); }
    this.emit('change');
  }
  logonId(id) { return parseInt(id.replaceAll('-', '').slice(0, 8), 16) >>> 0; }
  failure(id, code) {
    const r = this.runtime(id);
    this.detach(r);
    if (!r.desired) return;
    // Reconnect conservatively; apply() still checks playingState and never forces games.
    const conflict = [6, 34, 50].includes(code);
    const temporary = [0, 2, 3, 10, 16, 20, 35, 36, 37, 38, 48, 84].includes(code) || code == null;
    if (conflict) {
      if (this.store.hasToken(id)) {
        r.failures++;
        const delay = Math.min(900, 120 * 2 ** Math.min(r.failures - 1, 3));
        r.retryAt = this.clock() + delay * 1000;
        r.status = 'Сессия занята: проверка через ' + delay + ' с';
      } else {
        r.desired = false; r.status = 'Сессия занята: для восстановления нужен вход';
      }
    } else if (temporary && this.store.hasToken(id)) {
      r.failures++;
      const delay = code === 84 ? 300 : Math.min(300, 30 * 2 ** Math.min(r.failures - 1, 4));
      r.retryAt = this.clock() + delay * 1000; r.status = 'Нет связи: повтор через ' + delay + ' с';
    } else {
      r.desired = false; r.status = 'Вход отклонён: попробуй пароль и Steam Guard';
    }
    this.log(id, r.status + (Number.isInteger(code) ? ' (Steam ' + code + ')' : ''));
  }
  guard(id, code) {
    const r = this.runtime(id);
    if (!r.guardCallback) throw new Error('Steam сейчас не запрашивает код');
    if (this.clock() < r.guard.waitUntil) throw new Error('Дождись нового кода Steam Guard');
    code = String(code || '').trim().toUpperCase();
    if (!/^[A-Z0-9]{5}$/.test(code)) throw new Error('Код Steam Guard должен состоять из пяти символов');
    const cb = r.guardCallback; r.guardCallback = null; r.guard = null; r.status = 'Проверка кода…'; cb(code);
  }
  clearGames(r) {
    if (r.online && r.sent.length) {
      r.sent = [];
      try { r.client.gamesPlayed([]); } catch {}
    } else r.sent = [];
  }
  stop(id) {
    const r = this.runtime(id); r.desired = false; r.retryAt = 0;
    this.clearGames(r); this.detach(r); r.blocked = false; r.status = 'Остановлено'; this.emit('change');
  }
  apply(id) {
    const a = this.account(id), r = this.runtime(id);
    if (!r.desired || !r.online || this.clock() < r.readyAt) return;
    if (r.blocked || r.client.playingState?.blocked) {
      r.blocked = true; r.status = 'Пауза: игра на другом компьютере'; this.clearGames(r); return;
    }
    if (!a.appids.length) { r.status = 'В сети: выбери игры и сохрани'; this.clearGames(r); return; }
    const count = Math.ceil(a.appids.length / a.batchSize); r.index %= count;
    const batch = a.appids.slice(r.index * a.batchSize, (r.index + 1) * a.batchSize);
    if (JSON.stringify(batch) !== JSON.stringify(r.sent)) {
      r.sent = batch;
      try { r.client.gamesPlayed(batch, false); }
      catch { r.sent = []; this.failure(id, 2); return; }
      if (!r.blocked) this.log(id, 'Запрошена партия ' + (r.index + 1) + '/' + count + ': ' + batch.length + ' игр');
    }
    if (!r.blocked) r.status = 'Работает';
  }
  next(id) { const r = this.runtime(id); r.index++; r.batchElapsed = 0; this.apply(id); }
  tick() {
    const now = this.clock();
    for (const a of this.store.config.accounts) {
      const r = this.runtime(a.id), raw = now - r.lastTick; r.lastTick = now;
      // A suspended PC isn't counted as connected playtime.
      const elapsed = raw >= 0 && raw < 5000 ? raw : 0;
      if (r.online && r.desired && !r.blocked && r.sent.length) {
        r.activeMs += elapsed; r.gameMs += elapsed * r.sent.length; r.batchElapsed += elapsed;
        if (r.batchElapsed >= a.rotationMinutes * 60000) { r.index++; r.batchElapsed = 0; }
      }
      if (r.desired && !r.client && r.retryAt && now >= r.retryAt) {
        try { this.connect(a.id); } catch { r.desired = false; r.retryAt = 0; r.status = 'Сессия недоступна: нужен повторный вход'; }
      }
      this.apply(a.id);
    }
  }
  async refreshLibrary(id) {
    const r = this.runtime(id), c = r.client;
    if (!r.online || !c?.steamID) throw new Error('Сначала войди в аккаунт');
    const response = await c.getUserOwnedApps(c.steamID, { includePlayedFreeGames: true, includeAppInfo: true, skipUnvettedApps: false });
    if (r.client !== c) return;
    r.library = (response.apps || []).map(g => ({ appid: g.appid, name: g.name || 'App ' + g.appid,
      playtime_forever: g.playtime_forever || 0, playtime_2weeks: g.playtime_2weeks ?? null })).sort((a, b) => a.name.localeCompare(b.name));
    this.emit('change');
  }
  shutdown() { for (const a of this.store.config.accounts) this.stop(a.id); }
}
module.exports = { Engine, parseIds, settings };
