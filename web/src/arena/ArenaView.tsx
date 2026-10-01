import {
  Activity, AlertTriangle, CircleStop, Clock3, Gauge, LoaderCircle, Play, RotateCcw,
  Sparkles, Trophy, Wifi, WifiOff, X,
} from "lucide-react";
import { useCallback, useEffect, useMemo, useReducer, useRef, useState } from "react";
import { cancelRun, connectRunEvents, createRun } from "./api";
import { arenaReducer, initialArenaState } from "./reducer";
import type { ArenaGame, ArenaState } from "./types";

const SWIPE_COLORS = ["#ff5e73", "#5eead4", "#facc15", "#c084fc", "#60a5fa", "#fb923c", "#f472b6", "#a3e635"];
const PREVIEW_BOARD = "STARNETOLOIDMEPC".split("");

function App() {
  const [state, dispatch] = useReducer(arenaReducer, initialArenaState);
  const [count, setCount] = useState(24);
  const [now, setNow] = useState(Date.now());
  const closeStream = useRef<() => void>(() => undefined);
  const request = useRef<AbortController | null>(null);

  useEffect(() => {
    const timer = window.setInterval(() => setNow(Date.now()), 500);
    return () => window.clearInterval(timer);
  }, []);

  useEffect(() => () => {
    request.current?.abort();
    closeStream.current();
  }, []);

  useEffect(() => {
    if (state.status === "finished" || state.status === "error") closeStream.current();
  }, [state.status]);

  const startRun = useCallback(async () => {
    request.current?.abort();
    closeStream.current();
    const controller = new AbortController();
    request.current = controller;
    const safeCount = Math.max(1, Math.min(100, Math.round(count || 1)));
    setCount(safeCount);
    dispatch({ type: "start", count: safeCount });
    try {
      const result = await createRun(safeCount, controller.signal);
      if (controller.signal.aborted) return;
      dispatch({ type: "created", runId: result.run_id });
      closeStream.current = connectRunEvents(
        result.run_id,
        (event) => dispatch({ type: "event", event }),
        () => dispatch({ type: "connection", connection: "live" }),
        () => dispatch({ type: "connection", connection: "reconnecting" }),
      );
    } catch (error) {
      if (!controller.signal.aborted) dispatch({ type: "failure", message: error instanceof Error ? error.message : "Unable to start arena" });
    }
  }, [count]);

  const stopRun = useCallback(async () => {
    if (!state.runId) return;
    dispatch({ type: "cancelling" });
    try {
      await cancelRun(state.runId);
    } catch (error) {
      dispatch({ type: "failure", message: error instanceof Error ? error.message : "Unable to cancel run" });
    }
  }, [state.runId]);

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
        </div>
        <div className="launch-control">
          <label htmlFor="game-count">Games</label>
          <div className="count-stepper">
            <button type="button" onClick={() => setCount((value) => Math.max(1, value - 1))} disabled={busy}>−</button>
            <input id="game-count" type="number" min="1" max="100" value={count} disabled={busy}
              onChange={(event) => setCount(Math.max(1, Math.min(100, Number(event.target.value) || 1)))} />
            <button type="button" onClick={() => setCount((value) => Math.min(100, value + 1))} disabled={busy}>+</button>
          </div>
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
          <span className="cap-note">1–100 concurrent</span>
        </div>
      </section>

      <Stats state={state} />

      <section className="arena-shell">
        <div className="arena-heading">
          <div>
            <h2>{state.status === "idle" ? "Arena preview" : `Run ${state.runId?.slice(0, 8) ?? "starting"}`}</h2>
            <span>{state.status === "idle" ? `${count} boards ready to deploy` : `${state.games.length} of ${state.requestedCount} boards provisioned`}</span>
          </div>
          <Connection state={state} />
        </div>

        {state.message && (
          <div className={state.status === "error" ? "notice error" : "notice"}>
            <AlertTriangle size={15} /> <span>{state.message}</span>
            {state.status === "error" && <button onClick={() => void startRun()}>Retry</button>}
          </div>
        )}

        <div className="board-grid" aria-live="polite">
          {visibleGames.map((game, index) => (
            <GameBoard key={`${game.id}-${index}`} game={game} now={now} runStartedAt={state.startedAt} preview={state.games.length === 0} />
          ))}
        </div>
      </section>

      {state.status === "finished" && <Results state={state} onClose={() => dispatch({ type: "reset" })} onRestart={() => void startRun()} />}
    </main>
  );
}

