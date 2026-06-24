# h2 Linear relay — deployment

The relay lets users plug h2 into Linear with one click. It's a single stateless
(except for the install state file) HTTP service. See
[../../docs/linear-agent-setup.md](../../docs/linear-agent-setup.md) for the full
picture; this is the ops quickstart.

## What you need

1. A **public HTTPS URL** for the relay (e.g. `https://relay.example.com`).
2. A **published Linear OAuth app** (Settings → API → OAuth applications):
   - Redirect URI: `https://relay.example.com/oauth/callback`
   - Webhook URL: `https://relay.example.com/webhook`, enable **Agent session events**
   - Scopes: `read`, `write`, `app:assignable`, `app:mentionable`
   - Note the **client id**, **client secret**, and **webhook signing secret**.

## Run with Docker Compose

```bash
cp deploy/linear-relay/.env.example deploy/linear-relay/.env
# edit .env with your base URL + OAuth credentials
docker compose -f deploy/linear-relay/docker-compose.yml up -d --build
```

Put TLS in front of it (your platform's load balancer, Caddy, nginx, or a
Cloudflare tunnel). `H2_RELAY_STATE_PATH` persists installs to the mounted
volume, so restarts don't drop connected workspaces.

## Run directly

```bash
H2_RELAY_BASE_URL=https://relay.example.com \
H2_RELAY_CLIENT_ID=... \
H2_RELAY_CLIENT_SECRET=... \
H2_RELAY_WEBHOOK_SECRET=... \
H2_RELAY_STATE_PATH=/var/lib/h2/relay-state.json \
h2 linear relay
```

## Health & endpoints

- `GET /healthz` — liveness probe
- `GET /oauth/authorize` — the user-facing install URL
- `GET /oauth/callback` — OAuth redirect target
- `POST /webhook` — Linear agent-session webhooks (signature-verified)
- `GET /poll`, `POST /activity` — daemon long-poll + activity proxy

## Onboarding a user

Send them to `https://relay.example.com/oauth/authorize`. After authorizing,
they copy the pairing token into `~/.h2/config.yaml` and run `h2 linear serve`
(see the user quickstart in the setup doc).

## Notes

- Workspace OAuth tokens live only on the relay (in `H2_RELAY_STATE_PATH`,
  written `0600`). User daemons dial out and never receive the token.
- The state file contains secrets — back it up securely and restrict access.
