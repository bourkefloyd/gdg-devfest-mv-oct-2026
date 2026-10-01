import {
  Activity,
  ArrowRight,
  Bot,
  Check,
  ChevronRight,
  CircleDot,
  Clock3,
  Grip,
  RotateCcw,
  ShieldCheck,
  Sparkles,
  Trophy,
  UserRound,
  Wifi,
  WifiOff,
  X,
  Zap,
} from "lucide-react";
import { useCallback, useEffect, useRef, useState } from "react";
import {
  connectEvents,
  createGame,
  submitWord,
  type ArenaEvent,
  type ArenaGame,
  type ArenaPlayer,
  type PlayerId,
} from "./api";
import { WordHuntBoard } from "./components/Board";
import { Badge, Button, Card, cn } from "./components/ui";

type Mode = "race" | "human_vs_gemini";

const starterPlayers: Record<string, ArenaPlayer> = {
  gemma: {
    id: "gemma",
    name: "Gemma",
    backend: "Local Metal",
    model: "Gemma 4 E2B",
    fallback: false,
    score: 0,
    accepted: [],
    rejected: [],
    status: "waiting",
  },
  gemini: {
    id: "gemini",
    name: "Gemini",
    backend: "Vertex AI",
    model: "Gemini 2.5 Flash",
    fallback: false,
    score: 0,
    accepted: [],
    rejected: [],
    status: "waiting",
  },
};

const initialGame: ArenaGame = {
  id: "preflight",
  tiles: "STARNETOLOIDMEPC",
  endsAt: new Date(Date.now() + 80_000).toISOString(),
  duration: 80,
  mode: "race",
  maxScore: 19_600,
  players: starterPlayers,
  commentary: [],
  status: "ready",
};

