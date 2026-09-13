# Agnia Steam Hours — 1.1.0

## Scope
Extend local 1.0.0, preserve authentication, session encryption, gamesPlayed and rotation.
Only engine change allowed: reconnect after session conflict, with backoff and no forced games.
Publish release to Kandelsbreit/Steam-Hour-Booster-CODEX; copy ZIP to Windows Desktop.

## Completed
- Original archive extracted into baseline/; original ZIP retained unchanged.
- Source copied into source/; locked dependencies installed.
- GitHub access verified; destination repository is empty.
- Original engine, renderer, persistence and 14 tests inspected.

## Completed implementation and validation
- Seven sections using the original dark/purple UI; independent accounts, library, presets and queues.
- Persistent stats/goals, startup/schedule/breaks/stop timer, cached library, Telegram and encrypted backup.
- All 32 unit tests pass, including original engine audit.
- UI smoke tests pass in source Electron and packaged Windows EXE, with no page errors.
- EXE and app.asar versions verified as 1.1.0. Original store.js unchanged byte-for-byte.
- User documentation and validation limitations written.

## Distribution completed
- Release: https://github.com/Kandelsbreit/Steam-Hour-Booster-CODEX/releases/tag/v1.1.0
- Source commit: d84b028 (release code).
- ZIP: dist/AgniaSteamHours-1.1.0-Windows-x64.zip, 167856438 bytes.
- Desktop: C:/Users/RL/Desktop/AgniaSteamHours-1.1.0-Windows-x64.zip.
- SHA-256: a8b704eb15be03f38fed4377d401a045e40b09860018e8ccfe05a77f807bde0a.
- Local ZIP, Desktop ZIP and GitHub asset digest match. Release is published, not a draft; ZIP and SHA256SUMS.txt both uploaded.
- No remaining implementation or distribution steps for 1.1.0.

## Validation limits
No real Steam credentials have been used. Real Steam hour credit and remote-PC handoff require a live account.
