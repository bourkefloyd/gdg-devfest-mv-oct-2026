import {
  Activity, AlertTriangle, CircleStop, Clock3, Gauge, LoaderCircle, Play, RotateCcw,
  Sparkles, Trophy, Wifi, WifiOff, X,
} from "lucide-react";
import { memo, useCallback, useEffect, useMemo, useReducer, useRef, useState } from "react";
import { LOCAL_DIFFUSION_ORIGIN, cancelRun, connectRunEvents, createRun, probeLocalDiffusion } from "./api";
import { arenaReducer, initialArenaState, isLocalSeat } from "./reducer";
import type { ArenaGame, ArenaSource, ArenaState, NormalizedArenaEvent } from "./types";

const SWIPE_COLORS = ["#ff5e73", "#5eead4", "#facc15", "#c084fc", "#60a5fa", "#fb923c", "#f472b6", "#a3e635"];
const PREVIEW_BOARD = "STARNETOLOIDMEPC".split("");

function App() {
  const [state, dispatch] = useReducer(arenaReducer, initialArenaState);
  const [count, setCount] = useState(24);
  const [duration, setDuration] = useState<10 | 30>(() => new URLSearchParams(window.location.search).get("duration") === "30" ? 30 : 10);
  const [seedInput, setSeedInput] = useState(() => new URLSearchParams(window.location.search).get("seed") ?? "");
  const [runSeed, setRunSeed] = useState<number>();
  const [sharedTiles, setSharedTiles] = useState("");
  const [now, setNow] = useState(() => Date.now());
  const closeStream = useRef<() => void>(() => undefined);
  const request = useRef<AbortController | null>(null);
  const autoStarted = useRef(false);
  const queuedEvents = useRef<Array<{ event: NormalizedArenaEvent; source: ArenaSource }>>([]);
  const eventFrame = useRef(0);

  const flushEvents = useCallback(() => {
    eventFrame.current = 0;
    const batch = queuedEvents.current;
    if (!batch.length) return;
    queuedEvents.current = [];
    dispatch({ type: "events", batch });
  }, []);

  const enqueueEvent = useCallback((event: NormalizedArenaEvent, source: ArenaSource) => {
    queuedEvents.current.push({ event, source });
    if (!eventFrame.current) eventFrame.current = requestAnimationFrame(flushEvents);
  }, [flushEvents]);

  const dropQueuedEvents = useCallback(() => {
    queuedEvents.current = [];
    if (!eventFrame.current) return;
    cancelAnimationFrame(eventFrame.current);
    eventFrame.current = 0;
  }, []);

  useEffect(() => {
    const timer = window.setInterval(() => setNow(Date.now()), 500);
    return () => window.clearInterval(timer);
  }, []);

  useEffect(() => () => {
    request.current?.abort();
    closeStream.current();
    dropQueuedEvents();
  }, [dropQueuedEvents]);

  useEffect(() => {
    if (state.status === "finished" || state.status === "error") closeStream.current();
  }, [state.status]);

  const startRun = useCallback(async (freshBoard = false) => {
    request.current?.abort();
    closeStream.current();
    dropQueuedEvents();
    const controller = new AbortController();
    request.current = controller;
    const stops: { cloud: () => void; local: () => void } = { cloud: () => undefined, local: () => undefined };
    closeStream.current = () => {
      stops.cloud();
      stops.local();
    };
    const safeCount = Math.max(1, Math.min(100, Math.round(count || 1)));
    const requestedSeed = freshBoard ? undefined : parseSeed(seedInput);
    if (freshBoard) setSeedInput("");
    setCount(safeCount);
    dispatch({ type: "start", count: safeCount });
    dispatch({ type: "local-gateway", status: "checking" });
    const localProbe = probeLocalDiffusion(controller.signal);
    try {
      const result = await createRun(safeCount, "mixed", duration, requestedSeed, controller.signal);
      if (controller.signal.aborted) return;
      setRunSeed(result.seed);
      setSharedTiles(result.tiles);
      dispatch({ type: "created", runId: result.run_id });
      stops.cloud = connectRunEvents(
        result.run_id,
        (event) => enqueueEvent(event, "cloud"),
        () => dispatch({ type: "connection", connection: "live" }),
        () => dispatch({ type: "connection", connection: "reconnecting" }),
      );
      const localUp = await localProbe;
      if (controller.signal.aborted) return;
      if (!localUp) {
        dispatch({ type: "local-gateway", status: "offline" });
        return;
      }
      try {
        const local = await createRun(safeCount, "mixed", duration, requestedSeed ?? result.seed, controller.signal, LOCAL_DIFFUSION_ORIGIN);
        if (controller.signal.aborted) return;
        dispatch({ type: "local-gateway", status: "live", runId: local.run_id });
        let opened = false;
        let cancelled = false;
        const stopLocal = connectRunEvents(
          local.run_id,
          (event) => enqueueEvent(event, "local"),
          () => { opened = true; },
          () => { if (!cancelled && !opened) dispatch({ type: "local-gateway", status: "offline" }); },
          LOCAL_DIFFUSION_ORIGIN,
        );
        stops.local = () => {
          cancelled = true;
          stopLocal();
        };
      } catch {
        if (!controller.signal.aborted) dispatch({ type: "local-gateway", status: "offline" });
      }
    } catch (error) {
      if (!controller.signal.aborted) dispatch({ type: "failure", message: error instanceof Error ? error.message : "Unable to start arena" });
    }
  }, [count, dropQueuedEvents, duration, enqueueEvent, seedInput]);

  useEffect(() => {
    if (autoStarted.current || new URLSearchParams(window.location.search).get("autostart") !== "24") return;
    autoStarted.current = true;
    void startRun();
  }, [startRun]);

  const stopRun = useCallback(async () => {
    if (!state.runId && !state.localRunId) return;
    dispatch({ type: "cancelling" });
    const tasks: Array<Promise<void>> = [];
    if (state.runId) tasks.push(cancelRun(state.runId));
    if (state.localRunId) tasks.push(cancelRun(state.localRunId, LOCAL_DIFFUSION_ORIGIN));
    const results = await Promise.allSettled(tasks);
    const cloudFailed = state.runId ? results[0]?.status === "rejected" : false;
    if (cloudFailed) {
      const reason = results[0]?.status === "rejected" ? results[0].reason : undefined;
      dispatch({ type: "failure", message: reason instanceof Error ? reason.message : "Unable to cancel run" });
    }
  }, [state.localRunId, state.runId]);

  const busy = state.status === "starting" || state.status === "running" || state.status === "cancelling";
  const previewGames = useMemo(() => Array.from({ length: count }, (_, index) => previewGame(index)), [count]);
  const visibleGames = state.games.length ? state.games : previewGames;

  return (
    <main className="min-h-screen text-[#f8f6ed]">
      <section className="control-bar">
        <div className="control-copy">
          <div className="eyebrow"><Sparkles size={12} /> parallel inference arena</div>
          <h1>Watch every model <span>hunt.</span></h1>
          <p>One board per agent. Every swipe, score, and backend response in real time.</p>
          <div className="shared-board">
            <span>Shared board · seed {runSeed ?? "random"}</span>
            <strong>{sharedTiles ? sharedTiles.match(/.{1,4}/g)?.join(" · ") : "dealt when the run starts"}</strong>
          </div>
        </div>
        <div className="launch-control">
          <div className="launch-field">
            <label htmlFor="game-count">Games</label>
            <div className="count-stepper">
              <button type="button" onClick={() => setCount((value) => Math.max(1, value - 1))} disabled={busy}>−</button>
              <input id="game-count" type="number" min="1" max="100" value={count} disabled={busy}
                onChange={(event) => setCount(Math.max(1, Math.min(100, Number(event.target.value) || 1)))} />
              <button type="button" onClick={() => setCount((value) => Math.min(100, value + 1))} disabled={busy}>+</button>
            </div>
          </div>
          <label className="launch-field">Round
            <select value={duration} disabled={busy} onChange={(event) => setDuration(Number(event.target.value) as 10 | 30)}>
              <option value={10}>10s test</option>
              <option value={30}>30s demo</option>
            </select>
          </label>
          <label className="launch-field seed-field">Replay seed
            <input value={seedInput} disabled={busy} inputMode="numeric" placeholder="random"
              onChange={(event) => setSeedInput(event.target.value.replace(/[^\d-]/g, ""))} />
          </label>
          {busy ? (
            <button className="play-button stop" type="button" onClick={() => void stopRun()} disabled={state.status === "cancelling"}>
              {state.status === "cancelling" ? <LoaderCircle className="spin" size={18} /> : <CircleStop size={18} />}
              {state.status === "cancelling" ? "Stopping" : "Cancel run"}
            </button>
          ) : (
            <button className="play-button" type="button" onClick={() => void startRun()}>
              <Play fill="currentColor" size={18} /> Load test
            </button>
          )}
          <span className="cap-note">50% Gemini 3.8 Flash · 50% Gemma 4 26B A4B · DiffusionGemma on this Mac when :8787 is up</span>
        </div>
      </section>

      <Stats state={state} />

      <section className="arena-shell">
        <div className="arena-heading">
          <div>
            <h2>{state.status === "idle" ? "Arena preview" : `Run ${state.runId?.slice(0, 8) ?? "starting"}`}</h2>
            <span>{state.status === "idle" ? `${count} boards ready to deploy` : boardSummary(state)}</span>
          </div>
          <div className="arena-status">
            <LocalDiffusion state={state} />
            <Connection state={state} />
          </div>
        </div>

        {state.message && (
          <div className={state.status === "error" ? "notice error" : "notice"}>
            <AlertTriangle size={15} /> <span>{state.message}</span>
            {state.status === "error" && <button onClick={() => void startRun()}>Retry</button>}
          </div>
        )}

        <div className="board-grid" aria-live="polite">
          {visibleGames.map((game, index) => (
            <GameBoard key={`${game.id}-${index}`} game={game} now={game.status === "running" ? now : 0} runStartedAt={game.status === "running" ? state.startedAt : undefined} preview={state.games.length === 0} />
          ))}
        </div>
      </section>

      {state.status === "finished" && <Results state={state} onClose={() => dispatch({ type: "reset" })}
        onRestart={() => void startRun()} onNewBoard={() => void startRun(true)} />}
    </main>
  );
}

