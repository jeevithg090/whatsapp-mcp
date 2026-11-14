"""Tests for the command line (--help, --version, --check) and the tool schemas clients display."""

from __future__ import annotations

import io
import subprocess
import sys
from pathlib import Path
from typing import Any

import anyio
import httpx
import pytest
from mcp.server.fastmcp.exceptions import ToolError

import doctor
import main
import whatsapp
from config import Settings

SERVER_DIR = Path(__file__).resolve().parent.parent
SEND_TOOLS = {"send_message", "send_file", "send_audio_message"}


def mock_bridge(monkeypatch: pytest.MonkeyPatch, status: int, body: Any = None) -> None:
    def factory(settings: Settings) -> httpx.Client:
        transport = httpx.MockTransport(lambda _req: httpx.Response(status, json=body))
        return httpx.Client(base_url=settings.api_base_url, transport=transport)

    monkeypatch.setattr(whatsapp, "_http_client", factory)


def unreachable_bridge(monkeypatch: pytest.MonkeyPatch) -> None:
    def refuse(request: httpx.Request) -> httpx.Response:
        raise httpx.ConnectError("connection refused", request=request)

    def factory(settings: Settings) -> httpx.Client:
        return httpx.Client(base_url=settings.api_base_url, transport=httpx.MockTransport(refuse))

    monkeypatch.setattr(whatsapp, "_http_client", factory)


def run_check() -> tuple[int, str]:
    out = io.StringIO()
    code = doctor.main(out)
    return code, out.getvalue()


def test_help_and_version_run_without_a_bridge() -> None:
    result = subprocess.run(
        [sys.executable, "main.py", "--help"], cwd=SERVER_DIR, capture_output=True, text=True, check=True
    )
    assert "--check" in result.stdout
    assert "WHATSAPP_DB_PATH" in result.stdout
    version = subprocess.run(
        [sys.executable, "main.py", "--version"], cwd=SERVER_DIR, capture_output=True, text=True, check=True
    )
    assert version.stdout.strip() == f"whatsapp-mcp-server {main.__version__}"


def test_check_passes_when_everything_is_ready(db: Path, monkeypatch: pytest.MonkeyPatch) -> None:
    mock_bridge(monkeypatch, 200, {"status": "ok", "connected": True, "logged_in": True})
    code, out = run_check()
    assert code == 0, out
    assert "3 chats, 7 messages" in out
    assert "connected and logged in" in out
    # The report must never leak message content or contact names.
    assert "Alice" not in out
    assert "lunch" not in out


def test_check_fails_without_database_or_bridge(monkeypatch: pytest.MonkeyPatch) -> None:
    unreachable_bridge(monkeypatch)
    code, out = run_check()
    assert code == 1
    assert "[FAIL] Message database: not found" in out
    assert "[FAIL] Bridge REST API: not reachable" in out
    assert "2 required check(s) failed" in out


def test_check_reports_unpaired_bridge(db: Path, monkeypatch: pytest.MonkeyPatch) -> None:
    mock_bridge(monkeypatch, 200, {"status": "ok", "connected": True, "logged_in": False})
    code, out = run_check()
    assert code == 1
    assert "not logged in to WhatsApp" in out
    assert "scan the QR code" in out


def test_check_reports_wrong_api_key(db: Path, monkeypatch: pytest.MonkeyPatch) -> None:
    mock_bridge(monkeypatch, 401, {"success": False, "message": "unauthorized"})
    code, out = run_check()
    assert code == 1
    assert "WHATSAPP_BRIDGE_API_KEY" in out


def test_check_reports_invalid_configuration(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setenv("WHATSAPP_REQUEST_TIMEOUT", "soon")
    code, out = run_check()
    assert code == 1
    assert "WHATSAPP_REQUEST_TIMEOUT must be a number" in out


def test_every_tool_parameter_is_described() -> None:
    tools = anyio.run(main.mcp.list_tools)
    for tool in tools:
        assert tool.title, f"{tool.name} has no title"
        assert tool.description, f"{tool.name} has no description"
        assert tool.annotations is not None, f"{tool.name} has no annotations"
        for name, prop in tool.inputSchema["properties"].items():
            assert prop.get("description"), f"{tool.name}.{name} has no description"


def test_tool_annotations_separate_reads_from_sends() -> None:
    tools = {t.name: t for t in anyio.run(main.mcp.list_tools)}
    for name, tool in tools.items():
        assert tool.annotations is not None
        if name in SEND_TOOLS:
            assert tool.annotations.readOnlyHint is False
            assert tool.annotations.openWorldHint is True
        elif name != "download_media":
            assert tool.annotations.readOnlyHint is True


def test_schema_bounds_are_enforced(db: Path) -> None:
    async def call() -> None:
        await main.mcp.call_tool("list_chats", {"limit": 0})

    with pytest.raises(ToolError, match="limit"):
        anyio.run(call)


def test_sort_by_is_an_enum() -> None:
    tools = {t.name: t for t in anyio.run(main.mcp.list_tools)}
    assert tools["list_chats"].inputSchema["properties"]["sort_by"]["enum"] == ["last_active", "name"]
