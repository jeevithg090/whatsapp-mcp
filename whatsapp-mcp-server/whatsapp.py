"""Data access for the WhatsApp MCP server.

Reads chats and messages from the SQLite database maintained by the Go bridge
and talks to the bridge's REST API to send messages and download media.
"""

from __future__ import annotations

import logging
import os
import sqlite3
from collections.abc import Iterator, Sequence
from contextlib import contextmanager
from dataclasses import dataclass, field
from datetime import UTC, datetime
from typing import Any

import httpx

import audio
from config import Settings, load_settings

logger = logging.getLogger(__name__)

MAX_LIMIT = 200
MAX_CONTEXT = 50
GROUP_SUFFIX = "@g.us"
USER_SERVER = "s.whatsapp.net"


class WhatsAppError(Exception):
    """An error that should be reported to the MCP client as a tool error."""


@dataclass
class Message:
    timestamp: datetime
    sender: str
    content: str
    is_from_me: bool
    chat_jid: str
    id: str
    chat_name: str | None = None
    media_type: str | None = None

    def to_dict(self) -> dict[str, Any]:
        return {
            "id": self.id,
            "chat_jid": self.chat_jid,
            "chat_name": self.chat_name,
            "sender": self.sender,
            "content": self.content,
            "timestamp": self.timestamp.isoformat(),
            "is_from_me": self.is_from_me,
            "media_type": self.media_type,
        }


@dataclass
class Chat:
    jid: str
    name: str | None
    last_message_time: datetime | None
    last_message: str | None = None
    last_sender: str | None = None
    last_is_from_me: bool | None = None

    @property
    def is_group(self) -> bool:
        """Whether this chat is a group (group JIDs end with ``@g.us``)."""
        return self.jid.endswith(GROUP_SUFFIX)

    def to_dict(self) -> dict[str, Any]:
        return {
            "jid": self.jid,
            "name": self.name,
            "is_group": self.is_group,
            "last_message_time": self.last_message_time.isoformat() if self.last_message_time else None,
            "last_message": self.last_message,
            "last_sender": self.last_sender,
            "last_is_from_me": self.last_is_from_me,
        }


@dataclass
class Contact:
    phone_number: str
    name: str | None
    jid: str

    def to_dict(self) -> dict[str, Any]:
        return {"phone_number": self.phone_number, "name": self.name, "jid": self.jid}


@dataclass
class MessageContext:
    message: Message
    before: list[Message] = field(default_factory=list)
    after: list[Message] = field(default_factory=list)

    def to_dict(self) -> dict[str, Any]:
        return {
            "message": self.message.to_dict(),
            "before": [m.to_dict() for m in self.before],
            "after": [m.to_dict() for m in self.after],
        }


# --------------------------------------------------------------------------- #
# Helpers
# --------------------------------------------------------------------------- #


def _settings() -> Settings:
    return load_settings()


@contextmanager
def _connect() -> Iterator[sqlite3.Connection]:
    """Open the bridge's message database, failing clearly if it does not exist yet."""
    db_path = _settings().db_path
    if not db_path.is_file():
        raise WhatsAppError(
            f"Message database not found at {db_path}. Start the WhatsApp bridge first (or set WHATSAPP_DB_PATH)."
        )
    conn = sqlite3.connect(db_path, timeout=10)
    try:
        yield conn
    except sqlite3.Error as exc:
        logger.exception("Database error")
        raise WhatsAppError(f"Database error: {exc}") from exc
    finally:
        conn.close()


def _clamp(value: int, low: int, high: int) -> int:
    return max(low, min(int(value), high))


def _like_pattern(text: str) -> str:
    """Build a ``%text%`` LIKE pattern, escaping wildcard characters in ``text``."""
    escaped = text.replace("\\", "\\\\").replace("%", "\\%").replace("_", "\\_")
    return f"%{escaped}%"


