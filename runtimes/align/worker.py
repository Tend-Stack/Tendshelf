# Tendshelf alignment worker: wav2vec2 CTC log-probabilities over stdin/stdout pipes.
#
# Derived from TendVocal `runtimes/alignment/worker.py` (same author; see NOTICES.md). The
# HTTP server of the original is gone: this worker has no port, no listener and no network
# path. It is embedded in the Tendshelf binary and launched only by internal/align/runtime.
#
# Protocol "tendshelf-emissions-v1", one request at a time:
#   ready line (stdout):  {"status":"ready","nonce":...,"identity":{...}}
#   request  (stdin):     one JSON header line, then samples*2 bytes of s16le mono 16 kHz
#                         {"op":"emissions","id","audioSha256","samples","fileQuickHash","offsetMs"}
#   response (stdout):    one JSON header line, then `bytes` of little-endian float16
#                         {"id","nonce","audioSha256","frames","vocabSize","frameMs","bytes"}
#                         or {"id","nonce","error":"bad_request"|"internal","detail":...}
# The process ends when stdin reaches end of file.
from __future__ import annotations

import argparse
import hashlib
import json
import os
import re
import socket
import sys
from pathlib import Path

PROTOCOL = "tendshelf-emissions-v1"
NONCE_ENV = "TENDSHELF_ALIGN_NONCE"
SAMPLE_RATE = 16000
FRAME_MS = 20
MAX_HEADER = 64 * 1024
MAX_SAMPLES = 544000  # 34 s
MIN_SAMPLES = 400  # one wav2vec2 receptive field
LOG_FLOOR = -1000.0  # keeps every value finite in float16
DELIMITER = "|"

# language -> (repository, pinned revision)
MODELS = {
    "en": ("facebook/wav2vec2-base-960h", "22aad52d435eb6dbaf354bdad9b0da84ce7d6156"),
    "es": (
        "jonatasgrosman/wav2vec2-large-xlsr-53-spanish",
        "80106547eee5065e01618d6588c22c256e8d0e70",
    ),
}
REQUIRED_FILES = {"config.json", "model.safetensors", "preprocessor_config.json", "vocab.json"}
HEX64 = re.compile(r"[a-f0-9]{64}")
REQUEST_ID = re.compile(r"[a-zA-Z0-9_-]{1,100}")


def block_network():
    """Make every socket connect or bind raise, before any library is imported."""

    def refuse(*_args, **_kwargs):
        raise OSError("network access is disabled in the alignment worker")

    socket.socket.connect = refuse
    socket.socket.connect_ex = refuse
    socket.socket.bind = refuse


def isolate_stdout():
    """Return the protocol stream and point fd 1 at stderr so library prints never reach it."""
    proto = os.fdopen(os.dup(1), "wb", buffering=0)
    os.dup2(2, 1)
    return proto


def model_sha(files):
    """Same digest as Go ModelSHA: sha256 of "name\\tsize\\tsha256\\n" lines sorted by name."""
    lines = "".join(f"{name}\t{size}\t{digest}\n" for name, size, digest in sorted(files))
    return hashlib.sha256(lines.encode("utf-8")).hexdigest()


def hash_model_dir(root):
    """Hash every file in the model folder; refuse extra folders, links and special files."""
    root = Path(root).resolve(strict=True)
    files = []
    for path in sorted(root.iterdir()):
        if path.is_symlink() or not path.is_file():
            raise ValueError("model folder must contain only regular local files")
        digest = hashlib.sha256()
        with path.open("rb") as source:
            for chunk in iter(lambda: source.read(1 << 20), b""):
                digest.update(chunk)
        files.append((path.name, path.stat().st_size, digest.hexdigest()))
    if not REQUIRED_FILES <= {name for name, _, _ in files}:
        raise ValueError("model folder is missing required files")
    return files


def read_vocab(root):
    """Vocabulary ordered by id; blank is the id of <pad>."""
    raw = json.loads((Path(root) / "vocab.json").read_text(encoding="utf-8"))
    vocab = [None] * len(raw)
    for token, index in raw.items():
        if not isinstance(index, int) or not 0 <= index < len(vocab) or vocab[index] is not None:
            raise ValueError("vocab.json ids are not a dense permutation")
        vocab[index] = token
    if "<pad>" not in raw:
        raise ValueError("vocab.json has no <pad> token")
    return vocab, raw["<pad>"]


def package_versions():
    from importlib import metadata

    return {
        name: metadata.version(name)
        for name in ("torch", "torchaudio", "transformers", "numpy", "safetensors")
    }


def build_identity(model_dir, language, versions):
    if language not in MODELS:
        raise ValueError("unsupported language")
    repository, revision = MODELS[language]
    files = hash_model_dir(model_dir)
    vocab, blank = read_vocab(model_dir)
    return {
        "protocol": PROTOCOL,
        "model": repository,
        "revision": revision,
        "modelSha256": model_sha(files),
        "language": language,
        "delimiter": DELIMITER,
        "vocab": vocab,
        "blank": blank,
        "frameMs": FRAME_MS,
        "sampleRate": SAMPLE_RATE,
        "versions": versions,
    }


def expected_frames(samples):
    return (samples - 400) // 320 + 1