function App() {
  const [mode, setMode] = useState<Mode>("race");
  const [game, setGame] = useState<ArenaGame>(initialGame);
  const [isMock, setIsMock] = useState(true);
  const [loading, setLoading] = useState(false);
  const [streamConnected, setStreamConnected] = useState(false);
  const [error, setError] = useState("");
  const [selectedPath, setSelectedPath] = useState<number[]>([]);
  const [dragging, setDragging] = useState(false);
  const [submission, setSubmission] = useState("");
  const cancelStream = useRef<() => void>(() => undefined);
  const startRequest = useRef(0);

  const applyEvent = useCallback((event: ArenaEvent) => {
    setGame((current) => reduceEvent(current, event));
  }, []);

  const startGame = useCallback(
    async (nextMode: Mode = mode) => {
      const request = ++startRequest.current;
      cancelStream.current();
      setLoading(true);
      setError("");
      setSelectedPath([]);
      setSubmission("");
      try {
        const result = await createGame({ mode: nextMode, duration_s: 80 });
        if (request !== startRequest.current) return;
        const running = { ...result.game, status: "running" as const };
        setGame(running);
        setIsMock(result.mock);
        cancelStream.current = connectEvents(
          running,
          result.mock,
          applyEvent,
          setStreamConnected,
        );
      } catch (reason) {
        setError(reason instanceof Error ? reason.message : "Could not start a game");
      } finally {
        setLoading(false);
      }
    },
    [applyEvent, mode],
  );

  useEffect(() => {
    void startGame("race");
    return () => {
      startRequest.current += 1;
      cancelStream.current();
    };
    // Start exactly once; mode changes happen via the controls.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  useEffect(() => {
    const stop = () => setDragging(false);
    window.addEventListener("pointerup", stop);
    window.addEventListener("pointercancel", stop);
    return () => {
      window.removeEventListener("pointerup", stop);
      window.removeEventListener("pointercancel", stop);
    };
  }, []);

  const humanMode = game.mode === "human_vs_gemini";
  const opponent = humanMode
    ? (game.players.human ?? makeHuman())
    : (game.players.gemma ?? starterPlayers.gemma);
  const gemini = game.players.gemini ?? starterPlayers.gemini;
  const selectedWord = selectedPath.map((index) => game.tiles[index]).join("");

  function chooseMode(nextMode: Mode) {
    setMode(nextMode);
    void startGame(nextMode);
  }

  function beginPath(index: number) {
    if (!humanMode || game.status === "finished") return;
    setDragging(true);
    setSelectedPath([index]);
    setSubmission("");
  }

  function extendPath(index: number) {
    if (!dragging || !humanMode) return;
    setSelectedPath((path) => {
      if (path.includes(index)) return path;
      const last = path[path.length - 1];
      return adjacent(last, index) ? [...path, index] : path;
    });
  }

  async function playSelection() {
    if (selectedPath.length < 3) {
      setSubmission("Connect at least 3 tiles");
      return;
    }
    try {
      const result = await submitWord(
        game.id,
        selectedWord,
        selectedPath,
        isMock,
      );
      applyEvent({
        type: "word",
        player: "human",
        word: selectedWord,
        accepted: result.accepted,
        reason: result.reason,
        points: result.points,
        total: result.total,
      });
      setSubmission(
        result.accepted
          ? `+${result.points ?? 0} · accepted`
          : humanReason(result.reason),
      );
      setSelectedPath([]);
    } catch (reason) {
      setSubmission(reason instanceof Error ? reason.message : "Could not submit");
    }
  }

  return (
    <main className="min-h-screen overflow-hidden bg-[#080a09] text-white">
      <Ambient />
      <div className="relative mx-auto max-w-[1480px] px-4 pb-10 pt-4 sm:px-6 lg:px-8">
        <Header
          mode={mode}
          onMode={chooseMode}
          isMock={isMock}
          connected={streamConnected}
        />

        <section className="mb-4 mt-7 flex flex-col justify-between gap-5 lg:flex-row lg:items-end">
          <div>
            <div className="mb-3 flex items-center gap-2">
              <Badge tone="lime">
                <CircleDot className="size-3 fill-current" />
                live arena
              </Badge>
              <span className="font-mono text-[11px] uppercase tracking-[.14em] text-zinc-500">
                Game {game.id.slice(-6)}
              </span>
            </div>
            <h1 className="max-w-3xl text-[clamp(2rem,4vw,4.25rem)] font-extrabold leading-[.95] tracking-[-.055em]">
              Two models. One board.
              <span className="block text-zinc-500">Every point verified.</span>
            </h1>
          </div>
          <div className="flex flex-wrap items-center gap-3">
            <Timer endsAt={game.endsAt} status={game.status} />
            <Button
              variant="outline"
              onClick={() => void startGame()}
              disabled={loading}
            >
              <RotateCcw className={cn("size-4", loading && "animate-spin")} />
              New board
            </Button>
          </div>
        </section>

        {error && (
          <div className="mb-4 rounded-xl border border-rose-400/25 bg-rose-400/10 px-4 py-3 text-sm text-rose-200">
            {error}
          </div>
        )}

        <div className="grid gap-4 xl:grid-cols-[minmax(650px,1.55fr)_minmax(380px,.85fr)]">
          <Card className="relative overflow-hidden p-4 sm:p-5">
            <div className="mb-5 flex items-center justify-between">
              <div>
                <div className="text-xs font-bold uppercase tracking-[.17em] text-zinc-500">
                  Head-to-head
                </div>
                <div className="mt-1 text-lg font-semibold">
                  {humanMode ? "You vs Gemini" : "Gemma vs Gemini"}
                </div>
              </div>
              <Badge tone="neutral">
                <ShieldCheck className="size-3" />
                server validated
              </Badge>
            </div>

            <div className="grid items-stretch gap-4 md:grid-cols-[1fr_300px_1fr] lg:grid-cols-[1fr_330px_1fr]">
              <PlayerPanel player={opponent} side="left" />
              <WordHuntBoard
                tiles={game.tiles}
                selectedPath={selectedPath}
                interactive={humanMode && game.status !== "finished"}
                onBegin={beginPath}
                onEnter={extendPath}
              />
              <PlayerPanel player={gemini} side="right" />
            </div>

            {humanMode && (
              <div className="mx-auto mt-5 flex max-w-xl items-center justify-between gap-3 rounded-2xl border border-white/10 bg-black/20 p-2 pl-4">
                <div className="min-w-0">
                  <div className="flex items-center gap-2">
                    <Grip className="size-4 text-lime-300" />
                    <span className="truncate font-mono text-lg font-semibold tracking-[.12em]">
                      {selectedWord || "DRAG LETTERS"}
                    </span>
                  </div>
                  <div
                    className={cn(
                      "mt-0.5 text-[11px] text-zinc-500",
                      submission.startsWith("+") && "text-lime-300",
                    )}
                  >
                    {submission || "Connect adjacent tiles without reusing one"}
                  </div>
                </div>
                <Button
                  size="sm"
                  onClick={() => void playSelection()}
                  disabled={selectedPath.length < 3}
                >
                  Submit <ChevronRight className="size-3.5" />
                </Button>
              </div>
            )}

            <ScoreRail
              opponent={opponent}
              gemini={gemini}
              maxScore={game.maxScore}
            />
          </Card>

          <div className="grid gap-4 xl:grid-rows-[auto_1fr]">
            <CommentaryCard
              commentary={game.commentary}
              status={game.status}
            />
            <RejectedCard players={[opponent, gemini]} />
          </div>
        </div>

        <footer className="mt-4 flex flex-col justify-between gap-3 px-1 text-[11px] text-zinc-600 sm:flex-row">
          <div className="flex items-center gap-4">
            <span className="flex items-center gap-1.5">
              <Activity className="size-3.5" />
              SSE event stream
            </span>
            <span className="flex items-center gap-1.5">
              <ShieldCheck className="size-3.5" />
              server-authoritative scoring
            </span>
          </div>
          <span className="font-mono uppercase tracking-[.12em]">
            Word Hunt Arena · build / secure / scale
          </span>
        </footer>
      </div>
    </main>
  );
}

