# Changelog

This file starts after `v1.0.5`.

## Unreleased

## v1.0.6 (2026-11-19)

### Added

- `WSClientOptions.ReplyAckTimeout` and `WsConnectionManager.SetReplyAckTimeout` to configure the reply acknowledgement timeout (default `5000` ms).
- Per-module `nilaway` analysis in `make lint`.

### Changed

- Callback frames are now dispatched off the WebSocket read loop on a per-lifecycle ordered queue, so `OnMessage*` and `OnEvent*` handlers can call `Reply*` and await acknowledgements inline without deadlocking (`OnAuthenticated` remains synchronous on the read loop).
- `MessageHandler.HandleFrame` parses the JSON body once and reuses the shared `extractEventMap` helper across `message_handler.go`, `client.go` (`GetEventType`), and `ws.go` (`isDisconnectedEvent`).
- `NewWeComApiClient` defaults a `nil` logger to `NewDefaultLogger()`, and `DownloadFileRaw` bounds non-200 body draining to 4096 bytes.
- `generateRandomString` reads `(length+1)/2` random bytes instead of `length`.

### Fixed

- Avoided signed `int` shift overflow in reconnect backoff calculation for large attempt counts (`calcReconnectDelay` bounded doubling loop capped at `30000` ms).
- Fixed reply queue / pending-ack races and double-rejection hangs: `SendReply` now uses a `sync.Once`-guarded single buffered channel, `addPendingAck` is registered before `Send` and cleaned up on failure by item `seq`, `clearPendingMessages` deduplicates by `seq`, and dequeued queue heads are zeroed.
- Guarded `startHeartbeat` with `heartbeatMu` (checking `isManualClosed` and active connection) and `SetReplyAckTimeout` with `pendingAcksMu`.
- Guarded `nil` frames in `WSClient.Reply` and `WSClient.HasPendingReplyAck`, and rejected empty buffers in `WSClient.UploadMedia`.
- Fixed `GetFilenameFromURL` to use `path.Base` + `url.PathUnescape` and treat `""`, `"."`, and `"/"` as empty so `?filename=` fallback works; `parseFilename` now tries `mime.ParseMediaType` first with `url.PathUnescape` fallback.
- Hardened `examples/agent` `ValidateBashCommand` (rejecting shell metacharacters) and `ValidatePath` (handling `workingDir == "/"`), and `examples/basic` temp file creation (`os.MkdirTemp` error check and rejecting `.`/`..` filenames).
- Updated stale read-goroutine comments and fixed `README.md` formatting and missing option/method/type entries.
