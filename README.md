# Contexta

Personal WhatsApp data platform and read-only MCP server. Contexta receives UAZAPI webhooks, keeps an independent PostgreSQL history, exposes an administrative REST API, and gives authenticated MCP clients progressive access to conversations.

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
GET /api/v1/messages/{id}
GET /api/v1/messages/{id}/around
GET /api/v1/dashboard
GET /api/v1/integrations/uazapi
POST /api/v1/integrations/uazapi/configure-webhook
GET /api/v1/mcp/status
GET /api/v1/activity
```

Common message filters are `query`, `from`, `to`, `contact_id`, `conversation_id`, `direction`, `type`, `limit`, and `cursor`. Dates accept RFC3339 or `YYYY-MM-DD` in UTC.

The Streamable HTTP MCP endpoint is:

```text
POST /mcp
Authorization: Bearer $MCP_BEARER_TOKEN
```

Available read-only tools:

```text
search_messages
find_conversations
get_conversation
get_messages
get_messages_around
list_contacts
get_contact
```

## Webhook fixtures

UAZAPI 2.1.1 does not document complete `messages`, `history`, and `messages_update` webhook payloads. Set `UAZAPI_CAPTURE_DIR=.contexta-captures` temporarily to capture real payload structures. Captures are written with mode `0600`, credential-like fields are redacted, and the directory is ignored by Git.

Captured files still contain private conversation data. Keep capture mode disabled during normal operation and delete files after producing sanitized synthetic fixtures.

## Security

- REST and MCP use separate Bearer tokens.
- The webhook secret is never emitted by the route logger.
- Logs do not include message bodies or authentication headers.
- UAZAPI credentials and application tokens only come from environment variables.
- MCP tools are read-only and expose response models rather than database metadata.
- Operational activity excludes message bodies, payloads, authentication headers, and secrets.

Run checks with:

```sh
go test ./...
go test -race ./...
go vet ./...
```
