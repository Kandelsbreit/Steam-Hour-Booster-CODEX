# Статус переноса

Версия 2.1.0 перенесена с Electron/Node.js на Wails v2 + Go.

Завершено: GoSteam adapter, engine, DPAPI storage и migration legacy данных, backup, Telegram с проверкой токена/chat/webhook, scheduler, statistics, goals, пресеты и профили аккаунтов, ожидание сети/VPN, экономный режим трафика, проверка обновлений без автоустановки, native autostart/suspend lifecycle, Wails bindings, сохранённый vanilla UI, тесты и Windows x64 build.

Перед выпуском остаётся только добровольная живая проверка Steam/Telegram владельцем учётной записи. Она не запускается автоматически и не использует сохранённые секреты без явного интерактивного ввода.
