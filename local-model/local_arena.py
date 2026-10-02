"""Localhost arena seat for the 4-bit DiffusionGemma already on this Mac.

Binds 127.0.0.1:8787 only. The model stays loaded for the life of the process.
One generate at a time. A run plays at most 4 seats, all as
"DiffusionGemma 26B (local)".
"""

from __future__ import annotations

import json
import queue
import random
import re
import threading
import time
import uuid
from datetime import datetime, timezone
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from urllib.parse import urlparse

import mlx.core as mx

HOST = "127.0.0.1"
PORT = 8787
MAX_SEATS = 4
# One canvas sized for about 40 words. The decoder rejects batch size > 1.
MAX_CANDIDATES = 40
MAX_NEW_TOKENS = 96
CANVAS_LENGTH = 96
MAX_DENOISING_STEPS = 24
DIFFUSION_THRESHOLD = 0.95
PROFILE = "DiffusionGemma 26B (local)"
PROFILE_ID = "diffusiongemma-local"
MODEL_ID = "mlx-community/diffusiongemma-26B-A4B-it-4bit"
WEIGHTS = Path(
    "~/.cache/huggingface/hub/models--mlx-community--diffusiongemma-26B-A4B-it-4bit/snapshots/a7a81407613811e8ba63af92ac0d852b809e191f"
).expanduser()
DICT_PATH = Path(__file__).resolve().parents[1] / "go-gateway" / "dict" / "enable1.txt"
ALLOWED_ORIGINS = {
    "https://wordrust-958584846348.us-central1.run.app",
    "https://wordrust-zah6ccgmva-uc.a.run.app",
    "http://127.0.0.1:8787",
    "http://localhost:8787",
    "http://127.0.0.1:4317",
    "http://localhost:4317",
    "http://127.0.0.1:8080",
    "http://localhost:8080",
    "http://127.0.0.1:43617",
    "http://localhost:43617",
}
DICE = (
    "aaeegn", "abbjoo", "achops", "affkps",
    "aoottw", "cimotu", "deilrx", "delrvy",
    "distty", "eeghnw", "eeinsu", "ehrtvw",
    "eiosst", "elrtty", "himnqu", "hlnnrz",
)
WORDS_RE = re.compile(r"WORDS:\s*(.*)", re.IGNORECASE | re.DOTALL)
WORD_RE = re.compile(r"[A-Za-z]+")

MODEL = None
TOKENIZER = None
INFER_Q: queue.Queue = queue.Queue()
MODEL_READY = threading.Event()
RUNS: dict[str, "Run"] = {}
RUNS_LOCK = threading.Lock()
ACTIVE: str | None = None
WORDS: set[str] = set()
PREFIXES: set[str] = set()


def now_iso() -> str:
    return datetime.now(timezone.utc).isoformat()


def score_word(word: str) -> int:
    n = len(word)
    if n < 3:
        return 0
    if n == 3:
        return 100
    if n == 4:
        return 400
    if n == 5:
        return 800
    if n == 6:
        return 1400
    if n == 7:
        return 1800
    return 2200 + 400 * (n - 8)


def load_dict() -> None:
    for line in DICT_PATH.read_text().splitlines():
        word = line.strip().lower()
        if not word.isalpha() or not 3 <= len(word) <= 16:
            continue
        WORDS.add(word)
        for i in range(1, len(word)):
            PREFIXES.add(word[:i])


def neighbors(pos: int) -> list[int]:
    row, col = divmod(pos, 4)
    out = []
    for dr in (-1, 0, 1):
        for dc in (-1, 0, 1):
            if dr == 0 and dc == 0:
                continue
            r, c = row + dr, col + dc
            if 0 <= r < 4 and 0 <= c < 4:
                out.append(r * 4 + c)
    return out


NEIGHBORS = [neighbors(i) for i in range(16)]


def find_path(tiles: str, word: str) -> list[int] | None:
    letters = tiles.lower()
    word = word.lower()

    def visit(pos: int, at: int, used: int, path: list[int]) -> list[int] | None:
        if letters[pos] != word[at]:
            return None
        path.append(pos)
        if at == len(word) - 1:
            return list(path)
        used |= 1 << pos
        for nxt in NEIGHBORS[pos]:
            if used & (1 << nxt):
                continue
            found = visit(nxt, at + 1, used, path)
            if found:
                return found
        path.pop()
        return None

    for start in range(16):
        found = visit(start, 0, 0, [])
        if found:
            return found
    return None


