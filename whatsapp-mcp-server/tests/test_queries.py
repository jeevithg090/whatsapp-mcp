from __future__ import annotations

from datetime import UTC, datetime
from pathlib import Path

import pytest

import whatsapp
from tests.conftest import ALICE, BOB, GROUP


def test_missing_database_gives_clear_error() -> None:
    with pytest.raises(whatsapp.WhatsAppError, match="Start the WhatsApp bridge"):
        whatsapp.list_chats()


def test_list_chats_sorted_by_activity_with_last_message(db: Path) -> None:
    chats = whatsapp.list_chats()
    assert [c.jid for c in chats] == [GROUP, ALICE, BOB]
    assert chats[0].last_message == "Loved it_already"
    assert chats[0].is_group
    assert not chats[1].is_group


def test_list_chats_without_last_message(db: Path) -> None:
    chats = whatsapp.list_chats(include_last_message=False)
    assert len(chats) == 3
    assert all(c.last_message is None for c in chats)


def test_list_chats_sort_by_name_and_query(db: Path) -> None:
    assert [c.name for c in whatsapp.list_chats(sort_by="name")] == ["Alice", "Bob", "Book Club"]
    assert [c.jid for c in whatsapp.list_chats(query="bo")] == [GROUP, BOB]
    with pytest.raises(whatsapp.WhatsAppError):
        whatsapp.list_chats(sort_by="jid; DROP TABLE chats")


def test_list_chats_pagination(db: Path) -> None:
    first = whatsapp.list_chats(limit=2, page=0)
    second = whatsapp.list_chats(limit=2, page=1)
    assert len(first) == 2
    assert len(second) == 1
    assert {c.jid for c in first}.isdisjoint({c.jid for c in second})


def test_search_contacts_excludes_groups(db: Path) -> None:
    contacts = whatsapp.search_contacts("o")
    assert [c.jid for c in contacts] == [BOB]
    assert contacts[0].phone_number == "447700900002"
    assert [c.name for c in whatsapp.search_contacts("900001")] == ["Alice"]
    with pytest.raises(whatsapp.WhatsAppError):
        whatsapp.search_contacts("  ")


def test_like_wildcards_are_escaped(db: Path) -> None:
    assert "100% there" in whatsapp.list_messages(query="100%", include_context=False)
    # "_" must match literally, not "any character".
    assert "Loved it_already" in whatsapp.list_messages(query="t_a", include_context=False)
    assert whatsapp.list_messages(query="i_s", include_context=False) == "No messages to display."


def test_list_messages_formats_and_resolves_names(db: Path) -> None:
    out = whatsapp.list_messages(chat_jid=ALICE, include_context=False)
    lines = out.splitlines()
    assert len(lines) == 4
    assert lines[0].startswith("[2025-05-01 10:05:00] Chat: Alice From: Alice: [image - Message ID: a4")
    assert "From: Me: Yes, 1pm" in lines[2]


def test_list_messages_date_filters(db: Path) -> None:
    out = whatsapp.list_messages(
        after="2025-05-01T10:00:30Z", before="2025-05-01T10:03:00+00:00", include_context=False
    )
    assert out.splitlines() == [
        "[2025-05-01 10:02:00] Chat: Alice From: Alice: Great, 100% there",
        "[2025-05-01 10:01:00] Chat: Alice From: Me: Yes, 1pm at the usual place",
    ]
    with pytest.raises(whatsapp.WhatsAppError, match="Invalid date"):
        whatsapp.list_messages(after="yesterday")


def test_list_messages_sender_filter_accepts_formatted_numbers(db: Path) -> None:
    out = whatsapp.list_messages(sender_phone_number="+44 7700 900002", include_context=False)
    assert "Next book: Dune" in out
    assert "Invoice attached" in out
    assert "Alice" not in out


def test_list_messages_with_context_deduplicates(db: Path) -> None:
    out = whatsapp.list_messages(chat_jid=ALICE, include_context=True, context_before=1, context_after=1)
    lines = out.splitlines()
    assert len(lines) == len(set(lines)) == 4


def test_list_messages_limit_is_clamped(db: Path) -> None:
    assert len(whatsapp.list_messages(limit=0, include_context=False).splitlines()) == 1
    assert len(whatsapp.list_messages(limit=10_000, include_context=False).splitlines()) == 7


def test_get_message_context(db: Path) -> None:
    ctx = whatsapp.get_message_context("a2", before=5, after=1)
    assert [m.id for m in ctx.before] == ["a1"]
    assert ctx.message.id == "a2"
    assert ctx.message.is_from_me is True
    assert [m.id for m in ctx.after] == ["a3"]
    data = ctx.to_dict()
    assert data["message"]["timestamp"] == "2025-05-01T10:01:00+00:00"
    with pytest.raises(whatsapp.WhatsAppError, match="not found"):
        whatsapp.get_message_context("nope")


def test_get_chat(db: Path) -> None:
    chat = whatsapp.get_chat(ALICE)
    assert chat is not None
    assert chat.name == "Alice"
    assert chat.last_message == ""  # the last message is an image without caption
    assert chat.last_message_time == datetime(2025, 5, 1, 10, 5, tzinfo=UTC)
    no_last = whatsapp.get_chat(ALICE, include_last_message=False)
    assert no_last is not None
    assert no_last.last_message is None
    assert whatsapp.get_chat("unknown@s.whatsapp.net") is None


def test_get_direct_chat_by_contact(db: Path) -> None:
    chat = whatsapp.get_direct_chat_by_contact("+447700900002")
    assert chat is not None
    assert chat.jid == BOB
    assert whatsapp.get_direct_chat_by_contact("999999") is None


def test_get_contact_chats_includes_groups(db: Path) -> None:
    jids = [c.jid for c in whatsapp.get_contact_chats(BOB)]
    assert jids == [GROUP, BOB]


def test_get_last_interaction(db: Path) -> None:
    out = whatsapp.get_last_interaction(ALICE)
    assert out is not None
    assert out.startswith("[2025-05-02 19:30:00] Chat: Book Club From: Alice: Loved it_already")
    assert whatsapp.get_last_interaction("000@s.whatsapp.net") is None


def test_sender_name_lookup(db: Path) -> None:
    assert whatsapp.get_sender_name("447700900001") == "Alice"
    assert whatsapp.get_sender_name(BOB) == "Bob"
    assert whatsapp.get_sender_name("123") == "123"
