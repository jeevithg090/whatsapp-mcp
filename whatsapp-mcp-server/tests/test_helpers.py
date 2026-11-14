from __future__ import annotations

from datetime import UTC, datetime
from pathlib import Path

import pytest

import whatsapp
from config import load_settings


@pytest.mark.parametrize(
    ("raw", "expected"),
    [
        ("2025-05-01 06:30:00+00:00", datetime(2025, 5, 1, 6, 30, tzinfo=UTC)),
        ("2025-05-01 12:00:00+05:30", datetime(2025, 5, 1, 6, 30, tzinfo=UTC)),
        ("2025-05-01 06:30:00.123456789+00:00", datetime(2025, 5, 1, 6, 30, 0, 123456, tzinfo=UTC)),
        ("2025-05-01T06:30:00Z", datetime(2025, 5, 1, 6, 30, tzinfo=UTC)),
        ("2025-05-01 06:30:00", datetime(2025, 5, 1, 6, 30, tzinfo=UTC)),
    ],
)
def test_parse_db_timestamp(raw: str, expected: datetime) -> None:
    assert whatsapp.parse_db_timestamp(raw) == expected


def test_to_db_timestamp_normalises_to_utc() -> None:
    assert whatsapp.to_db_timestamp("2025-05-01T12:00:00+05:30", "after") == "2025-05-01 06:30:00+00:00"
    assert whatsapp.to_db_timestamp("2025-05-01", "after") == "2025-05-01 00:00:00+00:00"  # TZ=UTC in tests
    with pytest.raises(whatsapp.WhatsAppError):
        whatsapp.to_db_timestamp("31/12/2025", "before")


@pytest.mark.parametrize(
    ("raw", "expected"),
    [
        ("+44 7700 900123", "447700900123"),
        ("447700900123@s.whatsapp.net", "447700900123"),
        ("447700900123:12@s.whatsapp.net", "447700900123"),
        ("abc", "abc"),
    ],
)
def test_normalize_phone(raw: str, expected: str) -> None:
    assert whatsapp.normalize_phone(raw) == expected


def test_settings_defaults_and_overrides(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.delenv("WHATSAPP_DB_PATH")
    monkeypatch.delenv("WHATSAPP_API_BASE_URL")
    settings = load_settings()
    assert settings.db_path.parts[-3:] == ("whatsapp-bridge", "store", "messages.db")
    assert settings.api_base_url == "http://127.0.0.1:8080/api"
    assert settings.api_key is None
    assert settings.request_timeout == 60

    monkeypatch.setenv("WHATSAPP_DB_PATH", "~/wa/messages.db")
    monkeypatch.setenv("WHATSAPP_API_BASE_URL", "http://localhost:9000/api/")
    monkeypatch.setenv("WHATSAPP_BRIDGE_API_KEY", "k")
    monkeypatch.setenv("WHATSAPP_REQUEST_TIMEOUT", "5")
    settings = load_settings()
    assert settings.db_path == Path("~/wa/messages.db").expanduser()
    assert settings.api_base_url == "http://localhost:9000/api"
    assert settings.api_key == "k"
    assert settings.request_timeout == 5


@pytest.mark.parametrize("value", ["abc", "0", "-1"])
def test_invalid_timeout(monkeypatch: pytest.MonkeyPatch, value: str) -> None:
    monkeypatch.setenv("WHATSAPP_REQUEST_TIMEOUT", value)
    with pytest.raises(ValueError, match="WHATSAPP_REQUEST_TIMEOUT"):
        load_settings()
