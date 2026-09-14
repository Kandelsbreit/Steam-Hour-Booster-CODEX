# Миграция Electron → Wails v2

Baseline: commit 1028d72, Electron 1.1.0, все 32 исходных теста прошли перед изменениями. Исходники доступны в истории Git и исходных ZIP; пользовательские данные не изменялись во время разработки.

| Исходный компонент | Новая ответственность |
|---|---|
| engine.js | internal/engine: состояние аккаунтов, партии, события, reconnect |
| steam-user | internal/steam: адаптер GoSteam v0.2.0, отдельный Client на аккаунт |
| store.js / safeStorage | internal/storage: атомарный JSON, DPAPI, чтение старого AES-GCM Local State |
| features.js / features-store.js | internal/model + engine: прежние настройки, расписание, цели, история |
| backup.js | internal/backup: совместимый AGNIA1 gzip/scrypt/AES-GCM |
| telegram.js / http.js | internal/telegram: HTTP API, ограничение отправителя, backoff |
| main.js / preload.js | app.go + Wails bindings/events + Windows lifecycle |
| HTML/CSS/renderer.js | frontend: прежние экраны, только замена IPC |

GoSteam используется без форка и без собственного CM/protobuf-клиента. Библиотека не предоставляет GetOwnedGames: библиотека игр читается через Steam HTTPS API с авторизацией текущей сессии, а не отдельный CM transport.

Непроверенное с реальным аккаунтом не считается подтверждённым. Live harness требует явного интерактивного ввода учётных данных; обычные тесты используют fake только на границе Steam.