def board_solutions(tiles: str) -> set[str]:
    letters = tiles.lower()
    found: set[str] = set()

    def dfs(pos: int, used: int, prefix: str) -> None:
        prefix += letters[pos]
        if len(prefix) >= 3 and prefix in WORDS:
            found.add(prefix)
        if len(prefix) == 16 or prefix not in PREFIXES and prefix not in WORDS:
            return
        used |= 1 << pos
        for nxt in NEIGHBORS[pos]:
            if used & (1 << nxt) == 0:
                dfs(nxt, used, prefix)

    for start in range(16):
        dfs(start, 0, "")
    return found


def count_solutions(tiles: str) -> int:
    return len(board_solutions(tiles))


def perfect_score(tiles: str) -> int:
    return sum(score_word(word) for word in board_solutions(tiles))


def normalize_tiles(value: object) -> str | None:
    if not isinstance(value, str):
        return None
    letters = "".join(ch for ch in value.upper() if "A" <= ch <= "Z")
    if len(letters) != 16:
        return None
    return letters


def make_board(seed: int) -> tuple[str, int]:
    rng = random.Random(seed)
    best = ""
    best_n = -1
    for _ in range(12):
        order = list(range(16))
        rng.shuffle(order)
        tiles = [""] * 16
        for pos, die in enumerate(order):
            faces = DICE[die]
            tiles[pos] = faces[rng.randrange(len(faces))]
        text = "".join(tiles).upper()
        n = len(board_solutions(text))
        if n > best_n:
            best, best_n = text, n
        if n >= 40:
            break
    return best, best_n


def board_prompt(tiles: str) -> str:
    rows = []
    for row in range(4):
        cells = [f"{row * 4 + col}:{tiles[row * 4 + col]}" for col in range(4)]
        rows.append(" ".join(cells))
    grid = "\n".join(" ".join(tiles[row * 4 + col] for col in range(4)) for row in range(4))
    instruction = (
        "Trace paths of 3 or more letters. Neighbors include diagonals. "
        "Do not reuse a cell inside one word. "
        f"Start immediately with WORDS: then {MAX_CANDIDATES} English words that are on this grid, longest first, comma-separated. "
        "No thinking and no extra commas."
    )
    return (
        "Word Hunt board (index:letter):\n" + "\n".join(rows)
        + "\nGrid:\n" + grid + "\n" + instruction
    )


def parse_words(raw: str) -> list[str]:
    match = WORDS_RE.search(raw or "")
    if not match:
        return []
    seen: set[str] = set()
    out = []
    for part in WORD_RE.findall(match.group(1)):
        word = part.lower()
        if len(word) < 3 or len(word) > 16 or word in seen:
            continue
        seen.add(word)
        out.append(word)
    out.sort(key=len, reverse=True)
    return out[:MAX_CANDIDATES]


def _generate_on_loader_thread(tiles: str) -> tuple[list[str], float]:
    """One diffusion decode on the loader thread. Empty output is not retried."""
    from optiq.vlm._mlxvlm.generate.diffusion import stream_diffusion_generate

    user = board_prompt(tiles)
    prompt = TOKENIZER.apply_chat_template(
        [{"role": "user", "content": user}],
        tokenize=False,
        add_generation_prompt=True,
    )
    ids = TOKENIZER.encode(prompt, add_special_tokens=True)
    input_ids = mx.array(ids, dtype=mx.int32)[None]
    started = time.perf_counter()
    parts: list[str] = []
    steps = 0
    for result in stream_diffusion_generate(
        MODEL,
        TOKENIZER,
        TOKENIZER,
        input_ids,
        None,
        None,
        max_tokens=MAX_NEW_TOKENS,
        skip_special_token_ids=set(),
        temperature=0.0,
        max_denoising_steps=MAX_DENOISING_STEPS,
        diffusion_min_canvas_length=CANVAS_LENGTH,
        diffusion_max_canvas_length=CANVAS_LENGTH,
        diffusion_sampler="confidence-threshold",
        diffusion_threshold=DIFFUSION_THRESHOLD,
    ):
        if not getattr(result, "is_draft", False):
            parts.append(result.text)
        steps = int(getattr(result, "diffusion_denoising_steps", 0) or steps)
    text = "".join(parts)
    elapsed = time.perf_counter() - started
    preview = " ".join(text.split())[:180]
    print(f"decode {elapsed:.2f}s steps={steps} chars={len(text)} {preview!r}", flush=True)
    return parse_words(text), elapsed


