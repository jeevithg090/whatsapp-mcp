"""WhatsApp MCP server: exposes your WhatsApp chats and messaging as MCP tools over stdio."""

from __future__ import annotations

import functools
import logging
import sys
from collections.abc import Callable
from typing import Any, ParamSpec, TypeVar

from mcp.server.fastmcp import FastMCP
from mcp.server.fastmcp.exceptions import ToolError

import whatsapp

# stdout carries the MCP JSON-RPC stream, so all logging must go to stderr.
logging.basicConfig(
    level=logging.INFO,
    stream=sys.stderr,
    format="%(asctime)s %(levelname)s %(name)s: %(message)s",
)

mcp = FastMCP("whatsapp")

P = ParamSpec("P")
R = TypeVar("R")


def _tool_errors(func: Callable[P, R]) -> Callable[P, R]:
    """Report expected WhatsApp errors to the client as tool errors."""

    @functools.wraps(func)
    def wrapper(*args: P.args, **kwargs: P.kwargs) -> R:
        try:
            return func(*args, **kwargs)
        except whatsapp.WhatsAppError as exc:
            raise ToolError(str(exc)) from exc

    return wrapper


def _result(success: bool, message: str, **extra: Any) -> dict[str, Any]:
    return {"success": success, "message": message, **extra}


@mcp.tool()
@_tool_errors
def search_contacts(query: str) -> list[dict[str, Any]]:
    """Search WhatsApp contacts by name or phone number.

    Args:
        query: Search term to match against contact names or phone numbers
    """
    return [c.to_dict() for c in whatsapp.search_contacts(query)]


@mcp.tool()
@_tool_errors
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
    """Get WhatsApp messages matching specified criteria with optional context.

    Args:
        after: Optional ISO-8601 date/time; only return messages after it (local time if no offset)
        before: Optional ISO-8601 date/time; only return messages before it (local time if no offset)
        sender_phone_number: Optional phone number to filter messages by sender
        chat_jid: Optional chat JID to filter messages by chat
        query: Optional search term to filter messages by content
        limit: Maximum number of messages to return (default 20, max 200)
        page: Page number for pagination (default 0)
        include_context: Whether to include messages before and after matches (default True)
        context_before: Number of messages to include before each match (default 1)
        context_after: Number of messages to include after each match (default 1)
    """
    return whatsapp.list_messages(
        after=after,
        before=before,
        sender_phone_number=sender_phone_number,
        chat_jid=chat_jid,
        query=query,
        limit=limit,
        page=page,
        include_context=include_context,
        context_before=context_before,
        context_after=context_after,
    )


@mcp.tool()
@_tool_errors
def list_chats(
    query: str | None = None,
    limit: int = 20,
    page: int = 0,
    include_last_message: bool = True,
    sort_by: str = "last_active",
) -> list[dict[str, Any]]:
    """Get WhatsApp chats matching specified criteria.

    Args:
        query: Optional search term to filter chats by name or JID
        limit: Maximum number of chats to return (default 20, max 200)
        page: Page number for pagination (default 0)
        include_last_message: Whether to include the last message in each chat (default True)
        sort_by: Field to sort results by, either "last_active" or "name" (default "last_active")
    """
    chats = whatsapp.list_chats(
        query=query,
        limit=limit,
        page=page,
        include_last_message=include_last_message,
        sort_by=sort_by,
    )
    return [c.to_dict() for c in chats]


@mcp.tool()
@_tool_errors
def get_chat(chat_jid: str, include_last_message: bool = True) -> dict[str, Any] | None:
    """Get WhatsApp chat metadata by JID.

    Args:
        chat_jid: The JID of the chat to retrieve
        include_last_message: Whether to include the last message (default True)
    """
    chat = whatsapp.get_chat(chat_jid, include_last_message)
    return chat.to_dict() if chat else None


