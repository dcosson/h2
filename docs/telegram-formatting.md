# Telegram text formatting

Outgoing Telegram text messages recognize a deliberately small Markdown-like
subset, without changing stored message bodies or the CLI interface:

- `**important text**` becomes bold (also useful for headings).
- Single backticks mark inline code, such as commands and paths.
- Triple-backtick fenced blocks become copyable preformatted code. An optional
  language label such as `bash` is passed to Telegram.
- Paragraph spacing, bullets and numbered steps stay as written.

This is not a full Markdown parser: headings using `#`, links, tables, nested
emphasis, italic and arbitrary HTML are not interpreted. Use explicit `**bold**`
headings. Unsupported delimiter runs and unclosed blocks remain literal. Code
contents are never interpreted as emphasis. Plain text colors and fonts follow
Telegram's client/theme, not terminal ANSI colors. Image captions are unchanged.

The bridge sends text plus Telegram `entities`, not `parse_mode`. This avoids
HTML/Markdown escaping failures for shell operators, paths and other literal
text. Entity offsets use UTF-16 units, including emoji. Long messages are split
at UTF-8 boundaries within a conservative 4096 UTF-16-unit budget, preferably
at newlines. Spanning entities are clipped and rebased per page. The existing
three-page cap remains; its truncation notice is outside formatting entities.

For literal markup, put it inside a code span/block. A backslash before an inline
opening marker prevents formatting; that backslash is retained. Single-line,
non-nested bold/code and unindented triple-backtick blocks are the supported
syntax; this first increment intentionally does not try to render all Markdown.

Example message body:

~~~~text
**Status**
• Bridgen kjører.
• **Ingen agentrestart nødvendig.**

**Sjekk**
Kjør `h2o list --all`:

```bash
h2o list --all
```
~~~~

Telegram API reference: https://core.telegram.org/bots/api#messageentity