def generate_words(tiles: str) -> tuple[list[str], float]:
    box: queue.Queue = queue.Queue(maxsize=1)
    INFER_Q.put((tiles, box))
    result = box.get()
    if isinstance(result, BaseException):
        raise result
    return result


class Run:
    def __init__(self, run_id: str, n: int, duration_s: float, player_mix: str, seed: int, tiles: str, perfect: int):
        self.id = run_id
        self.n = n
        self.duration_s = duration_s
        self.player_mix = player_mix
        self.seed = seed
        self.tiles = tiles
        self.perfect = perfect
        self.started = now_iso()
        self.status = "running"
        self.events: list[dict] = []
        self.leaderboard: list[dict] = []
        self.latencies: list[float] = []
        self.words = 0
        self.errors = 0
        self.running = 0
        self.cancel = threading.Event()
        self.finished = threading.Event()
        self.cond = threading.Condition()

    def stats(self) -> dict:
        ordered = sorted(self.latencies)
        def pct(q: float) -> float:
            if not ordered:
                return 0
            idx = min(len(ordered) - 1, max(0, int(round((len(ordered) - 1) * q))))
            return ordered[idx]

        return {
            "running": self.running,
            "completed": len(self.leaderboard),
            "total": self.n,
            "words": self.words,
            "words_per_second": 0,
            "p50_latency_ms": pct(0.5),
            "p95_latency_ms": pct(0.95),
            "errors": self.errors,
            "retries": 0,
            "games_running": self.running,
        }

    def publish(self, event: dict) -> None:
        with self.cond:
            event = dict(event)
            event["id"] = len(self.events) + 1
            event.setdefault("run_id", self.id)
            event.setdefault("at", now_iso())
            event["stats"] = self.stats()
            self.events.append(event)
            self.cond.notify_all()

    def start_body(self) -> dict:
        return {
            "run_id": self.id,
            "status": "running",
            "n": self.n,
            "duration_ms": int(self.duration_s * 1000),
            "started_at": self.started,
            "events_url": f"/api/arena/runs/{self.id}/events",
            "player_mix": self.player_mix,
            "seed": self.seed,
            "tiles": self.tiles,
        }


def play(run: Run) -> None:
    global ACTIVE
    try:
        run.publish({"type": "run_started", "status": "running"})
        for index in range(run.n):
            if run.cancel.is_set():
                break
            game_id = f"{run.id}-{index + 1:03d}"
            run.running = 1
            run.publish({
                "type": "game_started",
                "status": "running",
                "game": {
                    "id": game_id,
                    "index": index,
                    "tiles": run.tiles,
                    "player_name": PROFILE,
                    "name": PROFILE,
                    "model": PROFILE,
                    "backend": "local-mlx",
                    "profile": PROFILE,
                    "profile_id": PROFILE_ID,
                    "perfect_score": run.perfect,
                },
            })
            entry = {
                "game_id": game_id,
                "name": PROFILE,
                "backend": "local-mlx",
                "model": PROFILE,
                "score": 0,
                "words": [],
                "word_count": 0,
                "latency_ms": 0,
                "perfect_score": run.perfect,
                "time_to_score_ms": 0,
                "profile": PROFILE,
                "profile_id": PROFILE_ID,
                "retries": 0,
            }
            try:
                claims, elapsed = generate_words(run.tiles)
                entry["latency_ms"] = round(elapsed * 1000, 1)
                entry["time_to_score_ms"] = entry["latency_ms"]
                run.latencies.append(entry["latency_ms"])
                print(f"seat {game_id} {entry['latency_ms']}ms claims={len(claims)}", flush=True)
            except Exception as exc:
                entry["error"] = str(exc)
                run.errors += 1
                print(f"seat {game_id} failed: {exc}", flush=True)
                run.leaderboard.append(entry)
                run.running = 0
                run.publish({"type": "game_finished", "result": entry})
                continue
            seen: set[str] = set()
            for word in claims:
                if run.cancel.is_set() or word in seen:
                    continue
                seen.add(word)
                if word not in WORDS:
                    continue
                path = find_path(run.tiles, word)
                if not path:
                    continue
                points = score_word(word)
                entry["score"] += points
                entry["words"].append(word)
                entry["word_count"] = len(entry["words"])
                run.words += 1
                run.publish({
                    "type": "word",
                    "game_id": game_id,
                    "player_name": PROFILE,
                    "word": word,
                    "path": path,
                    "points": points,
                    "total_score": entry["score"],
                    "word_event": {
                        "game_id": game_id,
                        "player_name": PROFILE,
                        "word": word,
                        "path": path,
                        "points": points,
                        "total": entry["score"],
                        "profile_id": PROFILE_ID,
                    },
                })
                if run.cancel.is_set():
                    break
            run.leaderboard.append(entry)
            run.running = 0
            run.publish({"type": "game_finished", "result": entry})
        run.status = "cancelled" if run.cancel.is_set() else "finished"
        event_type = "run_cancelled" if run.status == "cancelled" else "run_finished"
        run.publish({
            "type": event_type,
            "status": run.status,
            "leaderboard": list(run.leaderboard),
            "message": "run cancelled" if run.status == "cancelled" else "",
        })
    finally:
        run.finished.set()
        with RUNS_LOCK:
            if ACTIVE == run.id:
                ACTIVE = None
        with run.cond:
            run.cond.notify_all()


