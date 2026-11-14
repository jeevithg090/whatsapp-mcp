"""End-to-end tests of the MCP tool layer, including a real stdio session."""

from __future__ import annotations

import json
import os
import sys
from pathlib import Path

import anyio
import pytest
from mcp import ClientSession, StdioServerParameters
from mcp.client.stdio import stdio_client
from mcp.server.fastmcp.exceptions import ToolError

import main
from tests.conftest import ALICE, GROUP

SERVER_DIR = Path(__file__).resolve().parent.parent

EXPECTED_TOOLS = {
    "search_contacts",
    "list_messages",
    "list_chats",
    "get_chat",
    "get_direct_chat_by_contact",
    "get_contact_chats",
    "get_last_interaction",
    "get_message_context",
    "send_message",
    "send_file",
    "send_audio_message",
    "download_media",
}


def call(name: str, args: dict[str, object]) -> object:
    async def run() -> object:
        _content, structured = await main.mcp.call_tool(name, args)
        return structured

    return anyio.run(run)


def test_all_tools_registered() -> None:
    tools = anyio.run(main.mcp.list_tools)
    assert {t.name for t in tools} == EXPECTED_TOOLS


def test_list_chats_tool_returns_json_serialisable_dicts(db: Path) -> None:
    result = call("list_chats", {"limit": 2})
    assert isinstance(result, dict)
    chats = result["result"]
    assert [c["jid"] for c in chats] == [GROUP, ALICE]
    json.dumps(chats)


def test_get_chat_tool_unknown_returns_none(db: Path) -> None:
    assert call("get_chat", {"chat_jid": "nobody@s.whatsapp.net"}) == {"result": None}


def test_list_messages_tool_returns_text(db: Path) -> None:
    result = call("list_messages", {"chat_jid": ALICE, "include_context": False, "limit": 1})
    assert isinstance(result, dict)
    assert result["result"].startswith("[2025-05-01 10:05:00]")


def test_message_context_tool(db: Path) -> None:
    result = call("get_message_context", {"message_id": "a2", "before": 1, "after": 1})
    assert isinstance(result, dict)
    assert result["message"]["id"] == "a2"
    assert [m["id"] for m in result["before"]] == ["a1"]


def test_errors_become_tool_errors(db: Path) -> None:
    with pytest.raises(ToolError, match="not found"):
        call("get_message_context", {"message_id": "missing"})


def test_missing_db_is_a_tool_error() -> None:
    with pytest.raises(ToolError, match="Start the WhatsApp bridge"):
        call("list_chats", {})


def test_last_interaction_tool_when_empty(db: Path) -> None:
    assert call("get_last_interaction", {"jid": "0@s.whatsapp.net"}) == {
        "result": "No messages found for 0@s.whatsapp.net."
    }


def test_send_message_tool_validation() -> None:
    assert call("send_message", {"recipient": "", "message": "hi"}) == {
        "success": False,
        "message": "Recipient must be provided",
    }


def test_stdio_session(db: Path) -> None:
    """Start the real server over stdio: stdout must contain nothing but valid MCP JSON-RPC."""

    async def run() -> None:
        with anyio.fail_after(60):
            await session_checks()

    async def session_checks() -> None:
        params = StdioServerParameters(
            command=sys.executable,
            args=[str(SERVER_DIR / "main.py")],
            env={**os.environ, "WHATSAPP_DB_PATH": str(db)},
            cwd=str(SERVER_DIR),
        )
        async with stdio_client(params) as (read, write), ClientSession(read, write) as session:
            await session.initialize()
            tools = await session.list_tools()
            assert {t.name for t in tools.tools} == EXPECTED_TOOLS
            result = await session.call_tool("search_contacts", {"query": "alice"})
            assert not result.isError
            assert result.structuredContent == {
                "result": [{"phone_number": "447700900001", "name": "Alice", "jid": ALICE}]
            }

    anyio.run(run)
