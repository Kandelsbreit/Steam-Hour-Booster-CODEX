# Миграция Electron → Wails v2

Baseline: commit `1028d72`, Electron 1.1.0. Перед переносом прошли все 32 исходных Node-теста. Предыдущая реализация остаётся доступна в Git-истории и в исходном архиве; рабочие пользовательские данные не перезаписываются без резервной копии.

| Раньше | Теперь |
|---|---|
| Electron main/preload IPC | `app.go`, Wails bindings и events |
| `engine.js` и `features.js` | `internal/engine` |
| `steam-user` | `internal/steam`, собственный интерфейс поверх GoSteam v0.2.0 |
| `store.js` / Electron safeStorage | `internal/storage`: атомарные файлы, DPAPI и чтение legacy AES-GCM |
| `features-store.js` | `internal/model`, встроенный каталог и Go persistence |
| `backup.js` | `internal/backup`, совместимый AGNIA1 gzip/scrypt/AES-GCM |
| `telegram.js` / `http.js` | `internal/telegram` и `internal/netx` |
| Electron autostart / power monitor | `internal/platform` и Windows lifecycle Wails |
| HTML/CSS/renderer | `frontend`, сохранён vanilla интерфейс; Electron bridge заменён на `bridge.js` |

## Границы Steam

`internal/steam.Client` скрывает GoSteam от движка. На каждый аккаунт создаётся отдельный client. Адаптер выполняет парольный вход, refresh token, Steam Guard, `PlayGamesConfirmed`, `ClearGames`, `RequestFreeLicense`, события игровой сессии и штатное закрытие соединения.

GoSteam не предоставляет публичный `GetOwnedGames`; кеш библиотеки запрашивается через Steam HTTPS API с access token текущей сессии. Это не новый CM/protobuf-клиент.

## Совместимость данных

При первом штатном запуске создаётся `migration-backup-*`, затем конфигурация читается в прежнем JSON-формате. Старые Electron-секреты расшифровываются только локальным DPAPI и после успешного входа сохраняются в `secrets-v2`. Старые файлы остаются на месте. `FORGOTTEN` tombstone не даёт старой сессии появиться вновь после явного «Забыть вход».

## Проверяемые инварианты

- один account — один client и одна последовательность reconnect;
- desired state переживает временную сеть и conflict, но ручная остановка отменяет его;
- remote playing session очищает текущую партию и не вытесняет настоящую игру;
- результаты старой асинхронной партии не могут вернуть игры после `ClearGames` или изменения настроек;
- закрытие отменяет contexts, очищает игры, закрывает clients и ждёт goroutines;
- refresh token, access token, GuardData и пароль не появляются в snapshot, логах или тестовых сообщениях.

Реальные Steam-операции не заявляются проверенными без интерактивного smoke harness и учётной записи владельца.
