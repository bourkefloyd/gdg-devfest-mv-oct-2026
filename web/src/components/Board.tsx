import { cn } from "./ui";

export type WordHuntBoardProps = {
  tiles: string;
  selectedPath?: number[];
  interactive?: boolean;
  onBegin?: (index: number) => void;
  onEnter?: (index: number) => void;
  compact?: boolean;
};

/** Shared 4x4 board; game state and validation remain in the parent. */
export function WordHuntBoard({
  tiles,
  selectedPath = [],
  interactive = false,
  onBegin = () => undefined,
  onEnter = () => undefined,
  compact = false,
}: WordHuntBoardProps) {
  const letters = tiles.padEnd(16, "·").slice(0, 16).split("");
  return (
    <div
      className={cn(
        "relative mx-auto w-full self-center",
        compact ? "max-w-[230px]" : "max-w-[340px]",
      )}
    >
      <div className="absolute -inset-6 rounded-full bg-lime-300/[.035] blur-2xl" />
      <div
        className={cn(
          "relative grid aspect-square grid-cols-4 rounded-[28px] border border-white/10 bg-[#0d100e] shadow-[inset_0_1px_0_rgba(255,255,255,.06),0_30px_60px_rgba(0,0,0,.28)]",
          compact ? "gap-1.5 p-2.5" : "gap-2 p-3 sm:gap-2.5 sm:p-4",
          interactive && "touch-none select-none",
        )}
      >
        {letters.map((letter, index) => {
          const order = selectedPath.indexOf(index);
          return (
            <button
              key={index}
              aria-label={`Tile ${index + 1}: ${letter}`}
              className={cn(
                "group relative grid aspect-square place-items-center border border-white/10 bg-gradient-to-br from-zinc-700 to-zinc-900 font-extrabold text-white shadow-[inset_0_1px_0_rgba(255,255,255,.18),0_8px_15px_rgba(0,0,0,.23)] transition-all",
                compact
                  ? "rounded-xl text-lg"
                  : "rounded-[18px] text-[clamp(1.5rem,4vw,2.45rem)]",
                interactive &&
                  "cursor-crosshair hover:-translate-y-0.5 hover:border-lime-300/35",
                order >= 0 &&
                  "-translate-y-1 scale-[1.03] border-lime-200 bg-gradient-to-br from-lime-200 to-lime-400 text-zinc-950 shadow-[0_8px_24px_rgba(190,242,100,.25)]",
              )}
              onPointerDown={() => onBegin(index)}
              onPointerEnter={() => onEnter(index)}
              disabled={!interactive}
            >
              {letter}
              {!compact && (
                <span
                  className={cn(
                    "absolute bottom-1.5 right-2 font-mono text-[8px] text-zinc-600",
                    order >= 0 && "text-zinc-700",
                  )}
                >
                  {order >= 0 ? order + 1 : index + 1}
                </span>
              )}
            </button>
          );
        })}
      </div>
      {interactive && !compact && (
        <div className="mt-3 text-center text-[10px] font-semibold uppercase tracking-[.14em] text-zinc-600">
          Press and drag to build a word
        </div>
      )}
    </div>
  );
}