function Stats({ state }: { state: ArenaState }) {
  const cards = [
    { label: "Games running", value: state.stats.running.toString(), detail: `of ${state.requestedCount}`, icon: Activity },
    { label: "Words / sec", value: state.stats.wordsPerSecond.toFixed(1), detail: "verified", icon: Gauge },
    { label: "p50 latency", value: formatLatency(state.stats.p50LatencyMs), detail: "median", icon: Clock3 },
    { label: "p95 latency", value: formatLatency(state.stats.p95LatencyMs), detail: "tail", icon: Clock3 },
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

function GameBoard({ game, now, runStartedAt, preview }: { game: ArenaGame; now: number; runStartedAt?: number; preview: boolean }) {
  const board = game.board.length === 16 ? game.board : preview ? rotatedBoard(game.ordinal) : Array(16).fill("");
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
        <div><span>Words</span><strong>{game.words}</strong></div>
        <div><span>Timer</span><strong>{formatTimer(elapsed)}</strong></div>
      </div>
      {game.error && <div className="game-error"><AlertTriangle size={11} /> {game.error}</div>}
    </article>
  );
}

function Results({ state, onClose, onRestart }: { state: ArenaState; onClose: () => void; onRestart: () => void }) {
  const ranked = [...state.games].sort((a, b) => b.score - a.score || b.words - a.words);
  return (
    <div className="modal-backdrop" role="dialog" aria-modal="true" aria-labelledby="results-title">
      <section className="results-panel">
        <div className="results-head">
          <div className="trophy"><Trophy size={23} /></div>
          <div><span>Run complete</span><h2 id="results-title">Arena leaderboard</h2><p>{state.games.length} games · {state.games.reduce((sum, game) => sum + game.words, 0)} verified words</p></div>
          <button className="close" onClick={onClose} aria-label="Close results"><X size={20} /></button>
        </div>
        <div className="leaderboard-head"><span>Rank / agent</span><span>Model / backend</span><span>Score</span><span>Words</span><span>Latency</span></div>
        <div className="leaderboard">
          {ranked.map((game, index) => (
            <div className="leader-row" key={game.id}>
              <div><b>{index + 1}</b><span className="agent-dot" style={{ background: SWIPE_COLORS[game.ordinal % SWIPE_COLORS.length] }} /><strong>{game.name}</strong></div>
              <div><strong>{game.model}</strong><small>{game.backend}</small></div>
              <strong>{game.score.toLocaleString()}</strong><span>{game.words}</span><span>{formatLatency(game.latencyMs ?? 0)}</span>
            </div>
          ))}
        </div>
        <div className="results-actions"><button onClick={onClose}>View boards</button><button className="play-button" onClick={onRestart}><RotateCcw size={17} /> Start over</button></div>
      </section>
    </div>
  );
}

function previewGame(index: number): ArenaGame {
  return { id: `preview-${index}`, ordinal: index, board: rotatedBoard(index), name: `Agent ${String(index + 1).padStart(2, "0")}`, model: index % 2 ? "Gemini 2.5 Flash" : "Gemma 3", backend: index % 2 ? "Vertex AI" : "Local GPU", score: 0, words: 0, elapsedMs: 0, currentWord: "", swipe: { path: [], word: "", updatedAt: 0 }, status: "queued" };
}
function rotatedBoard(index: number) {
  const shift = index % PREVIEW_BOARD.length;
  return [...PREVIEW_BOARD.slice(shift), ...PREVIEW_BOARD.slice(0, shift)];
}
function formatTimer(ms: number) {
  const seconds = Math.max(0, Math.floor(ms / 1000));
  return `${String(Math.floor(seconds / 60)).padStart(2, "0")}:${String(seconds % 60).padStart(2, "0")}`;
}
function formatLatency(ms: number) {
  if (!ms) return "—";
  return ms >= 1000 ? `${(ms / 1000).toFixed(2)}s` : `${Math.round(ms)}ms`;
}

export default App;
