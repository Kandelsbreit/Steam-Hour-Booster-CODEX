'use strict';
const { app, BrowserWindow, ipcMain, safeStorage, dialog, Tray, Menu, nativeImage, net, Notification, powerMonitor } = require('electron');
const path = require('node:path');
const fs = require('node:fs');
const { pathToFileURL } = require('node:url');
const SteamUser = require('steam-user');
const { Store } = require('./store');
const { Features } = require('./features');
const { Telegram } = require('./telegram');
const { searchGames } = require('./http');
const { exportBackup, unpack, restoreBackup, LIMIT } = require('./backup');
const { atomicWrite } = require('./store');
const testProfile = process.argv.find(a => a.startsWith('--test-profile='))?.slice('--test-profile='.length);
app.setPath('userData', testProfile ? path.resolve(testProfile) : path.join(app.getPath('appData'), 'agnia-steam-hours'));
let win, tray, engine, telegram, timer, quitting = false, restored = false;
const page = pathToFileURL(path.join(__dirname, 'index.html')).href;

function openWindow() {
  if (win) { win.show(); win.focus(); return; }
  win = new BrowserWindow({ width: 1260, height: 900, minWidth: 980, minHeight: 700,
    title: 'Agnia Steam Hours', backgroundColor: '#10121b', autoHideMenuBar: true,
    webPreferences: { preload: path.join(__dirname, 'preload.js'), contextIsolation: true, nodeIntegration: false, sandbox: true } });
  win.webContents.setWindowOpenHandler(() => ({ action: 'deny' }));
  win.webContents.on('will-navigate', (event, url) => { if (url !== page) event.preventDefault(); });
  win.webContents.session.setPermissionRequestHandler((_wc, _permission, callback) => callback(false));
  win.loadURL(page);
  win.on('close', event => {
    if (!quitting && tray) { event.preventDefault(); win.hide(); }
  });
  win.on('closed', () => { win = null; });
}
function snapshot() { const s = engine.snapshot(); s.telegram.hasToken = telegram.hasToken(); s.telegram.status = telegram.status; s.dataPath = engine.store.dir; return s; }
function publish() { if (win && !win.isDestroyed()) win.webContents.send('snapshot', snapshot()); }
function icon() {
  const size = 32, pixels = Buffer.alloc(size * size * 4);
  for (let y = 0; y < size; y++) for (let x = 0; x < size; x++) {
    const p = (y * size + x) * 4, ring = Math.abs(Math.hypot(x - 15.5, y - 15.5) - 11) < 2;
    const hand = (x >= 15 && x <= 17 && y > 7 && y < 18) || (y >= 16 && y <= 18 && x >= 16 && x < 24);
    pixels[p] = 220; pixels[p + 1] = 170; pixels[p + 2] = 160; pixels[p + 3] = ring || hand ? 255 : 0;
  }
  return nativeImage.createFromBitmap(pixels, { width: size, height: size });
}
if (!app.requestSingleInstanceLock()) app.quit();
else {
  app.on('second-instance', openWindow);
  app.whenReady().then(() => {
    try {
      if (process.platform !== 'win32' && !process.argv.includes('--dev')) throw new Error('Эта сборка предназначена для Windows 10/11 x64.');
      const store = new Store(app.getPath('userData'), safeStorage);
      engine = new Features(store, () => {
        if (testProfile) throw new Error('Steam отключён в тестовом профиле');
        return new SteamUser({ dataDirectory: null, autoRelogin: false,
          renewRefreshTokens: true, enablePicsCache: false, webCompatibilityMode: true });
      }, undefined, () => net.isOnline());
      telegram = new Telegram(engine);
      ipcMain.handle('command', async (event, command, payload = {}) => {
        if (event.sender !== win?.webContents || event.senderFrame !== win.webContents.mainFrame || event.senderFrame.url !== page) {
          return { ok: false, error: 'Недопустимый источник команды' };
        }
        try {
          if (!payload || typeof payload !== 'object') throw new Error('Неверные параметры');
          let result;
          switch (command) {
            case 'state': break;
            case 'add': result = engine.add(payload.name); break;
            case 'save': engine.update(payload.id, payload); break;
            case 'start': engine.start(payload.id, payload.password); break;
            case 'stop': engine.stop(payload.id); break;
            case 'start-all': engine.startAll(); break;
            case 'stop-all': engine.shutdown(); break;
            case 'guard': engine.guard(payload.id, payload.code); break;
            case 'next': engine.next(payload.id); break;
            case 'library': await engine.refreshLibrary(payload.id, true); break;
            case 'search-games':
              if (Date.now() - engine.searchAt < 2000) throw Error('Подожди 2 секунды перед следующим поиском');
              engine.searchAt = Date.now(); result = await searchGames(payload.query, engine.bytes); break;
            case 'custom-add': engine.customAdd(payload.id, payload.appid, payload.name); break;
            case 'custom-remove': {
              engine.account(payload.id); const d = engine.features.account(payload.id);
              d.custom = d.custom.filter(g => g.appid !== Number(payload.appid)); engine.flush(); break;
            }
            case 'popular': {
              engine.account(payload.id); const d = engine.features.account(payload.id);
              const games = new Map(d.custom.map(g => [g.appid, g]));
              for (const g of require('./features-store').POPULAR) games.set(g.appid, g);
              d.custom = [...games.values()]; engine.flush(); break;
            }
            case 'game-remove': engine.gameRemove(payload.id, payload.appid); break;
            case 'free-license': result = await engine.freeLicense(payload.id, payload.appids); break;
            case 'preset-save': engine.presetSave(payload.id, payload.name, payload.appids); break;
            case 'preset-delete': engine.presetDelete(payload.id, payload.preset); break;
            case 'preset-apply': engine.presetApply(payload.id, payload.preset); break;
            case 'goal-save': engine.goalSave(payload.id, payload); break;
            case 'goal-delete': engine.goalDelete(payload.id, payload.appid); break;
            case 'options': engine.setOptions(payload); break;
            case 'telegram': telegram.save(payload); break;
            case 'export-backup': {
              const data = exportBackup(engine, !!payload.includeTokens, String(payload.password || ''));
              const save = await dialog.showSaveDialog(win, { title: 'Сохранить резервную копию', defaultPath: 'Agnia-backup.agnia', filters: [{ name: 'Резервная копия Agnia', extensions: ['agnia'] }] });
              if (!save.canceled && save.filePath) { atomicWrite(save.filePath, data); result = 'Резервная копия сохранена'; } break;
            }
            case 'restore-backup': {
              const open = await dialog.showOpenDialog(win, { title: 'Восстановить резервную копию', properties: ['openFile'], filters: [{ name: 'Резервная копия Agnia', extensions: ['agnia'] }] });
              if (open.canceled) break;
              if (fs.statSync(open.filePaths[0]).size > LIMIT) throw Error('Слишком большой файл');
              const data = unpack(fs.readFileSync(open.filePaths[0]), String(payload.password || ''));
              const confirm = await dialog.showMessageBox(win, { type: 'warning', buttons: ['Отмена', 'Восстановить'], defaultId: 0, cancelId: 0,
                message: `Заменить настройки, пресеты и статистику? Аккаунтов в копии: ${data.config.accounts.length}. Программа перезапустится.` });
              if (confirm.response !== 1) break;
              engine.shutdown(); engine.flush(); telegram.stop();
              restoreBackup(store, data); restored = true; app.relaunch(); app.quit(); break;
            }
            case 'export-diagnostics': {
              const save = await dialog.showSaveDialog(win, { defaultPath: 'Agnia-diagnostics.json', filters: [{ name: 'JSON', extensions: ['json'] }] });
              if (!save.canceled && save.filePath) {
                const s = snapshot(); atomicWrite(save.filePath, JSON.stringify({ version: s.version, health: s.health, logs: s.logs,
                  accounts: s.accounts.map(a => ({ name: a.name, status: a.status, online: a.online, retryAt: a.retryAt, failures: a.failures })) }, null, 2)); result = 'Диагностика сохранена';
              } break;
            }
            case 'forget': engine.forget(payload.id); break;
            case 'remove': engine.remove(payload.id); break;
            case 'autolaunch':
              if (!testProfile) app.setLoginItemSettings({ openAtLogin: !!payload.enabled, path: process.execPath });
              store.config.autoLaunch = !!payload.enabled; store.save(); break;
            case 'quit': app.quit(); break;
            default: throw new Error('Неизвестная команда');
          }
          return { ok: true, result, state: snapshot() };
        } catch (e) { return { ok: false, error: ['library', 'search-games', 'free-license'].includes(command) ? 'Не удалось выполнить запрос: ' + e.message : e.message }; }
      });
      engine.on('change', publish);
      engine.on('alert', message => { if (!testProfile && Notification.isSupported()) new Notification({ title: 'Agnia Steam Hours', body: message }).show(); });
      try {
        tray = new Tray(icon()); tray.setToolTip('Agnia Steam Hours');
        tray.setContextMenu(Menu.buildFromTemplate([{ label: 'Открыть', click: openWindow },
          { label: 'Остановить всё', click: () => engine.shutdown() }, { type: 'separator' }, { label: 'Выйти', click: () => app.quit() }]));
        tray.on('double-click', openWindow);
      } catch { tray = null; }
      openWindow();
      if (engine.features.data.options.startMinimized && tray && !testProfile) win.hide();
      if (!testProfile) {
        // Opening the app once after a move repairs the Windows startup entry.
        if (store.config.autoLaunch) app.setLoginItemSettings({ openAtLogin: true, path: process.execPath });
        engine.startup(); telegram.restart();
      }
      timer = setInterval(() => {
        try { engine.tick(); publish(); }
        catch { clearInterval(timer); engine.shutdown(); if (win) win.webContents.send('app-error', 'Работа остановлена: не удалось сохранить данные или выполнить цикл. Проверь доступ к папке данных и перезапусти программу.'); }
      }, 1000);
      powerMonitor.on('suspend', () => { engine.flush(); for (const a of store.config.accounts) { const r = engine.runtime(a.id); engine.clearGames(r); } });
      powerMonitor.on('resume', () => {
        for (const a of store.config.accounts) { const r = engine.runtime(a.id); r.lastTick = Date.now();
          if (r.desired) engine.failure(a.id, 3);
        }
      });
      win.webContents.on('render-process-gone', () => { if (!quitting) { engine.log(null, 'Интерфейс остановился; перезагрузка окна'); win.reload(); } });
    } catch (e) { dialog.showErrorBox('Agnia Steam Hours', e.message); app.quit(); }
  });
  app.on('before-quit', () => { quitting = true; clearInterval(timer); telegram?.stop(); if (!restored) { engine?.shutdown(); try { engine?.flush(); } catch {} } tray?.destroy(); tray = null; });
  app.on('window-all-closed', () => { if (!tray) app.quit(); });
}