class Handler(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def log_message(self, fmt: str, *args) -> None:
        print(f"{self.address_string()} {fmt % args}", flush=True)

    def _cors(self) -> None:
        origin = self.headers.get("Origin")
        if origin in ALLOWED_ORIGINS:
            self.send_header("Access-Control-Allow-Origin", origin)
            self.send_header("Vary", "Origin")
        self.send_header("Access-Control-Allow-Private-Network", "true")
        self.send_header("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
        requested = self.headers.get("Access-Control-Request-Headers")
        self.send_header(
            "Access-Control-Allow-Headers",
            requested or "Authorization, Content-Type, Last-Event-ID",
        )
        self.send_header("Access-Control-Max-Age", "600")

    def _json(self, status: int, body: dict) -> None:
        raw = json.dumps(body).encode()
        self.send_response(status)
        self._cors()
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(raw)))
        self.send_header("Cache-Control", "no-store")
        self.end_headers()
        self.wfile.write(raw)

    def do_OPTIONS(self) -> None:
        self.send_response(204)
        self._cors()
        self.send_header("Content-Length", "0")
        self.end_headers()

    def do_GET(self) -> None:
        path = urlparse(self.path).path.rstrip("/") or "/"
        if path == "/api/health":
            self._json(200, {
                "status": "ok",
                "model": MODEL_ID,
                "profile": PROFILE,
                "profile_id": PROFILE_ID,
                "loaded": MODEL is not None,
            })
            return
        parts = [p for p in path.split("/") if p]
        if len(parts) == 4 and parts[:3] == ["api", "arena", "runs"]:
            run = RUNS.get(parts[3])
            if not run:
                self._json(404, {"error": {"code": "run_not_found", "message": "run not found"}})
                return
            self._json(200, {
                "run_id": run.id,
                "status": run.status,
                "n": run.n,
                "tiles": run.tiles,
                "seed": run.seed,
                "player_mix": run.player_mix,
                "stats": run.stats(),
                "leaderboard": run.leaderboard,
            })
            return
        if len(parts) == 5 and parts[:3] == ["api", "arena", "runs"] and parts[4] == "events":
            run = RUNS.get(parts[3])
            if not run:
                self._json(404, {"error": {"code": "run_not_found", "message": "run not found"}})
                return
            self._events(run)
            return
        self._json(404, {"error": {"code": "not_found", "message": "no such route"}})

    def do_POST(self) -> None:
        global ACTIVE
        path = urlparse(self.path).path.rstrip("/") or "/"
        parts = [p for p in path.split("/") if p]
        if len(parts) == 5 and parts[:3] == ["api", "arena", "runs"] and parts[4] == "cancel":
            run = RUNS.get(parts[3])
            if not run:
                self._json(404, {"error": {"code": "run_not_found", "message": "run not found"}})
                return
            run.cancel.set()
            self._json(202, {"run_id": run.id, "status": "cancelled"})
            return
        if path not in ("/api/arena/runs", "/api/arena/start"):
            self._json(404, {"error": {"code": "not_found", "message": "no such route"}})
            return
        length = int(self.headers.get("Content-Length") or 0)
        if length > 16 * 1024:
            self._json(400, {"error": {"code": "invalid_json", "message": "body too large"}})
            return
        raw = self.rfile.read(length) if length else b""
        try:
            request = json.loads(raw.decode() or "{}")
        except json.JSONDecodeError:
            self._json(400, {"error": {"code": "invalid_json", "message": "invalid start request"}})
            return
        if not isinstance(request, dict):
            self._json(400, {"error": {"code": "invalid_json", "message": "start request must be an object"}})
            return
        asked = request.get("count", request.get("n", 1))
        try:
            asked = int(asked)
        except (TypeError, ValueError):
            self._json(400, {"error": {"code": "invalid_request", "message": "count must be an integer"}})
            return
        n = max(1, min(MAX_SEATS, asked))
        duration = request.get("duration_s", 10)
        try:
            duration = float(duration)
        except (TypeError, ValueError):
            duration = 10
        mix = request.get("player_mix") or "mixed"
        seed = request.get("seed")
        try:
            seed = int(seed) if seed is not None else random.SystemRandom().randrange(1, 2**31 - 1)
        except (TypeError, ValueError):
            self._json(400, {"error": {"code": "invalid_request", "message": "seed must be an integer"}})
            return
        supplied = request.get("tiles")
        if supplied is None or supplied == "":
            tiles, _word_count = make_board(seed)
        else:
            tiles = normalize_tiles(supplied)
            if tiles is None:
                self._json(400, {"error": {"code": "invalid_request", "message": "tiles must be 16 letters"}})
                return
        perfect = perfect_score(tiles)
        with RUNS_LOCK:
            if ACTIVE is not None:
                self._json(409, {"error": {"code": "busy", "message": "a local run is already playing"}})
                return
            run = Run(uuid.uuid4().hex[:16], n, duration, str(mix), seed, tiles, perfect)
            RUNS[run.id] = run
            ACTIVE = run.id
        threading.Thread(target=play, args=(run,), daemon=True).start()
        body = run.start_body()
        raw_body = json.dumps(body).encode()
        self.send_response(202)
        self._cors()
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(raw_body)))
        self.send_header("Location", body["events_url"])
        self.send_header("Cache-Control", "no-store")
        self.end_headers()
        self.wfile.write(raw_body)

    def _events(self, run: Run) -> None:
        after = 0
        header = self.headers.get("Last-Event-ID")
        if header:
            try:
                after = int(header)
            except ValueError:
                self._json(400, {"error": {"code": "invalid_event_id", "message": "Last-Event-ID must be an unsigned integer"}})
                return
        self.send_response(200)
        self._cors()
        self.send_header("Content-Type", "text/event-stream")
        self.send_header("Cache-Control", "no-cache, no-transform")
        self.send_header("Connection", "keep-alive")
        self.send_header("X-Accel-Buffering", "no")
        self.end_headers()
        try:
            self.wfile.write(b"retry: 1000\n\n")
            self.wfile.flush()
            cursor = after
            while True:
                with run.cond:
                    while cursor >= len(run.events) and not run.finished.is_set():
                        run.cond.wait(timeout=15)
                        if cursor >= len(run.events):
                            break
                    batch = list(run.events[cursor:])
                    done = run.finished.is_set() and cursor + len(batch) >= len(run.events)
                if not batch:
                    self.wfile.write(b": keep-alive\n\n")
                    self.wfile.flush()
                    if done:
                        return
                    continue
                for event in batch:
                    payload = json.dumps(event, separators=(",", ":")).encode()
                    header_bits = f"id: {event['id']}\nevent: {event['type']}\ndata: ".encode()
                    self.wfile.write(header_bits + payload + b"\n\n")
                    cursor = event["id"]
                self.wfile.flush()
                if done or any(event.get("type") in ("run_finished", "run_cancelled") for event in batch):
                    return
        except (BrokenPipeError, ConnectionResetError):
            return


def mlx_loop() -> None:
    global MODEL, TOKENIZER
    from optiq.vlm.diffusion_gemma import load

    print("loading dictionary", flush=True)
    load_dict()
    print(f"dict {len(WORDS)} words", flush=True)
    print(f"loading {MODEL_ID}", flush=True)
    started = time.perf_counter()
    MODEL, TOKENIZER = load(str(WEIGHTS))
    mx.eval(MODEL.parameters())
    print(f"loaded in {time.perf_counter() - started:.1f}s", flush=True)
    MODEL_READY.set()
    while True:
        tiles, box = INFER_Q.get()
        try:
            box.put(_generate_on_loader_thread(tiles))
        except Exception as exc:
            box.put(exc)


def main() -> None:
    threading.Thread(target=mlx_loop, name="mlx", daemon=True).start()
    if not MODEL_READY.wait(timeout=300):
        raise SystemExit("model did not load")
    server = ThreadingHTTPServer((HOST, PORT), Handler)
    print(f"listening http://{HOST}:{PORT}", flush=True)
    server.serve_forever()


if __name__ == "__main__":
    main()
