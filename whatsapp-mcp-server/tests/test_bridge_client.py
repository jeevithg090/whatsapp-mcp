"""Tests for the REST client that talks to the Go bridge (HTTP is mocked)."""

from __future__ import annotations

import json
import os
import subprocess
from collections.abc import Callable
from pathlib import Path
from typing import Any

import httpx
import pytest

import audio
import whatsapp
from config import Settings

Handler = Callable[[httpx.Request], httpx.Response]


@pytest.fixture
def bridge(monkeypatch: pytest.MonkeyPatch) -> Callable[[Handler], list[httpx.Request]]:
    """Route bridge HTTP calls to a handler and record the requests."""

    def install(handler: Handler) -> list[httpx.Request]:
        seen: list[httpx.Request] = []

        def record(request: httpx.Request) -> httpx.Response:
            seen.append(request)
            return handler(request)

        def factory(settings: Settings) -> httpx.Client:
            headers = {"X-API-Key": settings.api_key} if settings.api_key else {}
            return httpx.Client(base_url=settings.api_base_url, headers=headers, transport=httpx.MockTransport(record))

        monkeypatch.setattr(whatsapp, "_http_client", factory)
        return seen

    return install


def ok(body: dict[str, Any]) -> Handler:
    return lambda _req: httpx.Response(200, json=body)


def test_send_message_success(
    bridge: Callable[[Handler], list[httpx.Request]], monkeypatch: pytest.MonkeyPatch
) -> None:
    monkeypatch.setenv("WHATSAPP_BRIDGE_API_KEY", "secret")
    seen = bridge(ok({"success": True, "message": "Message sent to 123"}))
    assert whatsapp.send_message(" 123 ", "hello") == (True, "Message sent to 123")
    assert seen[0].url == "http://bridge.test/api/send"
    assert seen[0].headers["X-API-Key"] == "secret"
    assert json.loads(seen[0].content) == {"recipient": "123", "message": "hello"}


def test_send_message_validation(bridge: Callable[[Handler], list[httpx.Request]]) -> None:
    seen = bridge(ok({"success": True}))
    assert whatsapp.send_message("", "hi") == (False, "Recipient must be provided")
    assert whatsapp.send_message("123", "")[0] is False
    assert seen == []


def test_send_message_bridge_error(bridge: Callable[[Handler], list[httpx.Request]]) -> None:
    bridge(lambda _r: httpx.Response(500, json={"success": False, "message": "not connected to WhatsApp"}))
    assert whatsapp.send_message("123", "hi") == (False, "not connected to WhatsApp")


def test_send_message_non_json_error(bridge: Callable[[Handler], list[httpx.Request]]) -> None:
    bridge(lambda _r: httpx.Response(502, text="Bad Gateway"))
    assert whatsapp.send_message("123", "hi") == (False, "Bad Gateway")


def test_send_message_unauthorized(bridge: Callable[[Handler], list[httpx.Request]]) -> None:
    bridge(lambda _r: httpx.Response(401, json={"success": False, "message": "unauthorized"}))
    ok_, msg = whatsapp.send_message("123", "hi")
    assert not ok_
    assert "WHATSAPP_BRIDGE_API_KEY" in msg


def test_bridge_unreachable(bridge: Callable[[Handler], list[httpx.Request]]) -> None:
    def fail(request: httpx.Request) -> httpx.Response:
        raise httpx.ConnectError("connection refused", request=request)

    bridge(fail)
    ok_, msg = whatsapp.send_message("123", "hi")
    assert not ok_
    assert "Is it running?" in msg


def test_bridge_timeout(bridge: Callable[[Handler], list[httpx.Request]]) -> None:
    def slow(request: httpx.Request) -> httpx.Response:
        raise httpx.ReadTimeout("slow", request=request)

    bridge(slow)
    ok_, msg = whatsapp.send_message("123", "hi")
    assert not ok_
    assert "Timed out" in msg


