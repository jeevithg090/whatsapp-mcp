# WhatsApp MCP Server

**Let Claude Desktop, Cursor or any other MCP client search, read and send messages from your own WhatsApp account. Everything stays on your machine.**

This is a [Model Context Protocol](https://modelcontextprotocol.io) (MCP) server for WhatsApp. It connects to your **personal WhatsApp account** through the WhatsApp Web multi-device API (via the [whatsmeow](https://github.com/tulir/whatsmeow) library) and keeps your message history in a local SQLite database. Messages only reach an LLM when the agent calls one of the tools, and you control that.

![WhatsApp MCP used from Claude](./example-use.png)

> [!CAUTION]
> Like many MCP servers, this one is exposed to [the lethal trifecta](https://simonwillison.net/2025/Jun/16/the-lethal-trifecta/): it gives an agent access to private data (your chats), to untrusted content (messages other people send you) and to a way of communicating externally (sending messages). A prompt injection in an incoming message could lead to data being exfiltrated. Review tool calls before you approve them, especially `send_message`, `send_file` and `send_audio_message`.

## Features

- **Search and read** chats, contacts and messages, with filters for date range, sender, chat and text, plus pagination
- **Message context**: fetch the messages around any match
- **Send** text messages to people (by phone number) or groups (by JID)
- **Send media**: images, videos, documents and raw audio, with optional captions
- **Voice notes**: send any audio file as a playable WhatsApp voice message (converted to Ogg Opus with FFmpeg)
- **Download incoming media** (images, videos, audio, documents) on demand and get a local file path
- **History sync**: imports the message history WhatsApp shares when the device is linked
- **Local only**: the REST bridge listens on `127.0.0.1` by default and can require an API key

## Tech stack

| Component | Stack |
| --- | --- |
| WhatsApp bridge (`whatsapp-bridge/`) | Go 1.26, [whatsmeow](https://github.com/tulir/whatsmeow), SQLite ([go-sqlite3](https://github.com/mattn/go-sqlite3), needs CGO), `net/http` |
| MCP server (`whatsapp-mcp-server/`) | Python ≥ 3.11, [MCP Python SDK](https://github.com/modelcontextprotocol/python-sdk) (FastMCP), httpx, sqlite3 |
| Tooling | uv, pytest, ruff, mypy, `go test`, `go vet`, staticcheck, GitHub Actions |

## Architecture

```
┌────────────────────┐  stdio (MCP)  ┌────────────────────┐  HTTP (127.0.0.1:8080)  ┌────────────────────┐   WebSocket   ┌──────────┐
│ MCP client         │ ────────────▶ │ Python MCP server  │ ──── send / download ─▶ │ Go WhatsApp bridge │ ────────────▶ │ WhatsApp │
│ (Claude, Cursor…)  │ ◀──────────── │ whatsapp-mcp-server│                         │ whatsapp-bridge    │ ◀──────────── │ servers  │
└────────────────────┘               └─────────┬──────────┘                         └─────────┬──────────┘               └──────────┘
                                               │ reads (SQL)                                  │ writes chats + messages
                                               ▼                                              ▼
                                        ┌─────────────────────────────────────────────────────────┐
                                        │ whatsapp-bridge/store/messages.db (SQLite)              │
                                        │ whatsapp-bridge/store/whatsapp.db (session keys)        │
                                        │ whatsapp-bridge/store/<chat>/… (downloaded media)       │
                                        └─────────────────────────────────────────────────────────┘
```

1. **Go bridge** (`whatsapp-bridge/`) logs in as a linked device (QR code on first run), receives live messages and history syncs, and writes chats and messages to `store/messages.db`. It exposes a small REST API:
   - `POST /api/send`: `{"recipient", "message", "media_path"?}` sends text, or a local file with an optional caption
   - `POST /api/download`: `{"message_id", "chat_jid"}` downloads and decrypts a message's media into `store/` and returns its absolute path
   - `GET /api/health`: liveness check
2. **Python MCP server** (`whatsapp-mcp-server/`) speaks MCP over stdio. Read-only tools query `messages.db` directly, and the send and download tools call the bridge's REST API.

Message timestamps are stored in UTC and shown in your local time zone. `messages.db` has indexes on `(chat_jid, timestamp)`, `timestamp` and `sender`.

## Prerequisites

- **Go 1.26+** with **CGO enabled** and a C compiler (gcc/clang). go-sqlite3 needs these. On Windows, see [below](#windows).
- **Python 3.11+** and **[uv](https://docs.astral.sh/uv/)**: `curl -LsSf https://astral.sh/uv/install.sh | sh`
- An MCP client such as **Claude Desktop** or **Cursor**
- **FFmpeg** (_optional_), only needed to send non-`.ogg` audio as voice notes. Without it you can still send audio files as attachments with `send_file`.
- A phone with WhatsApp, used to link the bridge as a device

## Setup

1. **Clone the repository**

   ```bash
   git clone https://github.com/jeevithg090/whatsapp-mcp.git
   cd whatsapp-mcp
   ```

2. **Run the WhatsApp bridge**

   ```bash
   cd whatsapp-bridge
   go run .
   ```

   The first time, a QR code is printed. In WhatsApp on your phone, open **Settings → Linked devices → Link a device** and scan it. The session is saved in `store/whatsapp.db`, so later runs reconnect without a QR code. You may need to re-link roughly every 20 days.

   Leave the bridge running. The MCP server needs it for sending messages and downloading media, and it keeps the database up to date.

3. **Configure your MCP client**

   Find the paths you need with `which uv` and, from the repository root, `pwd`:

   ```json
   {
     "mcpServers": {
       "whatsapp": {
         "command": "/absolute/path/to/uv",
         "args": [
           "--directory",
           "/absolute/path/to/whatsapp-mcp/whatsapp-mcp-server",
           "run",
           "main.py"
         ]
       }
     }
   }
   ```

   - **Claude Desktop**: save it as `claude_desktop_config.json` in the configuration directory (`~/Library/Application Support/Claude/` on macOS, `%APPDATA%\Claude\` on Windows).
   - **Cursor**: save it as `~/.cursor/mcp.json`.

   If you set an API key on the bridge, add it to the server's environment:

   ```json
   "env": { "WHATSAPP_BRIDGE_API_KEY": "the-same-secret-as-the-bridge" }
   ```

4. **Restart Claude Desktop / Cursor.** WhatsApp should now be listed as an available integration.

### Windows

go-sqlite3 needs **CGO**, which is disabled by default on Windows. Install a C compiler (for example via [MSYS2](https://www.msys2.org/), adding `ucrt64\bin` to your `PATH`; see [this guide](https://code.visualstudio.com/docs/cpp/config-mingw)) and then:

```bash
cd whatsapp-bridge
go env -w CGO_ENABLED=1
go run .
```

Without this you will see `Binary was compiled with 'CGO_ENABLED=0', go-sqlite3 requires cgo to work.`

## Environment variables

All settings are optional. [`.env.example`](./.env.example) lists them with their defaults. Neither component loads a `.env` file itself, so export the variables in your shell for the bridge and put the server's in your MCP client's `env` block.

| Variable | Used by | Default | Description |
| --- | --- | --- | --- |
| `WHATSAPP_BRIDGE_STORE_DIR` | bridge | `store` (relative to the working directory) | Where `whatsapp.db`, `messages.db` and downloaded media are kept |
| `WHATSAPP_BRIDGE_ADDR` | bridge | `127.0.0.1:8080` | Listen address of the REST API |
| `WHATSAPP_BRIDGE_API_KEY` | bridge + server | _unset_ | Shared secret. When set, the bridge rejects requests without a matching `X-API-Key` (or `Authorization: Bearer`) header, and the server sends it |
| `WHATSAPP_BRIDGE_LOG_LEVEL` | bridge | `INFO` | `DEBUG`, `INFO`, `WARN` or `ERROR` |
| `WHATSAPP_DB_PATH` | server | `../whatsapp-bridge/store/messages.db` | Path to the bridge's message database |
| `WHATSAPP_API_BASE_URL` | server | `http://127.0.0.1:8080/api` | Bridge REST API base URL |
| `WHATSAPP_REQUEST_TIMEOUT` | server | `60` | Seconds to wait for the bridge (uploads can be slow) |

If you change `WHATSAPP_BRIDGE_STORE_DIR` or run the bridge from another directory, point `WHATSAPP_DB_PATH` at the resulting `messages.db`.

## MCP tools

| Tool | Description |
| --- | --- |
| `search_contacts` | Search contacts by name or phone number |
| `list_messages` | Messages filtered by date range (`after`/`before`, ISO-8601), sender, chat or text, with optional surrounding context |
| `list_chats` | Chats filtered by name/JID, sorted by last activity or name, with the last message |
| `get_chat` | Metadata for a chat by JID |
| `get_direct_chat_by_contact` | The direct chat with a phone number |
| `get_contact_chats` | All chats (direct and group) a contact has written in |
| `get_last_interaction` | The most recent message involving a contact |
| `get_message_context` | A message plus the messages before and after it |
| `send_message` | Send text to a phone number (with country code) or a JID (`…@s.whatsapp.net`, group `…@g.us`) |
| `send_file` | Send an image, video, document or raw audio file, with an optional caption |
| `send_audio_message` | Send audio as a voice note (non-`.ogg` files are converted with FFmpeg) |
| `download_media` | Download a message's media and return the local file path |

### Media

- **Sending**: the file is chosen by its extension. `.jpg/.jpeg/.png/.gif/.webp` go as images, `.mp4/.mov/.avi` as videos, `.ogg/.opus` as voice notes, and anything else as a document (MIME type guessed from the extension). Files up to 100 MiB are accepted. Relative paths are resolved by the MCP server before they are handed to the bridge.
- **Receiving**: only media metadata is stored at first, and messages show `[image - Message ID: … - Chat JID: …]`. Call `download_media` with that message ID and chat JID to fetch the file into `whatsapp-bridge/store/<chat>/`. Filenames are sanitised and prefixed with the message ID, so names chosen by the sender can't escape the store directory or overwrite each other.

## Development

A `Makefile` wraps the common commands (CI runs the same steps):

```bash
make lint    # gofmt, go vet, staticcheck, ruff check, ruff format --check, mypy
make test    # go test -race ./... and pytest
make build   # builds whatsapp-bridge/whatsapp-bridge
make fmt     # gofmt -w + ruff format/fix
```

Or run them directly:

```bash
# Go bridge
cd whatsapp-bridge
go test -race ./...
go vet ./...
go build -o whatsapp-bridge .

# Python MCP server
cd whatsapp-mcp-server
uv sync
uv run pytest
uv run ruff check . && uv run ruff format --check . && uv run mypy
```

The Python tests run against a temporary SQLite database with the bridge's schema and a mocked bridge API, and include an end-to-end stdio MCP session. The Go tests cover the REST handlers (including API-key auth), the SQLite store, filename sanitisation, recipient parsing and the Ogg Opus duration parser (using a real FFmpeg-encoded fixture). Nothing in the test suite talks to WhatsApp.

## Deployment notes

This project is meant to run **on your own computer**, next to your MCP client:

- The MCP server is started by the MCP client over stdio, so there is nothing to host.
- The bridge must keep running for sending and for media downloads. To run it in the background, build a binary (`make build`) and start it from a process manager such as launchd, systemd (`WorkingDirectory=/path/to/whatsapp-bridge`) or a terminal multiplexer. Run it interactively the first time so you can scan the QR code.
- The bridge reads local file paths sent to `/api/send` and writes media to its store, so the MCP server and the bridge need to share a filesystem. That is why there is no container image.
- Keep `WHATSAPP_BRIDGE_ADDR` on `127.0.0.1`. If you must expose it elsewhere, set `WHATSAPP_BRIDGE_API_KEY` and put it behind TLS. Anyone who can reach the API can send messages as you and make the bridge read local files.
- `whatsapp-bridge/store/` holds your WhatsApp session keys and full message history. Treat it as sensitive: it is git-ignored, and the bridge creates it with owner-only permissions. Don't sync it to shared locations.

## Project structure

```
.
├── .github/workflows/ci.yml   # Lint, test and build both components
├── .env.example               # All supported environment variables
├── Makefile                   # lint / test / build / fmt shortcuts
├── whatsapp-bridge/           # Go: WhatsApp connection + REST API
│   ├── main.go                # Startup, QR pairing, graceful shutdown
│   ├── config.go              # Environment configuration
│   ├── api.go                 # REST handlers, API-key middleware, server timeouts
│   ├── events.go              # Live message and history-sync handling, chat names
│   ├── store.go               # SQLite schema and queries (messages.db)
│   ├── send.go                # Recipient parsing, text/media message sending
│   ├── media.go               # Media metadata, safe local paths, downloads
│   ├── ogg.go                 # Ogg Opus duration + waveform for voice notes
│   ├── *_test.go, testdata/   # Unit tests and fixtures
│   └── go.mod / go.sum
├── whatsapp-mcp-server/       # Python: MCP server
│   ├── main.py                # FastMCP tool definitions (stdio entry point)
│   ├── whatsapp.py            # SQLite queries, formatting, bridge REST client
│   ├── audio.py               # FFmpeg Ogg Opus conversion
│   ├── config.py              # Environment configuration
│   ├── tests/                 # pytest suite
│   └── pyproject.toml / uv.lock
└── example-use.png
```

## Troubleshooting

- **`uv` permission or "command not found" errors**: use the absolute path from `which uv` in the MCP config.
- **"Message database not found"**: start the bridge at least once, or set `WHATSAPP_DB_PATH` to the right `messages.db`.
- **"Could not reach the WhatsApp bridge"**: the bridge isn't running, or `WHATSAPP_API_BASE_URL` doesn't match `WHATSAPP_BRIDGE_ADDR`.
- **"check WHATSAPP_BRIDGE_API_KEY"**: the key set for the MCP server doesn't match the bridge's.
- **QR code not displaying**: restart the bridge and make sure your terminal is wide enough for the code. If the QR code times out, just run the bridge again.
- **Already logged in**: with a saved session the bridge reconnects automatically without a QR code.
- **Device limit reached**: remove a linked device in WhatsApp (**Settings → Linked devices**).
- **No messages yet**: after linking, history sync can take several minutes for large accounts.
- **Out of sync or logged out**: stop the bridge, delete `whatsapp-bridge/store/messages.db` and `whatsapp-bridge/store/whatsapp.db`, and start it again to re-link.

For MCP client issues, see the [MCP documentation](https://modelcontextprotocol.io/quickstart/server#claude-for-desktop-integration-issues), which covers checking logs and common integration problems.

## Credits and license

Originally created by [Luke Harries](https://github.com/lharries) ([lharries/whatsapp-mcp](https://github.com/lharries/whatsapp-mcp)). This fork keeps the original [MIT License](./LICENSE).
