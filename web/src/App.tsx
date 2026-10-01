import { Grid2X2, Swords } from "lucide-react";
import { useState } from "react";
import ArenaView from "./arena/ArenaView";
import "./arena/arena.css";
import RaceView from "./RaceView";

type ProductView = "arena" | "race";

function App() {
  const [view, setView] = useState<ProductView>("arena");

  return (
    <div className="product-shell">
      <header className="product-nav">
        <button className="product-brand" onClick={() => setView("arena")}>
          <span className="brand-mark">W</span>
          <span className="brand">
            <strong>WORD HUNT</strong>
            <span>GOOGLE AI SHOWDOWN</span>
          </span>
        </button>
        <nav aria-label="Game views">
          <button
            className={view === "arena" ? "active" : ""}
            onClick={() => setView("arena")}
          >
            <Grid2X2 size={14} />
            Load arena
          </button>
          <button
            className={view === "race" ? "active" : ""}
            onClick={() => setView("race")}
          >
            <Swords size={14} />
            Head-to-head
          </button>
        </nav>
        <span className="product-tagline">Build · secure · scale</span>
      </header>
      {view === "arena" ? <ArenaView /> : <RaceView />}
    </div>
  );
}

export default App;
