"""Shared fixtures: a temporary messages.db with the same schema the Go bridge creates."""

from __future__ import annotations

import sqlite3
import time
from collections.abc import Iterator
from pathlib import Path

import pytest

# Keep in sync with the schema in whatsapp-bridge/store.go.
SCHEMA = """
CREATE TABLE chats (jid TEXT PRIMARY KEY, name TEXT, last_message_time TIMESTAMP);
CREATE TABLE messages (
    id TEXT, chat_jid TEXT, sender TEXT, content TEXT, timestamp TIMESTAMP, is_from_me BOOLEAN,
    media_type TEXT, filename TEXT, url TEXT, media_key BLOB, file_sha256 BLOB, file_enc_sha256 BLOB,
    file_length INTEGER, PRIMARY KEY (id, chat_jid), FOREIGN KEY (chat_jid) REFERENCES chats(jid)
);
"""

ALICE = "447700900001@s.whatsapp.net"
BOB = "447700900002@s.whatsapp.net"
GROUP = "120363000000000001@g.us"

CHATS = [
    (ALICE, "Alice", "2025-05-01 10:05:00+00:00"),
    (BOB, "Bob", "2025-04-30 08:00:00+00:00"),
    (GROUP, "Book Club", "2025-05-02 19:30:00+00:00"),
]

# id, chat_jid, sender, content, timestamp, is_from_me, media_type
MESSAGES = [
    ("a1", ALICE, "447700900001", "Hi! Are we still on for lunch?", "2025-05-01 10:00:00+00:00", 0, None),
    ("a2", ALICE, "447700900999", "Yes, 1pm at the usual place", "2025-05-01 10:01:00+00:00", 1, None),
    ("a3", ALICE, "447700900001", "Great, 100% there", "2025-05-01 10:02:00+00:00", 0, None),
    ("a4", ALICE, "447700900001", "", "2025-05-01 10:05:00+00:00", 0, "image"),
    ("b1", BOB, "447700900002", "Invoice attached", "2025-04-30 08:00:00.123456789+00:00", 0, "document"),
    ("g1", GROUP, "447700900002", "Next book: Dune", "2025-05-02 19:00:00+00:00", 0, None),
    ("g2", GROUP, "447700900001", "Loved it_already", "2025-05-02 19:30:00+00:00", 0, None),
]


def create_db(path: Path) -> None:
    conn = sqlite3.connect(path)
    try:
        conn.executescript(SCHEMA)
        conn.executemany("INSERT INTO chats VALUES (?, ?, ?)", CHATS)
        conn.executemany(
            "INSERT INTO messages (id, chat_jid, sender, content, timestamp, is_from_me, media_type)"
            " VALUES (?, ?, ?, ?, ?, ?, ?)",
            MESSAGES,
        )
        conn.commit()
    finally:
        conn.close()


@pytest.fixture(autouse=True)
def _isolated_env(monkeypatch: pytest.MonkeyPatch, tmp_path: Path) -> None:
    """Never touch a real database or bridge from tests, and use UTC for stable output."""
    monkeypatch.setenv("WHATSAPP_DB_PATH", str(tmp_path / "missing.db"))
    monkeypatch.setenv("WHATSAPP_API_BASE_URL", "http://bridge.test/api")
    monkeypatch.delenv("WHATSAPP_BRIDGE_API_KEY", raising=False)
    monkeypatch.delenv("WHATSAPP_REQUEST_TIMEOUT", raising=False)
    monkeypatch.setenv("TZ", "UTC")
    time.tzset()


@pytest.fixture
def db(tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> Iterator[Path]:
    path = tmp_path / "messages.db"
    create_db(path)
    monkeypatch.setenv("WHATSAPP_DB_PATH", str(path))
    yield path