def parse_db_timestamp(value: Any) -> datetime:
    """Parse a timestamp stored by the bridge and convert it to local time.

    The bridge stores timestamps via go-sqlite3 as e.g. ``2025-05-01 06:30:00+00:00``
    (optionally with fractional seconds). Naive values are assumed to be UTC.
    """
    if isinstance(value, datetime):
        parsed = value
    else:
        text = str(value).strip().replace("Z", "+00:00")
        # fromisoformat accepts at most microsecond precision on older Pythons.
        if "." in text:
            head, _, rest = text.partition(".")
            digits = ""
            while rest and rest[0].isdigit():
                digits, rest = digits + rest[0], rest[1:]
            text = f"{head}.{digits[:6].ljust(6, '0')}{rest}"
        parsed = datetime.fromisoformat(text)
    if parsed.tzinfo is None:
        parsed = parsed.replace(tzinfo=UTC)
    return parsed.astimezone()


def to_db_timestamp(value: str, name: str) -> str:
    """Convert a user-supplied ISO-8601 string into the bridge's UTC storage format.

    Naive values are interpreted in the local timezone.
    """
    try:
        parsed = datetime.fromisoformat(value.strip().replace("Z", "+00:00"))
    except ValueError as exc:
        raise WhatsAppError(
            f"Invalid date for {name!r}: {value!r}. Use ISO-8601, e.g. 2025-01-31 or 2025-01-31T18:00:00."
        ) from exc
    if parsed.tzinfo is None:
        parsed = parsed.astimezone()
    return parsed.astimezone(UTC).strftime("%Y-%m-%d %H:%M:%S+00:00")


def normalize_phone(value: str) -> str:
    """Reduce a phone number or user JID to the bare user part stored by the bridge."""
    value = value.strip()
    if "@" in value:
        value = value.split("@", 1)[0]
    value = value.split(":", 1)[0]  # strip a device suffix such as "123:4"
    return "".join(ch for ch in value if ch.isdigit()) or value


_MESSAGE_COLUMNS = (
    "messages.timestamp, messages.sender, chats.name, messages.content, "
    "messages.is_from_me, messages.chat_jid, messages.id, messages.media_type"
)


def _row_to_message(row: Sequence[Any]) -> Message:
    return Message(
        timestamp=parse_db_timestamp(row[0]),
        sender=row[1] or "",
        chat_name=row[2],
        content=row[3] or "",
        is_from_me=bool(row[4]),
        chat_jid=row[5],
        id=row[6],
        media_type=row[7] or None,
    )


def _row_to_chat(row: Sequence[Any]) -> Chat:
    return Chat(
        jid=row[0],
        name=row[1],
        last_message_time=parse_db_timestamp(row[2]) if row[2] else None,
        last_message=row[3],
        last_sender=row[4],
        last_is_from_me=bool(row[5]) if row[5] is not None else None,
    )


def _lookup_sender_name(conn: sqlite3.Connection, sender: str) -> str:
    """Resolve a sender (phone number or JID) to a contact name, falling back to the sender."""
    if not sender:
        return "Unknown"
    candidates = [sender] if "@" in sender else [f"{sender}@{USER_SERVER}", sender]
    for jid in candidates:
        row = conn.execute("SELECT name FROM chats WHERE jid = ? LIMIT 1", (jid,)).fetchone()
        if row and row[0]:
            return str(row[0])
    if "@" not in sender:
        row = conn.execute(
            "SELECT name FROM chats WHERE jid LIKE ? ESCAPE '\\' AND jid NOT LIKE '%@g.us' LIMIT 1",
            (_like_pattern(sender + "@")[1:],),
        ).fetchone()
        if row and row[0]:
            return str(row[0])
    return sender


def get_sender_name(sender_jid: str) -> str:
    """Return the contact name for a sender JID or phone number, or the input if unknown."""
    with _connect() as conn:
        return _lookup_sender_name(conn, sender_jid)


