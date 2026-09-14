# Проверка 2.0.0

Проверено на Windows 11 x64 в изолированном профиле `--test-profile`, поэтому настоящие Steam-аккаунты и Telegram не запускались.

- `go test ./...` — прошёл.
- `go vet ./...` — прошёл.
- `wails build -platform windows/amd64` — создаёт `build/bin/AgniaSteamHours.exe`.
- Нативный Wails EXE запущен с WebView2: подтверждены отображение сохранённого интерфейса, семь разделов, Wails binding и изолированная папка данных.
- Unit tests покрывают engine, storage, backup, GoSteam wrapper и Telegram validation.

Попытка `go test -race` была сделана с установленным MSYS2 GCC, но cgo завершился до компиляции тестов (`runtime/cgo`, exit status 2). Обычные тесты и `go vet` успешны; это ограничение конкретной cgo toolchain, а не успешный race-прогон.

Не подтверждено без добровольной интерактивной проверки владельцем: парольный вход Steam, Steam Guard, refresh reconnect против реальных CM, `PlayGamesConfirmed`, начисление часов, remote-PC handoff, free license и Telegram long polling с настоящим ботом.