function Header({
  mode,
  onMode,
  isMock,
  connected,
}: {
  mode: Mode;
  onMode: (mode: Mode) => void;
  isMock: boolean;
  connected: boolean;
}) {
  return (
    <header className="flex items-center justify-between gap-4 border-b border-white/8 pb-4">
      <div className="flex items-center gap-3">
        <div className="grid size-10 place-items-center rounded-xl bg-lime-300 text-zinc-950 shadow-[0_0_30px_rgba(190,242,100,.14)]">
          <Zap className="size-5 fill-current" />
        </div>
        <div>
          <div className="font-extrabold leading-tight tracking-[-.03em]">
            WORD HUNT <span className="text-lime-300">ARENA</span>
          </div>
          <div className="font-mono text-[9px] uppercase tracking-[.23em] text-zinc-500">
            Google AI showdown
          </div>
        </div>
      </div>

      <div className="hidden rounded-full border border-white/10 bg-black/20 p-1 sm:flex">
        <button
          className={cn(
            "rounded-full px-4 py-2 text-xs font-semibold transition",
            mode === "race"
              ? "bg-white text-zinc-950"
              : "text-zinc-500 hover:text-white",
          )}
          onClick={() => onMode("race")}
        >
          Model race
        </button>
        <button
          className={cn(
            "rounded-full px-4 py-2 text-xs font-semibold transition",
            mode === "human_vs_gemini"
              ? "bg-white text-zinc-950"
              : "text-zinc-500 hover:text-white",
          )}
          onClick={() => onMode("human_vs_gemini")}
        >
          Human challenge
        </button>
      </div>

      <Badge tone={connected ? (isMock ? "amber" : "lime") : "red"}>
        {connected ? <Wifi className="size-3" /> : <WifiOff className="size-3" />}
        {connected ? (isMock ? "mock stream" : "gateway live") : "connecting"}
      </Badge>
    </header>
  );
}

