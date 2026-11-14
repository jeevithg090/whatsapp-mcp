"""Audio helpers: convert arbitrary audio files to Ogg Opus voice notes with ffmpeg."""

from __future__ import annotations

import os
import shutil
import subprocess
import tempfile


class AudioConversionError(RuntimeError):
    """Raised when an audio file cannot be converted to Ogg Opus."""


def ffmpeg_available() -> bool:
    """Return True when an ``ffmpeg`` executable is on the PATH."""
    return shutil.which("ffmpeg") is not None


def convert_to_opus_ogg(
    input_file: str,
    output_file: str | None = None,
    bitrate: str = "32k",
    sample_rate: int = 24000,
) -> str:
    """Convert an audio file to Opus in an Ogg container (WhatsApp voice note format).

    Args:
        input_file: Path to the input audio file.
        output_file: Where to write the result. Defaults to ``input_file`` with an ``.ogg`` extension.
        bitrate: Target Opus bitrate.
        sample_rate: Output sample rate in Hz.

    Returns:
        The path of the converted file.

    Raises:
        FileNotFoundError: If ``input_file`` does not exist.
        AudioConversionError: If ffmpeg is missing or the conversion fails.
    """
    if not os.path.isfile(input_file):
        raise FileNotFoundError(f"Input file not found: {input_file}")
    if not ffmpeg_available():
        raise AudioConversionError(
            "ffmpeg is not installed or not on PATH; install it or send the file with send_file instead"
        )

    if output_file is None:
        output_file = os.path.splitext(input_file)[0] + ".ogg"

    output_dir = os.path.dirname(output_file)
    if output_dir:
        os.makedirs(output_dir, exist_ok=True)

    cmd = [
        "ffmpeg",
        "-nostdin",
        "-hide_banner",
        "-loglevel",
        "error",
        "-i",
        input_file,
        "-vn",  # drop any video/cover-art stream
        "-c:a",
        "libopus",
        "-b:a",
        bitrate,
        "-ar",
        str(sample_rate),
        "-application",
        "voip",
        "-vbr",
        "on",
        "-compression_level",
        "10",
        "-frame_duration",
        "60",
        "-y",
        output_file,
    ]

    try:
        subprocess.run(cmd, capture_output=True, text=True, check=True)
    except subprocess.CalledProcessError as exc:
        detail = (exc.stderr or "").strip().splitlines()
        raise AudioConversionError(f"ffmpeg failed to convert {input_file}: {detail[-1] if detail else exc}") from exc
    return output_file


def convert_to_opus_ogg_temp(input_file: str, bitrate: str = "32k", sample_rate: int = 24000) -> str:
    """Convert an audio file to Ogg Opus in a new temporary file and return its path.

    The caller is responsible for deleting the returned file.
    """
    fd, temp_path = tempfile.mkstemp(suffix=".ogg")
    os.close(fd)
    try:
        return convert_to_opus_ogg(input_file, temp_path, bitrate, sample_rate)
    except BaseException:
        if os.path.exists(temp_path):
            os.unlink(temp_path)
        raise