def format_message(message: Message, sender_name: str | None = None, show_chat_info: bool = True) -> str:
    """Format a message as a single human-readable line."""
    stamp = f"[{message.timestamp:%Y-%m-%d %H:%M:%S}]"
    chat = f" Chat: {message.chat_name}" if show_chat_info and message.chat_name else ""
    sender = "Me" if message.is_from_me else (sender_name or message.sender)
    media = (
        f"[{message.media_type} - Message ID: {message.id} - Chat JID: {message.chat_jid}] "
        if message.media_type
        else ""
    )
    return f"{stamp}{chat} From: {sender}: {media}{message.content}"


def _format_messages(conn: sqlite3.Connection, messages: Sequence[Message], show_chat_info: bool = True) -> str:
    if not messages:
        return "No messages to display."
    names: dict[str, str] = {}
    lines = []
    for message in messages:
        name = None
        if not message.is_from_me:
            if message.sender not in names:
                names[message.sender] = _lookup_sender_name(conn, message.sender)
            name = names[message.sender]
        lines.append(format_message(message, name, show_chat_info))
    return "\n".join(lines)


def format_messages_list(messages: Sequence[Message], show_chat_info: bool = True) -> str:
    """Format a list of messages, resolving sender names from the database."""
    if not messages:
        return "No messages to display."
    with _connect() as conn:
        return _format_messages(conn, messages, show_chat_info)


# --------------------------------------------------------------------------- #
# Queries
# --------------------------------------------------------------------------- #


def list_messages(
    after: str | None = None,
    before: str | None = None,
    sender_phone_number: str | None = None,
    chat_jid: str | None = None,
    query: str | None = None,
    limit: int = 20,
    page: int = 0,
    include_context: bool = True,
    context_before: int = 1,
    context_after: int = 1,
) -> str:
    """Return messages matching the filters, newest first, formatted as text."""
    limit = _clamp(limit, 1, MAX_LIMIT)
    page = max(0, int(page))

    where: list[str] = []
    params: list[Any] = []
    if after:
        where.append("messages.timestamp > ?")
        params.append(to_db_timestamp(after, "after"))
    if before:
        where.append("messages.timestamp < ?")
        params.append(to_db_timestamp(before, "before"))
    if sender_phone_number:
        where.append("messages.sender = ?")
        params.append(normalize_phone(sender_phone_number))
    if chat_jid:
        where.append("messages.chat_jid = ?")
        params.append(chat_jid.strip())
    if query:
        where.append("LOWER(messages.content) LIKE LOWER(?) ESCAPE '\\'")
        params.append(_like_pattern(query))

    sql = f"SELECT {_MESSAGE_COLUMNS} FROM messages JOIN chats ON messages.chat_jid = chats.jid"
    if where:
        sql += " WHERE " + " AND ".join(where)
    sql += " ORDER BY messages.timestamp DESC LIMIT ? OFFSET ?"
    params.extend([limit, page * limit])

    with _connect() as conn:
        result = [_row_to_message(row) for row in conn.execute(sql, params).fetchall()]

        if include_context and result:
            context_before = _clamp(context_before, 0, MAX_CONTEXT)
            context_after = _clamp(context_after, 0, MAX_CONTEXT)
            seen: set[tuple[str, str]] = set()
            expanded: list[Message] = []
            for msg in result:
                ctx = _message_context(conn, msg.id, context_before, context_after, msg.chat_jid)
                for item in [*ctx.before, ctx.message, *ctx.after]:
                    key = (item.chat_jid, item.id)
                    if key not in seen:
                        seen.add(key)
                        expanded.append(item)
            result = expanded

        return _format_messages(conn, result, show_chat_info=True)


