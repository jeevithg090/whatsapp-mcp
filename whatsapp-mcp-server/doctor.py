"""``main.py --check``: diagnose the MCP server setup without starting the server.

Only counts and connection states are printed, never message content or contacts.
"""

from __future__ import annotations

import sys
from dataclasses import dataclass
from typing import TextIO

import audio
import whatsapp
from config import Settings, load_settings


@dataclass
class Check:
    name: str
    ok: bool
    detail: str
    hint: str = ""
    required: bool = True


def _settings_check() -> tuple[Settings | None, Check]:
    try:
        settings = load_settings()
    except ValueError as exc:
        return None, Check("Configuration", False, str(exc), "Fix the environment variable and run --check again.")
    return settings, Check("Configuration", True, "environment variables are valid")


def _database_check(settings: Settings) -> Check:
    if not settings.db_path.is_file():
        return Check(
            "Message database",
            False,
            f"not found at {settings.db_path}",
            "Start the bridge once (cd whatsapp-bridge && go run .) or point WHATSAPP_DB_PATH at messages.db.",
        )
    try:
        stats = whatsapp.database_stats()
    except whatsapp.WhatsAppError as exc:
        return Check("Message database", False, str(exc), "Is this the bridge's messages.db?")
    detail = f"{settings.db_path} ({stats['chats']} chats, {stats['messages']} messages)"
    if stats["messages"] == 0:
        return Check(
            "Message database",
            True,
            detail,
            "No messages yet: pair the bridge and give history sync a few minutes.",
        )
    return Check("Message database", True, detail)


def _bridge_check(settings: Settings) -> Check:
    try:
        health = whatsapp.bridge_health()
    except whatsapp.WhatsAppError as exc:
        hint = "Start the bridge (cd whatsapp-bridge && go run .) and make WHATSAPP_API_BASE_URL match its --addr."
        if "API key" in str(exc):
            hint = "Use the same WHATSAPP_BRIDGE_API_KEY for the bridge and the MCP server."
        return Check("Bridge REST API", False, str(exc), hint)
    if "connected" not in health:
        return Check("Bridge REST API", True, f"reachable at {settings.api_base_url}")
    if not health.get("logged_in"):
        return Check(
            "Bridge REST API",
            False,
            f"reachable at {settings.api_base_url}, but not logged in to WhatsApp",
            "Look at the bridge terminal: scan the QR code shown there to link the device.",
        )
    if not health.get("connected"):
        return Check(
            "Bridge REST API",
            False,
            f"reachable at {settings.api_base_url}, but disconnected from WhatsApp",
            "The bridge reconnects automatically; check its terminal and your internet connection.",
        )
    return Check("Bridge REST API", True, f"reachable at {settings.api_base_url}, connected and logged in")


def _ffmpeg_check() -> Check:
    if audio.ffmpeg_available():
        return Check("FFmpeg", True, "found on PATH", required=False)
    return Check(
        "FFmpeg",
        False,
        "not found on PATH",
        "Optional: only needed to send non-.ogg audio with send_audio_message.",
        required=False,
    )


def run_checks() -> list[Check]:
    settings, config_check = _settings_check()
    checks = [config_check]
    if settings is not None:
        auth = "set" if settings.api_key else "not set"
        config_check.detail = f"bridge {settings.api_base_url}, API key {auth}, timeout {settings.request_timeout:g}s"
        checks += [_database_check(settings), _bridge_check(settings)]
    checks.append(_ffmpeg_check())
    return checks


def _mark(check: Check) -> str:
    if check.ok:
        return "[ ok ]"
    return "[FAIL]" if check.required else "[warn]"


def main(out: TextIO = sys.stdout) -> int:
    """Print a report and return the process exit code (0 when every required check passes)."""
    print("WhatsApp MCP server: setup check\n", file=out)
    checks = run_checks()
    for check in checks:
        print(f"{_mark(check)} {check.name}: {check.detail}", file=out)
        if check.hint:
            print(f"       -> {check.hint}", file=out)
    failed = [c for c in checks if c.required and not c.ok]
    print(file=out)
    if failed:
        print(f"{len(failed)} required check(s) failed. Fix them, then run --check again.", file=out)
        return 1
    print("All required checks passed. Your MCP client can start this server.", file=out)
    return 0