def test_send_file_sends_absolute_path(
    bridge: Callable[[Handler], list[httpx.Request]], tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    (tmp_path / "photo.jpg").write_bytes(b"\xff\xd8")
    monkeypatch.chdir(tmp_path)
    seen = bridge(ok({"success": True, "message": "sent"}))
    assert whatsapp.send_file("123", "photo.jpg", caption="look") == (True, "sent")
    payload = json.loads(seen[0].content)
    assert payload["media_path"] == str(tmp_path / "photo.jpg")
    assert payload["message"] == "look"


def test_send_file_missing(bridge: Callable[[Handler], list[httpx.Request]], tmp_path: Path) -> None:
    seen = bridge(ok({"success": True}))
    ok_, msg = whatsapp.send_file("123", str(tmp_path / "nope.pdf"))
    assert not ok_
    assert "not found" in msg
    assert seen == []


def test_send_audio_ogg_is_sent_as_is(bridge: Callable[[Handler], list[httpx.Request]], tmp_path: Path) -> None:
    voice = tmp_path / "voice.OGG"
    voice.write_bytes(b"OggS")
    seen = bridge(ok({"success": True, "message": "sent"}))
    assert whatsapp.send_audio_message("123", str(voice)) == (True, "sent")
    assert json.loads(seen[0].content)["media_path"] == str(voice)


def test_send_audio_converts_and_cleans_up(
    bridge: Callable[[Handler], list[httpx.Request]], tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    src = tmp_path / "note.mp3"
    src.write_bytes(b"ID3")
    converted = tmp_path / "converted.ogg"

    def fake_convert(path: str) -> str:
        assert path == str(src)
        converted.write_bytes(b"OggS")
        return str(converted)

    monkeypatch.setattr(audio, "convert_to_opus_ogg_temp", fake_convert)
    seen = bridge(ok({"success": True, "message": "sent"}))
    assert whatsapp.send_audio_message("123", str(src)) == (True, "sent")
    assert json.loads(seen[0].content)["media_path"] == str(converted)
    assert not converted.exists(), "temporary converted file should be removed"


def test_send_audio_conversion_failure(
    bridge: Callable[[Handler], list[httpx.Request]], tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    src = tmp_path / "note.wav"
    src.write_bytes(b"RIFF")

    def boom(_path: str) -> str:
        raise audio.AudioConversionError("ffmpeg is not installed")

    monkeypatch.setattr(audio, "convert_to_opus_ogg_temp", boom)
    seen = bridge(ok({"success": True}))
    ok_, msg = whatsapp.send_audio_message("123", str(src))
    assert not ok_
    assert "ffmpeg is not installed" in msg
    assert seen == []


def test_download_media(bridge: Callable[[Handler], list[httpx.Request]]) -> None:
    seen = bridge(ok({"success": True, "message": "Successfully downloaded image media", "path": "/store/x.jpg"}))
    assert whatsapp.download_media("a4", "123@s.whatsapp.net") == (
        True,
        "Successfully downloaded image media",
        "/store/x.jpg",
    )
    assert json.loads(seen[0].content) == {"message_id": "a4", "chat_jid": "123@s.whatsapp.net"}


def test_download_media_failure(bridge: Callable[[Handler], list[httpx.Request]]) -> None:
    bridge(lambda _r: httpx.Response(404, json={"success": False, "message": "Failed: not a media message"}))
    assert whatsapp.download_media("a1", "x") == (False, "Failed: not a media message", None)
    assert whatsapp.download_media("", "x")[0] is False


@pytest.mark.skipif(not audio.ffmpeg_available(), reason="ffmpeg not installed")
def test_real_audio_conversion(tmp_path: Path) -> None:
    wav = tmp_path / "tone.wav"
    subprocess.run(
        ["ffmpeg", "-loglevel", "error", "-f", "lavfi", "-i", "sine=frequency=440:duration=1", str(wav)],
        check=True,
    )
    out = audio.convert_to_opus_ogg_temp(str(wav))
    try:
        with open(out, "rb") as fh:
            assert fh.read(4) == b"OggS"
    finally:
        os.unlink(out)


def test_audio_conversion_missing_input(tmp_path: Path) -> None:
    with pytest.raises(FileNotFoundError):
        audio.convert_to_opus_ogg(str(tmp_path / "missing.mp3"))


def test_audio_conversion_without_ffmpeg(tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> None:
    src = tmp_path / "a.mp3"
    src.write_bytes(b"x")
    monkeypatch.setattr(audio.shutil, "which", lambda _name: None)
    with pytest.raises(audio.AudioConversionError, match="ffmpeg is not installed"):
        audio.convert_to_opus_ogg(str(src))
