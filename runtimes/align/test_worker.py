"""Pure-Python checks of the worker protocol with a stub backend. No torch needed.

Run: python3 -I runtimes/align/test_worker.py
"""
import hashlib
import json
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

HERE = Path(__file__).resolve().parent
NONCE = "ab" * 32
VOCAB = {"<pad>": 0, "<s>": 1, "</s>": 2, "<unk>": 3, "|": 4, "A": 5}

# Runs worker.main with a stub backend; prints to stdout inside the backend to prove isolation.
DRIVER = """
import sys
sys.path.insert(0, {here!r})
import worker

class Stub:
    def __init__(self, model_dir, threads, device):
        print("library noise on stdout")
    def emit(self, pcm):
        frames = (len(pcm) // 2 - 400) // 320 + 1
        return frames, {vocab}, b"\\x00\\x00" * (frames * {vocab})

sys.exit(worker.main(sys.argv[1:], backend_factory=Stub, versions={{"torch": "stub"}}))
"""


def write_model_dir(root):
    for name in ("config.json", "model.safetensors", "preprocessor_config.json"):
        (root / name).write_bytes(name.encode())
    (root / "vocab.json").write_text(json.dumps(VOCAB))


def request(samples, digest=None, rid="r1"):
    pcm = b"\x01\x00" * samples
    header = {
        "op": "emissions",
        "id": rid,
        "audioSha256": digest or hashlib.sha256(pcm).hexdigest(),
        "samples": samples,
        "fileQuickHash": "q",
        "offsetMs": 0,
    }
    return json.dumps(header).encode() + b"\n" + pcm


def split(out):
    """Split worker stdout into header objects, skipping the binary bodies."""
    lines = []
    rest = out
    while rest:
        line, _, rest = rest.partition(b"\n")
        lines.append(json.loads(line))
        if "bytes" in lines[-1]:
            rest = rest[lines[-1]["bytes"]:]
    return lines


class WorkerTest(unittest.TestCase):
    def setUp(self):
        tmp = tempfile.TemporaryDirectory()
        self.addCleanup(tmp.cleanup)
        self.model = Path(tmp.name) / "model"
        self.model.mkdir()
        write_model_dir(self.model)
        self.driver = Path(tmp.name) / "driver.py"
        self.driver.write_text(DRIVER.format(here=str(HERE), vocab=len(VOCAB)))

    def run_worker(self, stdin, *extra, nonce=NONCE):
        env = {"LC_ALL": "C"}
        if nonce is not None:
            env["TENDSHELF_ALIGN_NONCE"] = nonce
        args = [sys.executable, "-I", "-B", str(self.driver), "--model-dir", str(self.model),
                "--language", "en", "--threads", "1", *extra]
        return subprocess.run(args, input=stdin, capture_output=True, env=env, timeout=60)

    def test_ready_framing_and_eof_exit(self):
        done = self.run_worker(request(3200) + request(4000, rid="r2"), "--serve")
        self.assertEqual(done.returncode, 0, done.stderr)
        ready, one, two = split(done.stdout)
        self.assertEqual(ready["status"], "ready")
        self.assertEqual(ready["nonce"], NONCE)
        self.assertEqual(ready["identity"]["protocol"], "tendshelf-emissions-v1")
        self.assertEqual(ready["identity"]["blank"], 0)
        self.assertEqual(ready["identity"]["vocab"][5], "A")
        self.assertEqual(len(ready["identity"]["modelSha256"]), 64)
        self.assertEqual((one["id"], one["nonce"], one["frames"], one["vocabSize"]), ("r1", NONCE, 9, 6))
        self.assertEqual(one["bytes"], 9 * 6 * 2)
        self.assertEqual(two["id"], "r2")
        self.assertEqual(two["frames"], (4000 - 400) // 320 + 1)

    def test_stdout_isolation(self):
        done = self.run_worker(request(3200), "--serve")
        self.assertNotIn(b"library noise", done.stdout)
        self.assertIn(b"library noise", done.stderr)

    def test_oversize_refused(self):
        header = json.loads(request(3200).split(b"\n")[0])
        header["samples"] = 544001
        done = self.run_worker(json.dumps(header).encode() + b"\n", "--serve")
        replies = split(done.stdout)
        self.assertEqual(replies[1]["error"], "bad_request")
        self.assertEqual(replies[1]["nonce"], NONCE)
        self.assertNotEqual(done.returncode, 0)

    def test_hash_mismatch_keeps_serving(self):
        done = self.run_worker(request(3200, digest="0" * 64, rid="bad") + request(3200), "--serve")
        replies = split(done.stdout)
        self.assertEqual(replies[1]["error"], "bad_request")
        self.assertEqual(replies[2]["id"], "r1")

    def test_describe_matches_ready_identity(self):
        described = self.run_worker(b"", "--describe", nonce=None)
        served = self.run_worker(b"", "--serve")
        self.assertEqual(split(described.stdout)[0], split(served.stdout)[0]["identity"])

    def test_missing_nonce_refused(self):
        done = self.run_worker(b"", "--serve", nonce=None)
        self.assertEqual(done.returncode, 2)
        self.assertEqual(done.stdout, b"")

    def test_model_sha_formula(self):
        sys.path.insert(0, str(HERE))
        import worker

        files = [("b", 2, "y" * 64), ("a", 1, "x" * 64)]
        want = hashlib.sha256(
            ("a\t1\t" + "x" * 64 + "\n" + "b\t2\t" + "y" * 64 + "\n").encode()
        ).hexdigest()
        self.assertEqual(worker.model_sha(files), want)


if __name__ == "__main__":
    unittest.main()