function Stats({ state }: { state: ArenaState }) {
  const cards = [
    { label: "Games running", value: state.stats.running.toString(), detail: `of ${state.requestedCount}`, icon: Activity },
    { label: "Words / sec", value: state.stats.wordsPerSecond.toFixed(1), detail: "verified", icon: Gauge },
    { label: "p50 latency", value: formatLatency(state.stats.p50LatencyMs), detail: "median", icon: Clock3 },
    { label: "p95 latency", value: formatLatency(state.stats.p95LatencyMs), detail: "tail", icon: Clock3 },
    { label: "Retries", value: state.stats.retries.toString(), detail: "same model", icon: RotateCcw },
    { label: "Errors", value: state.stats.errors.toString(), detail: state.stats.errors ? "needs review" : "all clear", icon: AlertTriangle },
  ];
  return (
    <section className="stats-row">
      {cards.map(({ label, value, detail, icon: Icon }) => (
        <article className="stat" key={label}>
          <div><Icon size={14} /><span>{label}</span></div>
          <strong>{value}</strong><small>{detail}</small>
        </article>
      ))}
    </section>
  );
}

function LocalDiffusion({ state }: { state: ArenaState }) {
  if (state.status === "idle" || state.localGateway === "unknown") return null;
  const offline = state.localGateway === "offline";
  const checking = state.localGateway === "checking";
  const label = offline
    ? "Local diffusion is offline"
    : checking
      ? "Checking local diffusion"
      : state.localLabel ?? "DiffusionGemma";
  return (
    <div className={`local-diffusion ${offline ? "offline" : checking ? "checking" : "live"}`}>
      <span className="local-dot" />
      <span>{label}</span>
    </div>
  );
}

