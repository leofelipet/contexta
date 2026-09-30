# Contexta

Personal WhatsApp data platform and authenticated MCP server. Contexta receives UAZAPI webhooks, keeps an independent PostgreSQL history, exposes an administrative REST API, and gives authenticated MCP clients progressive access to conversations.

The administrative Next.js interface lives in the separate [contexta-web](https://github.com/leofelipet/contexta-web) repository.

Contexta does not contain an LLM, chatbot, response automation, or WhatsApp message-sending tool. Optional corporate email accounts (IMAP/SMTP) can be connected so MCP agents can read, organize, and send email.

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

## Email accounts

Contexta can connect IMAP/SMTP mailboxes. Mail is read live from the IMAP server and is not stored in PostgreSQL; only account settings are persisted. Passwords are encrypted at rest with AES-256-GCM using `EMAIL_CREDENTIALS_KEY`:

```sh
openssl rand -base64 32
```

Without the key, email endpoints respond `503` and existing accounts cannot be used. Losing or rotating the key makes stored passwords unreadable; re-enter each account password after changing it.

Register an account through the REST API, then verify connectivity:

```sh
curl -X POST http://localhost:8080/api/v1/email-accounts \
  -H "Authorization: Bearer $API_BEARER_TOKEN" -H "Content-Type: application/json" \
  -d '{"name":"Work","address":"me@company.com","username":"me@company.com","password":"...",
       "imap_host":"mail.company.com","imap_port":993,"smtp_host":"mail.company.com","smtp_port":465}'
curl -X POST http://localhost:8080/api/v1/email-accounts/{id}/test -H "Authorization: Bearer $API_BEARER_TOKEN"
```

TLS is on by default: IMAP port 143 and SMTP port 587 use STARTTLS, other ports use implicit TLS. `save_sent_copy` files sent mail in the Sent folder via IMAP; it defaults to `false` for Gmail and Microsoft 365, which already do this, and `true` elsewhere. Each IMAP/SMTP operation times out after 25 seconds.

`get_email` never marks messages as read; agents call `mark_email_read` explicitly. `delete_email` moves mail to the Trash folder. When the message is already in Trash, or the account has no Trash folder, it permanently expunges only that message and refuses when the server lacks UIDPLUS.

## Companies

Companies group tasks, recurring schedules, and WhatsApp contacts. A company has a unique name (case-insensitive) and free-text notes; each task, schedule, and contact links to at most one company through `company_id`.

- `GET /api/v1/tasks?company_id=`, `GET /api/v1/contacts?company_id=`, and `GET /api/v1/task-schedules?company_id=` list what is linked to a company. `list_tasks` also accepts `company` as a name substring.
- A task created with a `contact_id` and no `company_id` inherits the contact's company.
- Deleting a company keeps its tasks, schedules, and contacts, which lose the link.
- Migration `013` converted the previous free-text `company` field of tasks and schedules into companies, merging names that differ only by case or surrounding whitespace.

## Recurring tasks

The task scheduler turns cron schedules into tasks. A background worker ticks every 5 minutes on the wall clock (`:00`, `:05`, `:10`…) and once at startup; each enabled schedule whose `next_run_at` has passed creates one `pending` task from its template (`title`, `description`, `company_id`, `conversation_id`, `contact_id`, and an optional `due_in_minutes` offset). Created tasks carry `schedule_id`, and `list_tasks` / `GET /api/v1/tasks?schedule_id=` filter by it.

- `cron` is a standard 5-field expression (`0 9 * * 1-5`) or a descriptor (`@daily`, `@weekly`, `@monthly`). Expressions that fire more often than every 5 minutes are rejected; minutes off the 5-minute grid run on the next tick.
- `timezone` is an IANA name and defaults to `America/Sao_Paulo`.
- Runs missed while the server was down are collapsed into a single task on the next tick; there is no backfill.
- `skip_if_open=true` skips an occurrence while the task created by the previous run is still open.
- Each schedule is claimed with `FOR UPDATE SKIP LOCKED`, so multiple replicas never create duplicates. Set `SCHEDULER_ENABLED=false` to stop the worker on a replica.
- Deleting a schedule keeps the tasks it already created.

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
GET /api/v1/companies
POST /api/v1/companies
GET /api/v1/companies/{id}
PATCH /api/v1/companies/{id}
DELETE /api/v1/companies/{id}
POST /api/v1/companies/{id}/contacts
DELETE /api/v1/companies/{id}/contacts/{contact_id}
GET /api/v1/email-accounts
POST /api/v1/email-accounts
GET /api/v1/email-accounts/{id}
PATCH /api/v1/email-accounts/{id}
DELETE /api/v1/email-accounts/{id}
POST /api/v1/email-accounts/{id}/test
GET /api/v1/task-schedules
POST /api/v1/task-schedules
GET /api/v1/task-schedules/{id}
PATCH /api/v1/task-schedules/{id}
DELETE /api/v1/task-schedules/{id}
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
list_companies
get_company
create_company
update_company
delete_company
attach_contact_to_company
detach_contact_from_company
list_task_schedules
get_task_schedule
create_task_schedule
update_task_schedule
delete_task_schedule
```

Email tools, registered when the email service is available:

```text
list_email_accounts
list_mailboxes
search_emails
get_email
mark_email_read
set_email_flags
move_email
delete_email
send_email
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
- MCP WhatsApp data is read-only. The only WhatsApp state-changing tool writes idempotent, per-agent local read receipts and cannot alter WhatsApp content.
- Email passwords are encrypted at rest, never returned by the API or MCP, and never logged. `send_email` and `delete_email` are annotated as destructive so MCP clients can require confirmation.
- Operational activity excludes message bodies, payloads, authentication headers, and secrets.

Run checks with:

```sh
go test ./...
go test -race ./...
go vet ./...
```
