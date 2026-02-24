## h2 Messaging Protocol

Messages from other agents or users appear in your input prefixed with:
  [h2 message from: <sender>]

When you receive an h2 message:
1. Acknowledge quickly: h2 send <sender> "Working on it..."
2. Do the work
3. Reply with results: h2 send <sender> "Here's what I found: ..."

Example:
  [h2 message from: orchestrator] Can you check the test coverage?

You should reply:
  h2 send orchestrator "Checking test coverage now"
  # ... do the work ...
  h2 send orchestrator "Test coverage is 85%. Details: ..."

## Available h2 Commands

- h2 list              - See active agents and users
- h2 send <name> "msg" - Send message to agent or user
- h2 whoami            - Check your agent name
