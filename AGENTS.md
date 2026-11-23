# AGENTS.md

Contexta is a personal WhatsApp data platform. It receives UAZAPI webhooks, keeps its own PostgreSQL history, exposes an admin REST API, and gives MCP clients scoped access to conversations, tasks, memories, companies, email and notifications.

It has **no LLM, chatbot or automatic replies**. WhatsApp messages are sent only through the MCP send tools, one human-approved message at a time. Keep it that way.

## Contexta: three repos

Contexta lives in three sibling repos under the same folder:

- `contexta-backend`: Go API, MCP server and Postgres migrations.
- `contexta-web`: Next.js admin panel, which calls the API.
- `contexta-android`: Kotlin app, which calls the API directly.

Features ship as paired commits with the same version: `internal/version` in the backend and `package.json` in the web. Android has its own `versionName`; bump its `versionCode` too, so the in-app update sees the new version.

**Push order:** backend first, then web, then Android. The web and the app depend on API changes such as migrations and new fields.

**Verification before each push:**

- Backend: `go build ./... && go vet ./... && go test ./...`. Integration tests need `TEST_DATABASE_URL`.
- Web: `pnpm typecheck && pnpm lint && pnpm test && pnpm build`.
- Android: **do not run `./gradlew` on the local machine.** Gradle plus the Kotlin daemon use 2 to 3 GB of RAM and freeze the computer. Push, and let GitHub Actions verify: the `APK` workflow runs `test assembleRelease` and publishes the release. Follow it with `gh run watch <id> -R leofelipet/contexta-android --exit-status`.

Do not run heavy builds in parallel. When reporting a result, use the command's real exit code: `cmd | tail; echo $?` reports the exit code of `tail`.

## Layout

- `cmd/contexta`: single binary with the subcommands `serve`, `migrate` and the UAZAPI helpers. Deploys run `migrate` before `serve`.
- `internal/app`: wiring only. It builds services and mounts `/mcp` and the REST handler.
- `internal/<domain>` (tasks, memories, companies, email, ...): types, params and services for that domain. No HTTP and no SQL here.
- `internal/storage/postgres`: all persistence. One file per domain; validation happens in `validateX` helpers before SQL.
- `internal/transport/httpapi`: REST under `/api/v1`, webhooks, health and SSE.
- `internal/transport/mcpserver`: the Streamable HTTP MCP server (stateless).
- `internal/providers/*`: clients for external services (UAZAPI, IMAP/SMTP, Groq, Pushover, Trello).
- `migrations/`: goose SQL files, embedded in the binary.

Each transport declares its own `Store` interface with only the methods it uses. A new storage method must be added to that interface and to the test fakes that implement it.

## Conventions

- **Migrations:** the next number `NNN_name.sql`, with `-- +goose Up` and `-- +goose Down`. Never edit a migration that has already shipped; add a new one.
- **Errors:** storage returns `postgres.ErrNotFound`, `ErrInvalidArgument` or `ErrConflict`, wrapped with `%w` and a short reason. `handleStoreError` maps them to HTTP status codes; `safeToolError` hides internals from MCP clients. Do not leak raw database errors.
- **REST bodies:** decode with `http.MaxBytesReader` and `DisallowUnknownFields`; errors are returned as `{"error": "..."}`.
- **Pagination:** opaque cursors from `internal/pagination`, each with its own `Kind`; `limit` is capped at 100.
- **Dates:** inputs accept RFC3339 or `YYYY-MM-DD`, in UTC.
- **Realtime:** `realtime.Hub.Publish()` is only a "something changed" signal on `/api/v1/events` (SSE). Clients refetch; the event carries no data.
- **Activity log:** record admin and integration operations with `recordActivity` / `RecordActivity`. Never put message bodies, payloads, auth headers or secrets in it. The only exception is the WhatsApp send tools, which keep what they sent as an audit trail.

## MCP

- Auth uses named keys created in the panel (`mcp_keys` table). Only the SHA-256 hash is stored, and the token is shown once. There is no MCP token in env.
- A key has access `read` (every read-only tool), `write` (every tool) or `custom` (an explicit list). Tools outside the scope are hidden from `tools/list` and rejected on `tools/call`.
- **The tool annotation is a security boundary.** Register each tool with the constructor that matches what it does: `readOnlyTool`, `localWriteTool`, `openWorldReadTool`, `openWorldWriteTool` or `destructiveOpenWorldTool`. A tool that changes anything must never be read-only, or every read key gets it.
- Every tool handler calls `s.logAccess(ctx, "<tool>")` and returns errors through `safeToolError`.
- The MCP tests assert how many tools are registered; update them when you add or remove tools.

## Secrets

- Third-party credentials (email, Pushover, Trello) are encrypted at rest with `EMAIL_CREDENTIALS_KEY`. They are never returned by the API or MCP, and never logged.
- Logs never include message bodies, transcripts, media keys or `Authorization` headers.

## Tests

- Unit tests use fakes and run with `go test ./...`.
- Integration tests in `internal/storage/postgres` skip unless `TEST_DATABASE_URL` is set. They need pgvector; use a throwaway container and stop it at the end:

  ```sh
  docker run -d --rm --name contexta-test -e POSTGRES_USER=t -e POSTGRES_PASSWORD=t -e POSTGRES_DB=t -p 55439:5432 pgvector/pgvector:pg17
  TEST_DATABASE_URL='postgres://t:t@localhost:55439/t?sslmode=disable' go test ./internal/storage/postgres/
  docker stop contexta-test
  ```