function PlayerPanel({
  player,
  side,
}: {
  player: ArenaPlayer;
  side: "left" | "right";
}) {
  const gemini = player.id === "gemini";
  return (
    <div
      className={cn(
        "flex min-h-[320px] flex-col rounded-2xl border p-4",
        gemini
          ? "border-sky-300/15 bg-sky-400/[.035]"
          : "border-orange-300/15 bg-orange-300/[.03]",
      )}
    >
      <div className="mb-5 flex items-start justify-between gap-2">
        <div className="flex items-center gap-2.5">
          <div
            className={cn(
              "grid size-9 place-items-center rounded-xl",
              gemini
                ? "bg-sky-300/10 text-sky-300"
                : "bg-orange-300/10 text-orange-300",
            )}
          >
            {player.id === "human" ? (
              <UserRound className="size-4.5" />
            ) : gemini ? (
              <Sparkles className="size-4.5" />
            ) : (
              <Bot className="size-4.5" />
            )}
          </div>
          <div>
            <div className="font-bold">{player.name}</div>
            <div className="mt-0.5 font-mono text-[9px] uppercase tracking-[.12em] text-zinc-600">
              {player.model}
            </div>
          </div>
        </div>
        <Thinking status={player.status} />
      </div>

      <div className="flex items-end justify-between border-b border-white/7 pb-4">
        <div>
          <div className="font-mono text-[9px] uppercase tracking-[.15em] text-zinc-600">
            score
          </div>
          <div className="mt-0.5 text-4xl font-extrabold tabular-nums tracking-[-.06em]">
            {player.score.toLocaleString()}
          </div>
        </div>
        {player.latencyMs && (
          <span className="font-mono text-[10px] text-zinc-500">
            {(player.latencyMs / 1000).toFixed(2)}s
          </span>
        )}
      </div>

      <div className="mt-4 flex-1">
        <div className="mb-2 flex items-center justify-between">
          <span className="text-[10px] font-bold uppercase tracking-[.14em] text-zinc-600">
            Valid words
          </span>
          <span className="font-mono text-[10px] text-zinc-600">
            {player.accepted.length}
          </span>
        </div>
        <div className={cn("flex flex-wrap gap-1.5", side === "right" && "")}>
          {player.accepted.length ? (
            player.accepted.map((word, index) => (
              <span
                key={`${word}-${index}`}
                className={cn(
                  "animate-word rounded-md border px-2 py-1 font-mono text-[11px] font-medium uppercase",
                  gemini
                    ? "border-sky-300/15 bg-sky-300/8 text-sky-200"
                    : "border-orange-300/15 bg-orange-300/8 text-orange-200",
                )}
              >
                {word}
              </span>
            ))
          ) : (
            <span className="text-xs italic text-zinc-700">
              {player.status === "thinking" ? "Scanning board…" : "Waiting to start"}
            </span>
          )}
        </div>
      </div>

      <div className="mt-4 flex items-center justify-between gap-2">
        <Badge tone={player.fallback ? "amber" : gemini ? "blue" : "neutral"}>
          {player.fallback && <ArrowRight className="size-3" />}
          {player.fallback ? "fallback · " : ""}
          {player.backend}
        </Badge>
      </div>
    </div>
  );
}

function Thinking({ status }: { status: ArenaPlayer["status"] }) {
  if (status === "finished") {
    return (
      <span className="grid size-6 place-items-center rounded-full bg-lime-300/10 text-lime-300">
        <Check className="size-3.5" />
      </span>
    );
  }
  if (status === "thinking") {
    return (
      <span className="flex items-center gap-1.5 text-[9px] font-bold uppercase tracking-[.12em] text-lime-300">
        <span className="size-1.5 animate-pulse rounded-full bg-current" />
        thinking
      </span>
    );
  }
  return <span className="size-2 rounded-full bg-zinc-700" />;
}

function ScoreRail({
  opponent,
  gemini,
  maxScore,
}: {
  opponent: ArenaPlayer;
  gemini: ArenaPlayer;
  maxScore: number;
}) {
  const total = Math.max(opponent.score + gemini.score, 1);
  const split = Math.max(8, Math.min(92, (opponent.score / total) * 100));
  const leader =
    opponent.score === gemini.score
      ? "Tied"
      : opponent.score > gemini.score
        ? opponent.name
        : gemini.name;
  return (
    <div className="mt-5 rounded-2xl border border-white/8 bg-black/20 px-4 py-3">
      <div className="mb-2 flex items-center justify-between text-[10px] font-semibold uppercase tracking-[.12em] text-zinc-500">
        <span className="flex items-center gap-1.5">
          <Trophy className="size-3.5 text-lime-300" />
          {leader === "Tied" ? "Neck and neck" : `${leader} leads`}
        </span>
        <span>
          Solver ceiling{" "}
          <strong className="ml-1 font-mono text-zinc-300">
            {maxScore ? maxScore.toLocaleString() : "—"}
          </strong>
        </span>
      </div>
      <div className="flex h-2 overflow-hidden rounded-full bg-sky-300/20">
        <div
          className="rounded-r-full bg-gradient-to-r from-orange-400 to-lime-300 transition-all duration-700"
          style={{ width: `${split}%` }}
        />
      </div>
    </div>
  );
}