def _message_context(
    conn: sqlite3.Connection, message_id: str, before: int, after: int, chat_jid: str | None = None
) -> MessageContext:
    sql = (
        f"SELECT {_MESSAGE_COLUMNS}, messages.timestamp AS raw_ts FROM messages "
        "JOIN chats ON messages.chat_jid = chats.jid WHERE messages.id = ?"
    )
    params: list[Any] = [message_id]
    if chat_jid:
        sql += " AND messages.chat_jid = ?"
        params.append(chat_jid)
    row = conn.execute(sql + " LIMIT 1", params).fetchone()
    if not row:
        raise WhatsAppError(f"Message with ID {message_id} not found")

    target = _row_to_message(row)
    raw_ts = row[8]

    before_rows = conn.execute(
        f"SELECT {_MESSAGE_COLUMNS} FROM messages JOIN chats ON messages.chat_jid = chats.jid "
        "WHERE messages.chat_jid = ? AND messages.timestamp < ? "
        "ORDER BY messages.timestamp DESC LIMIT ?",
        (target.chat_jid, raw_ts, before),
    ).fetchall()
    after_rows = conn.execute(
        f"SELECT {_MESSAGE_COLUMNS} FROM messages JOIN chats ON messages.chat_jid = chats.jid "
        "WHERE messages.chat_jid = ? AND messages.timestamp > ? "
        "ORDER BY messages.timestamp ASC LIMIT ?",
        (target.chat_jid, raw_ts, after),
    ).fetchall()

    return MessageContext(
        message=target,
        # Return "before" messages in chronological order.
        before=[_row_to_message(r) for r in reversed(before_rows)],
        after=[_row_to_message(r) for r in after_rows],
    )


def get_message_context(message_id: str, before: int = 5, after: int = 5) -> MessageContext:
    """Return a message together with the messages before and after it in the same chat."""
    with _connect() as conn:
        return _message_context(conn, message_id, _clamp(before, 0, MAX_CONTEXT), _clamp(after, 0, MAX_CONTEXT))


_CHAT_COLUMNS_WITH_LAST = (
    "chats.jid, chats.name, chats.last_message_time, messages.content, messages.sender, messages.is_from_me"
)
_CHAT_COLUMNS_NO_LAST = "chats.jid, chats.name, chats.last_message_time, NULL, NULL, NULL"
_LAST_MESSAGE_JOIN = (
    " LEFT JOIN messages ON messages.rowid = ("
    "SELECT m.rowid FROM messages m WHERE m.chat_jid = chats.jid "
    "ORDER BY m.timestamp DESC LIMIT 1)"
)


def list_chats(
    query: str | None = None,
    limit: int = 20,
    page: int = 0,
    include_last_message: bool = True,
    sort_by: str = "last_active",
) -> list[Chat]:
    """Return chats matching ``query`` (name or JID), sorted by activity or name."""
    if sort_by not in ("last_active", "name"):
        raise WhatsAppError("sort_by must be 'last_active' or 'name'")
    limit = _clamp(limit, 1, MAX_LIMIT)
    page = max(0, int(page))

    columns = _CHAT_COLUMNS_WITH_LAST if include_last_message else _CHAT_COLUMNS_NO_LAST
    sql = f"SELECT {columns} FROM chats"
    if include_last_message:
        sql += _LAST_MESSAGE_JOIN
    params: list[Any] = []
    if query:
        sql += " WHERE (LOWER(chats.name) LIKE LOWER(?) ESCAPE '\\' OR chats.jid LIKE ? ESCAPE '\\')"
        pattern = _like_pattern(query)
        params.extend([pattern, pattern])
    order = "chats.last_message_time DESC" if sort_by == "last_active" else "LOWER(chats.name) ASC"
    sql += f" ORDER BY {order} LIMIT ? OFFSET ?"
    params.extend([limit, page * limit])

    with _connect() as conn:
        return [_row_to_chat(row) for row in conn.execute(sql, params).fetchall()]


