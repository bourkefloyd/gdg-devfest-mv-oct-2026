package arena

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go-gateway/internal/players"
	"go-gateway/internal/wordhunt"
)

type testPlayer struct {
	delay time.Duration
	block bool
}

func (*testPlayer) Name() string { return "test player" }

func (p *testPlayer) Play(ctx context.Context, _ wordhunt.Board, _ time.Time) (players.Result, error) {
	if p.block {
		<-ctx.Done()
		return players.Result{}, ctx.Err()
	}
	timer := time.NewTimer(p.delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return players.Result{}, ctx.Err()
	case <-timer.C:
		return players.Result{
			Backend: "test",
			Model:   "stub",
			Latency: p.delay,
		}, nil
	}
}

func TestNGameFanoutIsBounded(t *testing.T) {
	var calls, active, peak atomic.Int32
	manager := NewManager(Config{
		MaxConcurrent: 5,
		MinDuration:   time.Millisecond,
		PlayerFactory: func(context.Context, GameSpec) (players.Player, error) {
			calls.Add(1)
			return playerFunc{
				name: "bounded",
				play: func(ctx context.Context, _ wordhunt.Board, _ time.Time) (players.Result, error) {
					now := active.Add(1)
					defer active.Add(-1)
					for {
						old := peak.Load()
						if now <= old || peak.CompareAndSwap(old, now) {
							break
						}
					}
					timer := time.NewTimer(5 * time.Millisecond)
					defer timer.Stop()
					select {
					case <-ctx.Done():
						return players.Result{}, ctx.Err()
					case <-timer.C:
						return players.Result{Backend: "test", Model: "fanout", Latency: 5 * time.Millisecond}, nil
					}
				},
			}, nil
		},
	})

	run, err := manager.Start(StartRequest{N: 37, Duration: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	waitDone(t, run)
	snapshot := run.Snapshot()
	if calls.Load() != 37 {
		t.Fatalf("factory calls = %d, want 37", calls.Load())
	}
	if got := len(snapshot.Leaderboard); got != 37 {
		t.Fatalf("leaderboard entries = %d, want 37", got)
	}
	if snapshot.Stats.Completed != 37 || snapshot.Status != StatusFinished {
		t.Fatalf("unexpected final snapshot: %#v", snapshot)
	}
	if got := peak.Load(); got == 0 || got > 5 {
		t.Fatalf("peak concurrency = %d, want 1..5", got)
	}
}

func TestGameCapValidation(t *testing.T) {
	manager := NewManager(Config{MaxGames: 7})
	if _, err := manager.Start(StartRequest{N: 8}); !errors.Is(err, ErrTooManyGames) {
		t.Fatalf("error = %v, want ErrTooManyGames", err)
	}
	if _, err := manager.Start(StartRequest{N: -1}); !errors.Is(err, ErrInvalidGameCount) {
		t.Fatalf("error = %v, want ErrInvalidGameCount", err)
	}
}

func TestCancellation(t *testing.T) {
	started := make(chan struct{})
	var once sync.Once
	manager := NewManager(Config{
		MaxConcurrent: 4,
		MinDuration:   time.Millisecond,
		PlayerFactory: func(context.Context, GameSpec) (players.Player, error) {
			return playerFunc{
				name: "blocking",
				play: func(ctx context.Context, _ wordhunt.Board, _ time.Time) (players.Result, error) {
					once.Do(func() { close(started) })
					<-ctx.Done()
					return players.Result{}, ctx.Err()
				},
			}, nil
		},
	})
	run, err := manager.Start(StartRequest{N: 20, Duration: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("player did not start")
	}
	run.Cancel()
	waitDone(t, run)
	if got := run.Snapshot().Status; got != StatusCancelled {
		t.Fatalf("status = %q, want cancelled", got)
	}
}

func TestStartOverCancelsOldRun(t *testing.T) {
	var first atomic.Bool
	first.Store(true)
	manager := NewManager(Config{
		MaxConcurrent: 2,
		MinDuration:   time.Millisecond,
		PlayerFactory: func(context.Context, GameSpec) (players.Player, error) {
			if first.Load() {
				return &testPlayer{block: true}, nil
			}
			return &testPlayer{}, nil
		},
	})
	old, err := manager.Start(StartRequest{N: 8, Duration: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return old.Snapshot().Stats.Running > 0 })
	first.Store(false)
	replacement, err := manager.Start(StartRequest{N: 3, Duration: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	waitDone(t, replacement)
	if old.Snapshot().Status != StatusCancelled {
		t.Fatalf("old status = %q", old.Snapshot().Status)
	}
	if replacement.Snapshot().Status != StatusFinished {
		t.Fatalf("replacement status = %q", replacement.Snapshot().Status)
	}
}

func TestDefaultSolverPublishesWordPathsAndLeaderboard(t *testing.T) {
	manager := NewManager(Config{
		MinDuration:  time.Millisecond,
		WordsPerGame: 6,
	})
	run, err := manager.Start(StartRequest{N: 2, Duration: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	waitDone(t, run)
	replay, events, unsubscribe, err := run.Subscribe(0)
	if err != nil {
		t.Fatal(err)
	}
	defer unsubscribe()
	if _, open := <-events; open {
		t.Fatal("finished run subscription should be closed")
	}
	var wordEvents int
	for _, event := range replay {
		if event.Type != "word" {
			continue
		}
		wordEvents++
		if event.Word == nil || len(event.Word.Path) != len(event.Word.Value) {
			t.Fatalf("word event has unusable path: %#v", event.Word)
		}
	}
	if wordEvents == 0 {
		t.Fatal("default solver produced no word events")
	}
	snapshot := run.Snapshot()
	if len(snapshot.Leaderboard) != 2 {
		t.Fatalf("leaderboard length = %d", len(snapshot.Leaderboard))
	}
	for _, entry := range snapshot.Leaderboard {
		if entry.Name == "" || entry.Backend != "solver-bot" || entry.Model != "trie-dfs · paced mock" {
			t.Fatalf("incomplete leaderboard entry: %#v", entry)
		}
	}
}

func TestHTTPStartSSESnapshotAndCancel(t *testing.T) {
	manager := NewManager(Config{
		MinDuration:  time.Millisecond,
		WordsPerGame: 3,
	})
	server := httptest.NewServer(manager)
	defer server.Close()

	response, err := http.Post(server.URL+"/api/arena/runs", "application/json", strings.NewReader(`{"count":2,"duration_ms":1000}`))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusAccepted {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("start status = %d: %s", response.StatusCode, body)
	}
	var started StartResponse
	if err := json.NewDecoder(response.Body).Decode(&started); err != nil {
		t.Fatal(err)
	}

	stream, err := http.Get(server.URL + started.EventsURL)
	if err != nil {
		t.Fatal(err)
	}
	streamBody, err := io.ReadAll(stream.Body)
	stream.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if stream.StatusCode != http.StatusOK {
		t.Fatalf("stream status = %d: %s", stream.StatusCode, streamBody)
	}
	if !bytes.Contains(streamBody, []byte("event: run_finished")) ||
		!bytes.Contains(streamBody, []byte("event: word")) ||
		!bytes.Contains(streamBody, []byte(`"path":[`)) {
		t.Fatalf("SSE is missing expected events:\n%s", streamBody)
	}

	snapshotResponse, err := http.Get(server.URL + "/arena/runs/" + started.RunID)
	if err != nil {
		t.Fatal(err)
	}
	defer snapshotResponse.Body.Close()
	if snapshotResponse.StatusCode != http.StatusOK {
		t.Fatalf("snapshot status = %d", snapshotResponse.StatusCode)
	}
	var snapshot Snapshot
	if err := json.NewDecoder(snapshotResponse.Body).Decode(&snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot.Status != StatusFinished || len(snapshot.Leaderboard) != 2 {
		t.Fatalf("unexpected snapshot: %#v", snapshot)
	}

	cancelRequest, _ := http.NewRequest(http.MethodDelete, server.URL+"/arena/runs/"+started.RunID, nil)
	cancelResponse, err := http.DefaultClient.Do(cancelRequest)
	if err != nil {
		t.Fatal(err)
	}
	cancelResponse.Body.Close()
	if cancelResponse.StatusCode != http.StatusAccepted {
		t.Fatalf("cancel status = %d", cancelResponse.StatusCode)
	}
}

func TestHTTPRejectsCapAndUnknownFields(t *testing.T) {
	server := httptest.NewServer(NewManager(Config{MaxGames: 4}))
	defer server.Close()
	for _, body := range []string{`{"n":5}`, `{"n":2,"surprise":true}`} {
		response, err := http.Post(server.URL+"/arena/start", "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != http.StatusBadRequest {
			t.Fatalf("body %s: status = %d, want 400", body, response.StatusCode)
		}
	}
}

func TestConcurrentSnapshotSubscribeAndCancel(t *testing.T) {
	manager := NewManager(Config{
		MaxConcurrent:    8,
		MinDuration:      time.Millisecond,
		MaxSubscribers:   128,
		SubscriberBuffer: 64,
		PlayerFactory: func(context.Context, GameSpec) (players.Player, error) {
			return &testPlayer{delay: 20 * time.Millisecond}, nil
		},
	})
	run, err := manager.Start(StartRequest{N: 100, Duration: time.Second})
	if err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	for range 32 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 20 {
				_ = run.Snapshot()
				_, events, unsubscribe, subscribeErr := run.Subscribe(0)
				if subscribeErr == nil {
					select {
					case <-events:
					default:
					}
					unsubscribe()
				}
			}
		}()
	}
	time.Sleep(10 * time.Millisecond)
	run.Cancel()
	wg.Wait()
	waitDone(t, run)
}

func TestSSELastEventID(t *testing.T) {
	manager := NewManager(Config{MinDuration: time.Millisecond, WordsPerGame: 2})
	run, err := manager.Start(StartRequest{N: 1, Duration: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	waitDone(t, run)
	server := httptest.NewServer(manager)
	defer server.Close()

	request, _ := http.NewRequest(http.MethodGet, server.URL+"/arena/runs/"+run.ID()+"/events", nil)
	request.Header.Set("Last-Event-ID", "1")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	scanner := bufio.NewScanner(response.Body)
	for scanner.Scan() {
		if strings.HasPrefix(scanner.Text(), "id: ") && scanner.Text() == "id: 1" {
			t.Fatal("Last-Event-ID event was replayed")
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
}

type playerFunc struct {
	name string
	play func(context.Context, wordhunt.Board, time.Time) (players.Result, error)
}

func (p playerFunc) Name() string { return p.name }

func (p playerFunc) Play(ctx context.Context, board wordhunt.Board, deadline time.Time) (players.Result, error) {
	return p.play(ctx, board, deadline)
}

func waitDone(t *testing.T, run *Run) {
	t.Helper()
	select {
	case <-run.Done():
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for run")
	}
}

func waitFor(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("condition was not met")
}
