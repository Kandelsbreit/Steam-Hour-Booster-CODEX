# Статус переноса

Версия 2.0.0 перенесена с Electron/Node.js на Wails v2 + Go.

Завершено: GoSteam adapter, engine, DPAPI storage и migration legacy данных, backup, Telegram, scheduler, statistics, goals, presets, native autostart/suspend lifecycle, Wails bindings, сохранённый vanilla UI, тесты и Windows x64 build.

Перед выпуском остаётся только добровольная живая проверка Steam/Telegram владельцем учётной записи. Она не запускается автоматически и не использует сохранённые секреты без явного интерактивного ввода.