class TorchBackend:
    """wav2vec2 CTC on CPU. torch and transformers are imported only here."""

    def __init__(self, model_dir, threads, device):
        if device != "cpu":
            raise ValueError("only the cpu device is supported")
        import numpy
        import torch
        from transformers import Wav2Vec2FeatureExtractor, Wav2Vec2ForCTC

        self.numpy = numpy
        self.torch = torch
        torch.set_num_threads(threads)
        root = str(model_dir)
        self.extractor = Wav2Vec2FeatureExtractor.from_pretrained(root, local_files_only=True)
        self.model = Wav2Vec2ForCTC.from_pretrained(
            root, local_files_only=True, use_safetensors=True
        )
        self.model.eval()

    def emit(self, pcm):
        """pcm: s16le bytes. Returns (frames, vocab_size, float16 little-endian bytes)."""
        np, torch = self.numpy, self.torch
        samples = np.frombuffer(pcm, dtype="<i2").astype(np.float32) / 32768.0
        values = self.extractor(
            samples, sampling_rate=SAMPLE_RATE, return_tensors="pt"
        ).input_values
        with torch.inference_mode():
            logits = self.model(values).logits
            logp = torch.log_softmax(logits.float(), dim=-1)[0]
        logp = logp.clamp(min=LOG_FLOOR, max=0.0)
        out = logp.numpy().astype("<f2")
        return out.shape[0], out.shape[1], out.tobytes()


def write_line(stream, obj):
    stream.write(json.dumps(obj, allow_nan=False, separators=(",", ":")).encode("utf-8") + b"\n")
    stream.flush()


def read_exact(stream, count):
    data = b""
    while len(data) < count:
        chunk = stream.read(count - len(data))
        if not chunk:
            raise EOFError("stdin closed mid-request")
        data += chunk
    return data


def validate_header(header):
    """Return (id, audioSha256, samples). Raises ValueError for anything off-protocol."""
    if not isinstance(header, dict) or header.get("op") != "emissions":
        raise ValueError("unknown op")
    request_id = header.get("id")
    digest = header.get("audioSha256")
    samples = header.get("samples")
    if not isinstance(request_id, str) or not REQUEST_ID.fullmatch(request_id):
        raise ValueError("invalid id")
    if not isinstance(digest, str) or not HEX64.fullmatch(digest):
        raise ValueError("invalid audio hash")
    if type(samples) is not int or not MIN_SAMPLES <= samples <= MAX_SAMPLES:
        raise ValueError("samples out of range")
    return request_id, digest, samples


def serve(stdin, proto, backend, identity, nonce):
    """Answer requests until stdin ends. Returns the process exit code."""
    write_line(proto, {"status": "ready", "nonce": nonce, "identity": identity})
    vocab_size = len(identity["vocab"])
    while True:
        line = stdin.readline(MAX_HEADER + 1)
        if not line:
            return 0  # stdin EOF: the server is gone or closed us
        if len(line) > MAX_HEADER or not line.endswith(b"\n"):
            return 2  # the stream cannot be resynchronised
        request_id = ""
        try:
            try:
                header = json.loads(line.decode("utf-8"))
            except (ValueError, UnicodeError):
                write_line(proto, {"id": "", "nonce": nonce, "error": "bad_request", "detail": "header"})
                return 2
            try:
                request_id, digest, samples = validate_header(header)
            except ValueError as err:
                if isinstance(header, dict) and isinstance(header.get("id"), str):
                    request_id = header["id"][:100]
                write_line(proto, {"id": request_id, "nonce": nonce, "error": "bad_request", "detail": str(err)})
                return 2  # the body length is not trusted, so the stream is lost
            pcm = read_exact(stdin, samples * 2)
            if hashlib.sha256(pcm).hexdigest() != digest:
                write_line(proto, {"id": request_id, "nonce": nonce, "error": "bad_request", "detail": "audio hash"})
                continue
            try:
                frames, size, data = backend.emit(pcm)
                if size != vocab_size or abs(frames - expected_frames(samples)) > 2:
                    raise ValueError("backend shape")
                if len(data) != frames * size * 2:
                    raise ValueError("backend size")
            except Exception as err:  # traceback goes to stderr, never to the protocol stream
                import traceback

                traceback.print_exc(file=sys.stderr)
                write_line(proto, {"id": request_id, "nonce": nonce, "error": "internal", "detail": type(err).__name__})
                continue
            write_line(
                proto,
                {
                    "id": request_id,
                    "nonce": nonce,
                    "audioSha256": digest,
                    "frames": frames,
                    "vocabSize": size,
                    "frameMs": FRAME_MS,
                    "bytes": len(data),
                },
            )
            proto.write(data)
            proto.flush()
        except EOFError:
            return 0
        except BrokenPipeError:
            return 0


def parse_args(argv):
    parser = argparse.ArgumentParser(description="Tendshelf alignment worker")
    parser.add_argument("--model-dir", required=True, type=Path)
    parser.add_argument("--language", choices=sorted(MODELS), required=True)
    parser.add_argument("--threads", type=int, default=2)
    parser.add_argument("--device", default="cpu", choices=["cpu"])
    mode = parser.add_mutually_exclusive_group(required=True)
    mode.add_argument("--describe", action="store_true")
    mode.add_argument("--serve", action="store_true")
    args = parser.parse_args(argv)
    if not 1 <= args.threads <= 16:
        parser.error("threads must be 1 to 16")
    return args


def main(argv=None, backend_factory=None, versions=None):
    """backend_factory and versions let tests run the real loop without torch."""
    proto = isolate_stdout()
    block_network()
    args = parse_args(sys.argv[1:] if argv is None else argv)
    identity = build_identity(
        args.model_dir, args.language, package_versions() if versions is None else versions
    )
    if args.describe:
        write_line(proto, identity)
        return 0
    nonce = os.environ.pop(NONCE_ENV, "")
    if not HEX64.fullmatch(nonce):
        print("missing or malformed " + NONCE_ENV, file=sys.stderr)
        return 2
    factory = backend_factory or TorchBackend
    backend = factory(args.model_dir.resolve(), args.threads, args.device)
    return serve(sys.stdin.buffer, proto, backend, identity, nonce)


if __name__ == "__main__":
    sys.exit(main())