function boardSummary(state: ArenaState) {
  if (state.localGateway === "live" || state.localGateway === "finished") {
    return `${state.games.length} boards · ${state.localLabel ?? "DiffusionGemma"} is its own group`;
  }
  return `${state.games.length} of ${state.requestedCount} boards provisioned`;
}

function seatGroup(game: ArenaGame) {
  if (isLocalSeat(game)) return game.profile ?? game.name;
  return game.profile ?? "Unknown profile";
}

function Connection({ state, compact = false }: { state: ArenaState; compact?: boolean }) {
  const live = state.connection === "live";
  const reconnecting = state.connection === "reconnecting" || state.connection === "connecting";
  return (
    <div className={`connection ${live ? "live" : reconnecting ? "pending" : ""} ${compact ? "compact" : ""}`}>
      {live ? <Wifi size={13} /> : reconnecting ? <LoaderCircle className="spin" size={13} /> : <WifiOff size={13} />}
      <span>{live ? "Stream live" : reconnecting ? (state.connection === "reconnecting" ? "Reconnecting" : "Connecting") : "Stream offline"}</span>
    </div>
  );
}

const GameBoard = memo(function GameBoard({ game, now, runStartedAt, preview }: { game: ArenaGame; now: number; runStartedAt?: number; preview: boolean }) {
  const board = game.board.length === 16 ? game.board : preview ? PREVIEW_BOARD : Array(16).fill("");
  const active = new Set(game.swipe.path);
  const color = game.swipe.color ?? SWIPE_COLORS[game.ordinal % SWIPE_COLORS.length];
  const elapsed = game.elapsedMs || (game.status === "running" && runStartedAt ? now - runStartedAt : 0);
  const pathPoints = game.swipe.path.map((index) => `${12.5 + (index % 4) * 25},${12.5 + Math.floor(index / 4) * 25}`).join(" ");
  return (
    <article className={`game-card ${game.status} ${preview ? "preview" : ""}`}>
      <div className="game-card-head">
        <div className="agent">
          <span className="agent-dot" style={{ background: color }} />
          <div><strong>{game.name}</strong><small>{game.model}</small></div>
        </div>
        <span className={`status-pill ${game.status}`}>{preview ? "ready" : game.status}</span>
      </div>
      <div className="mini-board">
        <div className="tiles">
          {board.map((letter, index) => <span className={active.has(index) ? "tile active" : "tile"} key={index}>{letter}</span>)}
        </div>
        {pathPoints && (
          <svg className="swipe-path" viewBox="0 0 100 100" preserveAspectRatio="none" aria-label={`Swipe ${game.swipe.word}`}>
            {game.swipe.path.length > 1 && <polyline points={pathPoints} fill="none" stroke="rgba(0,0,0,.24)" strokeWidth="7" strokeLinecap="round" strokeLinejoin="round" />}
            <polyline points={pathPoints} fill="none" stroke={color} strokeWidth="5" strokeLinecap="round" strokeLinejoin="round" />
            {game.swipe.path.map((index) => <circle key={index} cx={12.5 + (index % 4) * 25} cy={12.5 + Math.floor(index / 4) * 25} r="3" fill={color} />)}
          </svg>
        )}
        {game.status === "queued" && !preview && <div className="board-loading"><LoaderCircle className="spin" size={20} /><span>Provisioning</span></div>}
      </div>
      <div className="word-ribbon" style={{ borderColor: `${color}66` }}>
        <span>{game.currentWord || game.swipe.word || (preview ? "READY" : game.status === "finished" ? "COMPLETE" : "SCANNING…")}</span>
        <small>{game.backend}</small>
      </div>
      <div className="game-metrics">
        <div><span>Score</span><strong>{game.score.toLocaleString()}</strong></div>
        <div><span>Perfect</span><strong>{game.perfectScore ? game.perfectScore.toLocaleString() : "—"}</strong></div>
        <div><span>Words</span><strong>{game.words}</strong></div>
        <div><span>Timer</span><strong>{formatTimer(elapsed)}</strong></div>
      </div>
      {game.error && <div className="game-error"><AlertTriangle size={11} /> {game.error}</div>}
    </article>
  );
});

