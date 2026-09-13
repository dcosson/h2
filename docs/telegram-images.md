# Telegram images

The Telegram bridge can receive photos (including captions) and JPEG/PNG
images sent as documents. The largest photo variant is downloaded once; photo
variants are alternate sizes, not separate attachments. Albums arrive as
individual messages. GIFs, stickers, video and arbitrary documents are outside
this feature.

Incoming images are validated and saved with random names and mode `0600` in
`$H2_DIR/attachments/<bridge-name>/` (directory mode `0700`). Original remote
filenames are never used locally. The agent receives the caption and an
absolute local image path in its normal message, including normal
expects-response handling. It must use its image-viewing tool to inspect the
image: this is not native image injection into a harness's prompt. The agent
needs filesystem access to the attachments directory and an image-viewing tool.

`agent-name:` prefixes in captions and replies to agent-tagged image captions
route just like text messages. Captions are not executed as slash commands.
Only messages from the configured chat are downloaded. Images are not fetched
from arbitrary links in user text.

## Send a local image

```sh
h2 send telegram --image ./screenshot.png "Here is the screenshot"
h2 send telegram --image ./screenshot.png
h2 send --closes <trigger-id> telegram --image ./result.png "Result"
```

Replace `telegram` with your configured bridge name. The optional body (or
`--file` contents) is a plain-text caption. Non-concierge sender tags are added
as for text. `--closes` sends the image successfully before removing the reminder.
`--raw`, `--expects-response` and agent targets are not supported with `--image`.
Symlinks and non-regular files are rejected. Files are read by the local bridge
process, under the same user as the CLI. The path is explicitly selected via
`--image`; paths mentioned in ordinary message bodies never trigger uploads.

An optional bridge `ImageSender` capability leaves text-only bridges unchanged.
A separate `send-image` socket operation makes older bridges reject unsupported
image requests instead of silently sending only the caption. Restart the bridge
with the updated binary before using images. No other daemon/harness needs image
protocol changes for incoming images; they receive ordinary text with a path.

## Bounds, privacy and errors

- JPEG/PNG only, at most 10 MiB and 40 megapixels. Both metadata and actual
  downloaded bytes are checked, and the image is decoded to detect corrupt data.
- Plain captions are limited to 1024 characters, including any sender tag.
  Overlong captions are rejected, never silently truncated.
- Image HTTP operations have a 60-second deadline and do not follow redirects.
  Download paths are validated and token-bearing request URLs are not included
  in image errors. API response bodies are not echoed in image errors.
- Failed downloads produce a visible error, not a caption that pretends the
  image arrived. No partial local image is retained on a failed write.
- Attachments persist for delayed agent delivery and session history. There is
  no automatic expiration or total-storage quota in this change. Monitor disk
  usage and remove old attachments when they are no longer needed; removing
  them will make historical image references unreadable. Do not commit this
  private attachment directory or expose it through a web server.
- A failed outbound request can be ambiguous if Telegram accepted the image but
  the connection failed before the response. Uploads are not automatically retried.

Telegram reference: [Bot API sendPhoto](https://core.telegram.org/bots/api#sendphoto)
and [getFile](https://core.telegram.org/bots/api#getfile). Telegram may reject
additional dimensions/aspect ratios even when local validation passes. Its API
error is surfaced as a failed upload, not a success.

Tests use synthetic PNGs, mock HTTP APIs and temporary Unix sockets/directories;
no real bot credentials, live agents or Telegram chat are needed. Live validation
should use a harmless image in each direction after an explicitly coordinated
bridge restart.