def search_contacts(query: str) -> list[Contact]:
    """Search individual (non-group) chats by name or phone number."""
    if not query or not query.strip():
        raise WhatsAppError("query must not be empty")
    pattern = _like_pattern(query.strip())
    with _connect() as conn:
        rows = conn.execute(
            """
            SELECT DISTINCT jid, name
            FROM chats
            WHERE (LOWER(name) LIKE LOWER(?) ESCAPE '\\' OR LOWER(jid) LIKE LOWER(?) ESCAPE '\\')
              AND jid NOT LIKE '%@g.us'
            ORDER BY name, jid
            LIMIT 50
            """,
            (pattern, pattern),
        ).fetchall()
    return [Contact(phone_number=jid.split("@", 1)[0], name=name, jid=jid) for jid, name in rows]


def get_contact_chats(jid: str, limit: int = 20, page: int = 0) -> list[Chat]:
    """Return chats (direct and group) in which the contact has participated."""
    limit = _clamp(limit, 1, MAX_LIMIT)
    page = max(0, int(page))
    sender = normalize_phone(jid)
    with _connect() as conn:
        rows = conn.execute(
            f"""
            SELECT {_CHAT_COLUMNS_WITH_LAST}
            FROM chats {_LAST_MESSAGE_JOIN}
            WHERE chats.jid = ?
               OR EXISTS (SELECT 1 FROM messages s WHERE s.chat_jid = chats.jid AND s.sender IN (?, ?))
            ORDER BY chats.last_message_time DESC
            LIMIT ? OFFSET ?
            """,
            (jid, jid, sender, limit, page * limit),
        ).fetchall()
    return [_row_to_chat(row) for row in rows]


def get_last_interaction(jid: str) -> str | None:
    """Return the most recent message sent by or to the contact, formatted as text."""
    sender = normalize_phone(jid)
    with _connect() as conn:
        row = conn.execute(
            f"""
            SELECT {_MESSAGE_COLUMNS}
            FROM messages JOIN chats ON messages.chat_jid = chats.jid
            WHERE messages.sender IN (?, ?) OR chats.jid = ?
            ORDER BY messages.timestamp DESC
            LIMIT 1
            """,
            (jid, sender, jid),
        ).fetchone()
        if not row:
            return None
        message = _row_to_message(row)
        name = None if message.is_from_me else _lookup_sender_name(conn, message.sender)
        return format_message(message, name)


def get_chat(chat_jid: str, include_last_message: bool = True) -> Chat | None:
    """Return chat metadata by JID, or None if the chat is unknown."""
    columns = _CHAT_COLUMNS_WITH_LAST if include_last_message else _CHAT_COLUMNS_NO_LAST
    sql = f"SELECT {columns} FROM chats"
    if include_last_message:
        sql += _LAST_MESSAGE_JOIN
    sql += " WHERE chats.jid = ?"
    with _connect() as conn:
        row = conn.execute(sql, (chat_jid.strip(),)).fetchone()
    return _row_to_chat(row) if row else None


def get_direct_chat_by_contact(sender_phone_number: str) -> Chat | None:
    """Return the direct (non-group) chat with a phone number, or None."""
    phone = normalize_phone(sender_phone_number)
    if not phone:
        raise WhatsAppError("sender_phone_number must not be empty")
    with _connect() as conn:
        row = conn.execute(
            f"""
            SELECT {_CHAT_COLUMNS_WITH_LAST}
            FROM chats {_LAST_MESSAGE_JOIN}
            WHERE chats.jid NOT LIKE '%@g.us'
              AND (chats.jid = ? OR chats.jid LIKE ? ESCAPE '\\')
            ORDER BY chats.jid = ? DESC, chats.last_message_time DESC
            LIMIT 1
            """,
            (f"{phone}@{USER_SERVER}", _like_pattern(phone), f"{phone}@{USER_SERVER}"),
        ).fetchone()
    return _row_to_chat(row) if row else None


# --------------------------------------------------------------------------- #
# Bridge REST API
# --------------------------------------------------------------------------- #