function Results({ state, onClose, onRestart, onNewBoard }: { state: ArenaState; onClose: () => void; onRestart: () => void; onNewBoard: () => void }) {
  const [tab, setTab] = useState<"complete" | "incomplete" | "all">("complete");
  const eligible = state.games.filter((game) => !isSolverRow(game));
  const complete = eligible.filter(isCompleteSeat);
  const incomplete = eligible.filter((game) => !isCompleteSeat(game));
  const ranked = [...(tab === "complete" ? complete : tab === "incomplete" ? incomplete : eligible)]
    .sort((a, b) => b.score - a.score || (a.timeToScoreMs ?? Infinity) - (b.timeToScoreMs ?? Infinity));
  const groups = Object.values(complete.reduce<Record<string, { name: string; games: number; score: number; words: number; latency: number; errors: number }>>((all, game) => {
    const name = seatGroup(game);
    const group = all[name] ??= { name, games: 0, score: 0, words: 0, latency: 0, errors: 0 };
    group.games++; group.score += game.score; group.words += game.words; group.latency += game.latencyMs ?? 0; group.errors += game.error ? 1 : 0;
    return all;
  }, {}));
  return (
    <div className="modal-backdrop" role="dialog" aria-modal="true" aria-labelledby="results-title">
      <section className="results-panel">
        <div className="results-head">
          <div className="trophy"><Trophy size={23} /></div>
          <div><span>Run complete</span><h2 id="results-title">Arena leaderboard</h2><p>{state.games.length} games · {state.games.reduce((sum, game) => sum + game.words, 0)} verified words</p></div>
          <button className="close" onClick={onClose} aria-label="Close results"><X size={20} /></button>
        </div>
        <div className="results-tabs">
          <button className={tab === "complete" ? "active" : ""} onClick={() => setTab("complete")}>Complete <b>{complete.length}</b></button>
          <button className={tab === "incomplete" ? "active" : ""} onClick={() => setTab("incomplete")}>Incomplete <b>{incomplete.length}</b></button>
          <button className={tab === "all" ? "active" : ""} onClick={() => setTab("all")}>All <b>{eligible.length}</b></button>
        </div>
        <div className="profile-groups">
          {groups.map((group) => <div key={group.name}><strong>{group.name}</strong><span>avg {Math.round(group.score / group.games).toLocaleString()} pts · {(group.words / group.games).toFixed(1)} words · {formatLatency(group.latency / group.games)} · {group.errors} errors</span></div>)}
          <div><strong>Incomplete seats</strong><span>{incomplete.length} excluded from profile averages</span></div>
        </div>
        <div className="leaderboard-head"><span>Rank / agent</span><span>Model / backend</span><span>Score / perfect</span><span>Words</span><span>Time</span></div>
        <div className="leaderboard">
          {ranked.map((game, index) => (
            <div className="leader-row" key={game.id}>
              <div><b>{index + 1}</b><span className="agent-dot" style={{ background: SWIPE_COLORS[game.ordinal % SWIPE_COLORS.length] }} /><strong>{game.name}</strong></div>
              <div><strong>{seatGroup(game)}</strong><small>{game.model} · {game.backend}</small></div>
              <strong>{game.score.toLocaleString()} / {game.perfectScore.toLocaleString()}</strong><span>{game.words}</span><span>{game.error ? "error" : formatLatency(game.timeToScoreMs ?? game.latencyMs ?? 0)} · {game.retries}r</span>
            </div>
          ))}
        </div>
        <div className="results-actions"><button onClick={onClose}>View boards</button><button onClick={onNewBoard}>New board</button><button className="play-button" onClick={onRestart}><RotateCcw size={17} /> Start over</button></div>
      </section>
    </div>
  );
}