function CommentaryCard({
  commentary,
  status,
}: {
  commentary: string[];
  status: ArenaGame["status"];
}) {
  const latest =
    commentary[commentary.length - 1] ??
    "The arena is warming up. Both players see the same 16 tiles; only server-validated words reach the scoreboard.";
  return (
    <Card className="overflow-hidden">
      <div className="flex items-center justify-between border-b border-white/8 px-5 py-4">
        <div className="flex items-center gap-2">
          <div className="grid size-7 place-items-center rounded-lg bg-violet-300/10 text-violet-300">
            <Sparkles className="size-3.5" />
          </div>
          <div>
            <div className="text-xs font-bold uppercase tracking-[.12em]">
              Gemini commentary
            </div>
            <div className="text-[9px] text-zinc-600">Flash Lite · validated data only</div>
          </div>
        </div>
        {status === "running" && (
          <div className="flex gap-1">
            <span className="size-1 animate-pulse rounded-full bg-violet-300" />
            <span className="size-1 animate-pulse rounded-full bg-violet-300 [animation-delay:150ms]" />
            <span className="size-1 animate-pulse rounded-full bg-violet-300 [animation-delay:300ms]" />
          </div>
        )}
      </div>
      <div className="relative px-5 py-5">
        <div className="absolute left-5 top-4 font-serif text-4xl text-violet-300/25">
          “
        </div>
        <p className="min-h-[50px] pl-5 text-[15px] font-medium leading-relaxed text-zinc-200">
          {latest}
        </p>
        {commentary.length > 1 && (
          <div className="mt-3 border-t border-white/7 pt-3 text-xs leading-relaxed text-zinc-600">
            {commentary[commentary.length - 2]}
          </div>
        )}
      </div>
    </Card>
  );
}

function RejectedCard({ players }: { players: ArenaPlayer[] }) {
  const rejected = players.flatMap((player) =>
    player.rejected.map((item) => ({ ...item, player: player.name })),
  );
  return (
    <Card className="flex min-h-[250px] flex-col overflow-hidden">
      <div className="flex items-center justify-between border-b border-white/8 px-5 py-4">
        <div>
          <div className="text-xs font-bold uppercase tracking-[.12em]">
            Rejected claims
          </div>
          <div className="mt-0.5 text-[9px] text-zinc-600">
            Transparent server-side validation
          </div>
        </div>
        <Badge tone={rejected.length ? "red" : "neutral"}>
          {rejected.length} caught
        </Badge>
      </div>
      <div className="flex-1 p-3">
        {rejected.length ? (
          <div className="space-y-2">
            {rejected.map((item, index) => (
              <div
                key={`${item.player}-${item.word}-${index}`}
                className="animate-word flex items-center justify-between gap-4 rounded-xl border border-rose-300/10 bg-rose-300/[.045] px-3 py-2.5"
              >
                <div className="flex items-center gap-2.5">
                  <span className="grid size-6 place-items-center rounded-lg bg-rose-300/10 text-rose-300">
                    <X className="size-3.5" />
                  </span>
                  <div>
                    <div className="font-mono text-xs font-semibold uppercase text-rose-100">
                      {item.word}
                    </div>
                    <div className="text-[9px] text-zinc-600">{item.player}</div>
                  </div>
                </div>
                <span className="rounded-md border border-white/8 bg-black/20 px-2 py-1 font-mono text-[9px] text-zinc-400">
                  {reasonLabel(item.reason)}
                </span>
              </div>
            ))}
          </div>
        ) : (
          <div className="grid h-full min-h-[150px] place-items-center text-center">
            <div>
              <ShieldCheck className="mx-auto mb-2 size-6 text-zinc-700" />
              <div className="text-xs font-semibold text-zinc-500">
                No invalid claims yet
              </div>
              <div className="mt-1 text-[10px] text-zinc-700">
                Hallucinations will appear here with reasons.
              </div>
            </div>
          </div>
        )}
      </div>
    </Card>
  );
}

