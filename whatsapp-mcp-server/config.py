"""Runtime configuration for the WhatsApp MCP server, read from environment variables."""

from __future__ import annotations

import os
from dataclasses import dataclass
from pathlib import Path

_DEFAULT_DB_PATH = Path(__file__).resolve().parent.parent / "whatsapp-bridge" / "store" / "messages.db"
_DEFAULT_API_URL = "http://127.0.0.1:8080/api"
_DEFAULT_TIMEOUT = 60.0


@dataclass(frozen=True)
class Settings:
    """Settings shared by the database and bridge clients."""

    db_path: Path
    """Path to the bridge's ``messages.db`` SQLite database."""

    api_base_url: str
    """Base URL of the Go bridge REST API (including the ``/api`` suffix)."""

    api_key: str | None
    """Optional key sent to the bridge in the ``X-API-Key`` header."""

    request_timeout: float
    """Timeout in seconds for requests to the bridge (sending media can be slow)."""


def _env(name: str) -> str | None:
    value = os.environ.get(name, "").strip()
    return value or None


def load_settings() -> Settings:
    """Build :class:`Settings` from the environment, applying defaults."""
    timeout_raw = _env("WHATSAPP_REQUEST_TIMEOUT")
    try:
        timeout = float(timeout_raw) if timeout_raw else _DEFAULT_TIMEOUT
    except ValueError as exc:
        raise ValueError(f"WHATSAPP_REQUEST_TIMEOUT must be a number, got {timeout_raw!r}") from exc
    if timeout <= 0:
        raise ValueError("WHATSAPP_REQUEST_TIMEOUT must be positive")

    db_path = _env("WHATSAPP_DB_PATH")
    return Settings(
        db_path=Path(db_path).expanduser() if db_path else _DEFAULT_DB_PATH,
        api_base_url=(_env("WHATSAPP_API_BASE_URL") or _DEFAULT_API_URL).rstrip("/"),
        api_key=_env("WHATSAPP_BRIDGE_API_KEY"),
        request_timeout=timeout,
    )