def _http_client(settings: Settings) -> httpx.Client:
    headers = {"X-API-Key": settings.api_key} if settings.api_key else {}
    return httpx.Client(base_url=settings.api_base_url, headers=headers, timeout=settings.request_timeout)


def _post(path: str, payload: dict[str, Any]) -> tuple[bool, dict[str, Any]]:
    """POST JSON to the bridge. Returns (success, decoded body)."""
    settings = _settings()
    try:
        with _http_client(settings) as client:
            response = client.post(path, json=payload)
    except httpx.TimeoutException:
        return False, {"message": f"Timed out after {settings.request_timeout:g}s waiting for the WhatsApp bridge"}
    except httpx.HTTPError as exc:
        return False, {
            "message": f"Could not reach the WhatsApp bridge at {settings.api_base_url} ({exc}). Is it running?"
        }

    try:
        body = response.json()
    except ValueError:
        body = {"message": response.text.strip() or f"HTTP {response.status_code}"}
    if not isinstance(body, dict):
        body = {"message": str(body)}

    if response.status_code == 401:
        body["message"] = "The WhatsApp bridge rejected the request: check WHATSAPP_BRIDGE_API_KEY"
    ok = response.is_success and bool(body.get("success", False))
    if not ok and "message" not in body:
        body["message"] = f"HTTP {response.status_code}"
    return ok, body


def _resolve_media_path(media_path: str) -> str:
    """Validate a local file path and make it absolute (the bridge may run in another directory)."""
    if not media_path or not media_path.strip():
        raise WhatsAppError("Media path must be provided")
    path = os.path.abspath(os.path.expanduser(media_path.strip()))
    if not os.path.isfile(path):
        raise WhatsAppError(f"Media file not found: {path}")
    return path


def send_message(recipient: str, message: str) -> tuple[bool, str]:
    """Send a text message. ``recipient`` is a phone number (with country code) or a JID."""
    if not recipient or not recipient.strip():
        return False, "Recipient must be provided"
    if not message:
        return False, "Message must not be empty"
    ok, body = _post("/send", {"recipient": recipient.strip(), "message": message})
    return ok, str(body.get("message", "Unknown response"))


def send_file(recipient: str, media_path: str, caption: str = "") -> tuple[bool, str]:
    """Send a local file (image, video, document or raw audio)."""
    if not recipient or not recipient.strip():
        return False, "Recipient must be provided"
    try:
        path = _resolve_media_path(media_path)
    except WhatsAppError as exc:
        return False, str(exc)
    ok, body = _post("/send", {"recipient": recipient.strip(), "message": caption, "media_path": path})
    return ok, str(body.get("message", "Unknown response"))


def send_audio_message(recipient: str, media_path: str) -> tuple[bool, str]:
    """Send an audio file as a voice note, converting it to Ogg Opus with ffmpeg if needed."""
    if not recipient or not recipient.strip():
        return False, "Recipient must be provided"
    try:
        path = _resolve_media_path(media_path)
    except WhatsAppError as exc:
        return False, str(exc)

    temp_path: str | None = None
    if not path.lower().endswith((".ogg", ".opus")):
        try:
            temp_path = audio.convert_to_opus_ogg_temp(path)
        except (audio.AudioConversionError, OSError) as exc:
            return False, f"Error converting file to Ogg Opus: {exc}"
        path = temp_path

    try:
        ok, body = _post("/send", {"recipient": recipient.strip(), "media_path": path})
    finally:
        if temp_path and os.path.exists(temp_path):
            os.unlink(temp_path)
    return ok, str(body.get("message", "Unknown response"))


def download_media(message_id: str, chat_jid: str) -> tuple[bool, str, str | None]:
    """Ask the bridge to download a message's media. Returns (success, message, local path)."""
    if not message_id or not chat_jid:
        return False, "message_id and chat_jid are required", None
    ok, body = _post("/download", {"message_id": message_id, "chat_jid": chat_jid})
    path = body.get("path") if ok else None
    return ok, str(body.get("message", "Unknown response")), path
