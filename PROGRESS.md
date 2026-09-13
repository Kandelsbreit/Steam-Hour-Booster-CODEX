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

## Remaining
- Final ZIP, SHA-256, Desktop copy, source commit and GitHub release.

## Validation limits
No real Steam credentials have been used. Real Steam hour credit and remote-PC handoff require a live account.
