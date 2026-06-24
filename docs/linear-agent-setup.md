# Linear Agent Setup

Run h2 as a Linear **agent**: delegate or `@mention` an issue to `h2` and it
spawns an agent to work it, reporting progress back to the issue's agent-session
activity feed.

There are two transports:

- **Relay (recommended, plug-and-play)** — connect to a hosted relay; the only
  thing you provide is a pairing token you get by clicking "Authorize h2". No
  OAuth app, no webhook config, no tunnel, no Linear token on your machine. See
  [Quick start](#quick-start-relay).
- **Webhook (dev / self-host)** — you register your own OAuth app and run a
  public webhook receiver. See [Self-host with webhook](#self-host-with-webhook).

Design details live in
[plan-linear-agent-delegation.md](plan-linear-agent-delegation.md).

## Quick start (relay)

For end users, once an h2 relay is running and its Linear app is published:

1. Open the relay's install URL (`https://<relay-host>/oauth/authorize`) and
   authorize **h2** into your workspace.
2. Copy the **pairing token** from the confirmation page into
   `~/.h2/config.yaml`:
   ```yaml
   linear:
     inbound:
       mode: relay
       relay_url: "https://<relay-host>"   # omit to use the built-in default
       pairing_token: "<from the authorize page>"
     agent:
       role: "default"                      # an existing h2 role
   ```
3. Run it:
   ```bash
   h2 linear serve
   ```
4. Delegate an issue to **h2** in Linear. That's it — no tunnel, no secrets.

The rest of this doc is for **operators** (running the relay) and **dev /
self-host** (webhook transport).

## Running the relay (operator)

The relay is the single public endpoint for a published Linear OAuth app. Run it
on a host with a public URL.

1. Register one OAuth app (see [below](#self-host-with-webhook) for the field
   details), enable webhooks + **Agent session events**, scopes
   `read`/`write`/`app:assignable`/`app:mentionable`. Set its **redirect URI**
   to `https://<relay-host>/oauth/callback` and its **webhook URL** to
   `https://<relay-host>/webhook`.
2. Config on the relay host:
   ```yaml
   linear:
     relay:
       address: ":8080"
       base_url: "https://<relay-host>"
       client_id: "<oauth client id>"
       client_secret: "<oauth client secret>"
       webhook_secret: "<webhook signing secret>"
   ```
3. `h2 linear relay`

Each user then follows [Quick start](#quick-start-relay). Workspace OAuth tokens
are held only by the relay; user daemons dial out and never receive the token.

## Self-host with webhook

## 1. Register the Linear OAuth app

In Linear: **Settings → Administration → API → OAuth applications → Create**.

- Configure it as a standard OAuth app (callback URL can be a placeholder for
  agent-only use).
- **Enable webhooks** and select **Agent session events**.
- Request scopes: `read`, `write`, `app:assignable`, `app:mentionable`.
  (Do **not** request `admin` — it cannot combine with `actor=app`.)
- Authorize the app into your workspace with `actor=app` (this makes h2 appear
  as its own agent, not a user). Admin permission required.
- Note the **access token** and the **webhook signing secret**.

To find the agent's identity (optional, for reference), query `viewer { id }`
with the access token.

## 2. Configure h2

Add a `linear` block to `~/.h2/config.yaml`:

```yaml
linear:
  oauth_token: "<actor=app access token>"
  inbound:
    mode: webhook
    address: ":4747"            # local listen address
    path: "/linear/webhook"     # must match the webhook URL path below
    secret: "<webhook signing secret>"
  agent:
    role: "default"             # an existing h2 role used to spawn agents
```

The `agent.role` must exist (e.g. `~/.h2/roles/default.yaml`). If that role has
`worktree_enabled`, each delegated issue runs in its own git worktree.

## 3. Expose the receiver

Linear must reach the local receiver. For dev, use a tunnel:

```bash
ngrok http 4747          # or: hookdeck listen 4747
```

Point the OAuth app's webhook URL at `https://<tunnel-host>/linear/webhook`
(path must match `inbound.path`).

> A zero-config outbound-dialed relay (no tunnel) is planned; see the plan doc.

## 4. Run

```bash
h2 linear serve            # add --debug to log raw inbound payloads
```

Then in Linear, open an issue and **delegate it to `h2`** (assignee/delegate
menu) or `@mention` h2 in a comment. You should see:

1. a `thought` ack ("On it — starting work on LIN-…") within seconds,
2. streamed `thought`/`action` activities while the agent works,
3. a `response` when the agent's turn completes.

Follow-up comments on the same session are routed to the running agent. If the
agent blocks on a permission prompt, h2 posts an `elicitation` asking you to
approve it (`h2 attach <agent>`).

## Troubleshooting

- **Nothing happens on delegation** — run with `--debug` and check the
  `[debug] inbound webhook` line. If absent, the tunnel/webhook URL or event
  selection is wrong. If present but no spawn, the payload shape may differ from
  what h2 parses (share the logged payload).
- **`invalid signature` (401)** — `inbound.secret` doesn't match the app's
  webhook signing secret.
- **`401` posting activities** — `oauth_token` is missing/invalid or lacks
  scopes; re-authorize with `actor=app` + the scopes above.
- **`load role … no such file`** — `agent.role` points at a role that doesn't
  exist.

## Current limitations

- The `response` is a summary pointing back to the agent
  (`h2 attach <name>`), not the agent's verbatim final message.
- Turn-completion is heuristic (agent settles into idle, or exits).
- Single configured role; per-team role mapping is not yet implemented.