@mcp.tool()
@_tool_errors
def get_direct_chat_by_contact(sender_phone_number: str) -> dict[str, Any] | None:
    """Get WhatsApp chat metadata by sender phone number.

    Args:
        sender_phone_number: The phone number to search for
    """
    chat = whatsapp.get_direct_chat_by_contact(sender_phone_number)
    return chat.to_dict() if chat else None


@mcp.tool()
@_tool_errors
def get_contact_chats(jid: str, limit: int = 20, page: int = 0) -> list[dict[str, Any]]:
    """Get all WhatsApp chats involving the contact.

    Args:
        jid: The contact's JID to search for
        limit: Maximum number of chats to return (default 20, max 200)
        page: Page number for pagination (default 0)
    """
    return [c.to_dict() for c in whatsapp.get_contact_chats(jid, limit, page)]


@mcp.tool()
@_tool_errors
def get_last_interaction(jid: str) -> str:
    """Get most recent WhatsApp message involving the contact.

    Args:
        jid: The JID of the contact to search for
    """
    return whatsapp.get_last_interaction(jid) or f"No messages found for {jid}."


@mcp.tool()
@_tool_errors
def get_message_context(message_id: str, before: int = 5, after: int = 5) -> dict[str, Any]:
    """Get context around a specific WhatsApp message.

    Args:
        message_id: The ID of the message to get context for
        before: Number of messages to include before the target message (default 5)
        after: Number of messages to include after the target message (default 5)
    """
    return whatsapp.get_message_context(message_id, before, after).to_dict()


@mcp.tool()
def send_message(recipient: str, message: str) -> dict[str, Any]:
    """Send a WhatsApp message to a person or group. For group chats use the JID.

    Args:
        recipient: The recipient - either a phone number with country code but no + or other symbols,
                 or a JID (e.g., "123456789@s.whatsapp.net" or a group JID like "123456789@g.us")
        message: The message text to send

    Returns:
        A dictionary containing success status and a status message
    """
    return _result(*whatsapp.send_message(recipient, message))


@mcp.tool()
def send_file(recipient: str, media_path: str, caption: str = "") -> dict[str, Any]:
    """Send a file such as a picture, raw audio, video or document via WhatsApp. For group messages use the JID.

    Args:
        recipient: The recipient - either a phone number with country code but no + or other symbols,
                 or a JID (e.g., "123456789@s.whatsapp.net" or a group JID like "123456789@g.us")
        media_path: The absolute path to the media file to send (image, video, document)
        caption: Optional caption to send with the file

    Returns:
        A dictionary containing success status and a status message
    """
    return _result(*whatsapp.send_file(recipient, media_path, caption))


@mcp.tool()
def send_audio_message(recipient: str, media_path: str) -> dict[str, Any]:
    """Send any audio file as a WhatsApp voice message. For group messages use the JID.

    If it errors because ffmpeg is not installed, use send_file instead.

    Args:
        recipient: The recipient - either a phone number with country code but no + or other symbols,
                 or a JID (e.g., "123456789@s.whatsapp.net" or a group JID like "123456789@g.us")
        media_path: The absolute path to the audio file (converted to Opus .ogg if it is not already)

    Returns:
        A dictionary containing success status and a status message
    """
    return _result(*whatsapp.send_audio_message(recipient, media_path))


@mcp.tool()
def download_media(message_id: str, chat_jid: str) -> dict[str, Any]:
    """Download media from a WhatsApp message and get the local file path.

    Args:
        message_id: The ID of the message containing the media
        chat_jid: The JID of the chat containing the message

    Returns:
        A dictionary containing success status, a status message, and the file path if successful
    """
    success, message, path = whatsapp.download_media(message_id, chat_jid)
    if success:
        return _result(True, message, file_path=path)
    return _result(False, message)


def main() -> None:
    """Run the MCP server over stdio."""
    mcp.run(transport="stdio")


if __name__ == "__main__":
    main()
