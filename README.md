# Contexta

Personal WhatsApp data platform and authenticated MCP server. Contexta receives UAZAPI webhooks, keeps an independent PostgreSQL history, exposes an administrative REST API, and gives authenticated MCP clients progressive access to conversations.

The administrative Next.js interface lives in the separate [contexta-web](https://github.com/leofelipet/contexta-web) repository.

Contexta does not contain an LLM, chatbot, response automation, or message-sending tool.

## Requirements

- Go 1.25 or newer
- PostgreSQL 14 or newer
- A UAZAPI instance

Docker Compose can run PostgreSQL locally.

## Local setup

```sh
cp .env.example .env
docker compose up -d postgres
set -a; . ./.env; set +a
go run ./cmd/contexta migrate
go run ./cmd/contexta serve
```

Generate independent random values containing at least 32 characters for `API_BEARER_TOKEN`, `MCP_BEARER_TOKEN`, and `UAZAPI_WEBHOOK_SECRET` before starting the server.

To run PostgreSQL, migrations, and the backend together in containers, fill `.env` and run:

```sh
docker compose --profile backend up --build
```

The Compose profile publishes the backend at `http://localhost:8082` by default.

Configure UAZAPI after the public HTTPS endpoint is available:

```sh
go run ./cmd/contexta uazapi-status
go run ./cmd/contexta uazapi-configure-webhook
```

`UAZAPI_WEBHOOK_PUBLIC_URL` is the URL without the secret, for example `https://context.example.com/webhooks/uazapi`. The configuration command appends the secret.

UAZAPI 2.1.1 does not document a webhook signature or custom authorization header. Configure the reverse proxy to redact the webhook path from access logs, apply rate limiting, and rotate `UAZAPI_WEBHOOK_SECRET` if the callback URL may have been exposed.

## Audio transcription

Contexta can transcribe new WhatsApp audio messages asynchronously with Groq. The webhook stores the message and enqueues a durable PostgreSQL job; a background worker downloads decrypted OGG media through UAZAPI and uploads it directly to Groq without creating a public media URL or retaining the audio.

Enable transcription with:

```text
TRANSCRIPTION_ENABLED=true
GROQ_API_KEY=gsk_...
GROQ_TRANSCRIPTION_MODEL=whisper-large-v3-turbo
GROQ_TRANSCRIPTION_LANGUAGE=pt
```

The supported models are `whisper-large-v3-turbo` and `whisper-large-v3`. Failed jobs use bounded retries, survive application restarts, and never block webhook acknowledgement. Only messages received after this feature is deployed are enqueued. Transcripts are searchable and available through REST and MCP responses.

Conversation names are reconciled from UAZAPI every six hours. Direct chats prefer the saved contact name, WhatsApp push name, and phone number in that order; internal `@lid` identifiers are never used as display titles.

## Endpoints

Health checks:

```text
GET /health/live
GET /health/ready
```

UAZAPI callback:

```text
POST /webhooks/uazapi/{secret}
```

REST API, protected by `Authorization: Bearer $API_BEARER_TOKEN`:

```text
GET /api/v1/contacts
GET /api/v1/contacts/{id}
GET /api/v1/conversations
GET /api/v1/conversations/{id}
GET /api/v1/conversations/{id}/messages
GET /api/v1/messages
GET /api/v1/messages/unread
POST /api/v1/messages/acknowledge
GET /api/v1/messages/{id}
GET /api/v1/messages/{id}/around
GET /api/v1/dashboard
GET /api/v1/integrations/uazapi
POST /api/v1/integrations/uazapi/configure-webhook
GET /api/v1/mcp/status
GET /api/v1/activity
```

Common message filters are `query`, `from`, `to`, `contact_id`, `conversation_id`, `direction`, `type`, `consumer_id`, `read_state`, `limit`, and `cursor`. Dates accept RFC3339 or `YYYY-MM-DD` in UTC.

The Streamable HTTP MCP endpoint is:

```text
POST /mcp
Authorization: Bearer $MCP_BEARER_TOKEN
```

Available tools:

```text
search_messages
list_unread_messages
acknowledge_messages
find_conversations
get_conversation
get_messages
get_messages_around
list_contacts
get_contact
```

Message read state is tracked independently for each agent `consumer_id`. Use `list_unread_messages` to fetch pending work without changing state, then call `acknowledge_messages` only after processing succeeds. `search_messages` accepts optional `consumer_id` and `read_state` (`all`, `read`, or `unread`) filters. Existing search and conversation tools always remain available for historical access.

All existing messages start unread for a new consumer. Audio messages with an active transcription job are held out of the unread queue until transcription completes or permanently fails. Acknowledgement only writes local read receipts; it never modifies or sends WhatsApp messages.

## Webhook fixtures

UAZAPI 2.1.1 does not document complete `messages`, `history`, and `messages_update` webhook payloads. Set `UAZAPI_CAPTURE_DIR=.contexta-captures` temporarily to capture real payload structures. Captures are written with mode `0600`, credential-like fields are redacted, and the directory is ignored by Git.

Captured files still contain private conversation data. Keep capture mode disabled during normal operation and delete files after producing sanitized synthetic fixtures.

## Security

- REST and MCP use separate Bearer tokens.
- The webhook secret is never emitted by the route logger.
- Logs do not include message bodies or authentication headers.
- UAZAPI credentials and application tokens only come from environment variables.
- Groq API keys, UAZAPI media keys, signed media URLs, audio bytes, and transcript contents are never logged.
- Downloaded audio is size-limited, kept in memory only for processing, and is not persisted by Contexta.
- MCP message data is read-only. The only state-changing tool writes idempotent, per-agent local read receipts and cannot alter WhatsApp content.
- Operational activity excludes message bodies, payloads, authentication headers, and secrets.

Run checks with:

```sh
go test ./...
go test -race ./...
go vet ./...
```