function Timer({
  endsAt,
  status,
}: {
  endsAt: string;
  status: ArenaGame["status"];
}) {
  const [now, setNow] = useState(Date.now());
  useEffect(() => {
    const id = window.setInterval(() => setNow(Date.now()), 250);
    return () => window.clearInterval(id);
  }, []);
  const seconds =
    status === "finished"
      ? 0
      : Math.max(0, Math.ceil((new Date(endsAt).getTime() - now) / 1000));
  return (
    <div className="flex h-11 items-center gap-3 rounded-full border border-white/10 bg-white/5 px-4">
      <Clock3 className={cn("size-4 text-lime-300", seconds < 10 && "text-rose-300")} />
      <div className="font-mono text-lg font-medium tabular-nums">
        00:{seconds.toString().padStart(2, "0")}
      </div>
    </div>
  );
}

function Ambient() {
  return (
    <div aria-hidden className="pointer-events-none fixed inset-0">
      <div className="absolute -left-56 top-20 size-[480px] rounded-full bg-lime-300/[.035] blur-[100px]" />
      <div className="absolute -right-32 top-1/3 size-[430px] rounded-full bg-sky-400/[.035] blur-[110px]" />
      <div className="absolute inset-0 opacity-[.022] [background-image:linear-gradient(rgba(255,255,255,.7)_1px,transparent_1px),linear-gradient(90deg,rgba(255,255,255,.7)_1px,transparent_1px)] [background-size:48px_48px]" />
    </div>
  );
}

function reduceEvent(game: ArenaGame, event: ArenaEvent): ArenaGame {
  if (event.type === "state") return event.game;
  if (event.type === "commentary") {
    return { ...game, commentary: [...game.commentary, event.text].slice(-5) };
  }
  if (event.type === "game_over") {
    if (event.game) {
      const players = Object.fromEntries(
        Object.entries(event.game.players).map(([id, player]) => [
          id,
          { ...player, status: "finished" as const },
        ]),
      );
      return {
        ...event.game,
        commentary: game.commentary,
        status: "finished",
        players,
      };
    }
    const players = Object.fromEntries(
      Object.entries(game.players).map(([id, player]) => [
        id,
        { ...player, status: "finished" as const },
      ]),
    );
    return {
      ...game,
      status: "finished",
      maxScore: event.max_score ?? game.maxScore,
      players,
    };
  }
  if (event.type === "player_started") {
    return updatePlayer(game, event.player, (player) => ({
      ...player,
      status: "thinking",
    }));
  }
  if (event.type === "word") {
    return updatePlayer(game, event.player, (player) => {
      if (event.accepted) {
        if (player.accepted.includes(event.word)) return player;
        return {
          ...player,
          accepted: [...player.accepted, event.word],
          score: event.total ?? player.score + (event.points ?? 0),
        };
      }
      return {
        ...player,
        rejected: [
          ...player.rejected,
          { word: event.word, reason: event.reason ?? "rejected" },
        ],
      };
    });
  }
  if (event.type === "player_result") {
    return updatePlayer(game, event.player, (player) => ({
      ...player,
      backend: event.backend ?? player.backend,
      model: event.model ?? player.model,
      fallback: event.fallback ?? player.fallback,
      latencyMs: event.latency_ms ?? player.latencyMs,
      accepted: event.accepted ?? player.accepted,
      rejected: event.rejected ?? player.rejected,
      score: event.score ?? player.score,
      status: "finished",
    }));
  }
  return game;
}

function updatePlayer(
  game: ArenaGame,
  id: PlayerId,
  update: (player: ArenaPlayer) => ArenaPlayer,
) {
  const existing = game.players[id] ?? (id === "human" ? makeHuman() : starterPlayers[id]);
  return {
    ...game,
    players: { ...game.players, [id]: update(existing) },
  };
}

function makeHuman(): ArenaPlayer {
  return {
    id: "human",
    name: "You",
    backend: "Human input",
    model: "Drag to play",
    fallback: false,
    score: 0,
    accepted: [],
    rejected: [],
    status: "thinking",
  };
}

function adjacent(from: number, to: number) {
  const fromRow = Math.floor(from / 4);
  const fromCol = from % 4;
  const toRow = Math.floor(to / 4);
  const toCol = to % 4;
  return (
    Math.abs(fromRow - toRow) <= 1 &&
    Math.abs(fromCol - toCol) <= 1 &&
    from !== to
  );
}

function reasonLabel(reason: string) {
  return reason.replaceAll("_", " ");
}

function humanReason(reason?: string) {
  if (!reason) return "Rejected by server";
  return reasonLabel(reason);
}

export default App;