function previewGame(index: number): ArenaGame {
  return { id: `preview-${index}`, ordinal: index, board: PREVIEW_BOARD, name: `Agent ${String(index + 1).padStart(2, "0")}`, model: index < 3 ? "gemini-3.8-flash" : "gemma-4-26b-a4b-it", backend: "Google GenAI", profile: index < 3 ? "Gemini 3.8 Flash" : "Gemma 4 26B A4B", score: 0, perfectScore: 0, words: 0, retries: 0, elapsedMs: 0, currentWord: "", swipe: { path: [], word: "", updatedAt: 0 }, status: "queued" };
}
function formatTimer(ms: number) {
  const seconds = Math.max(0, Math.floor(ms / 1000));
  return `${String(Math.floor(seconds / 60)).padStart(2, "0")}:${String(seconds % 60).padStart(2, "0")}`;
}
function formatLatency(ms: number) {
  if (!ms) return "—";
  return ms >= 1000 ? `${(ms / 1000).toFixed(2)}s` : `${Math.round(ms)}ms`;
}

function isCompleteSeat(game: ArenaGame) {
  return game.status === "finished" && !game.error && game.backend !== "—" && game.model !== "Awaiting model";
}

function isSolverRow(game: ArenaGame) {
  return game.backend === "in-process" || /solver bot|trie-dfs/i.test(`${game.model} ${game.profile ?? ""}`);
}

function parseSeed(value: string) {
  if (!value.trim()) return undefined;
  const parsed = Number(value);
  return Number.isSafeInteger(parsed) ? parsed : undefined;
}

export default App;
