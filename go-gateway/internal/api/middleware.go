package api

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

type ctxKey int

const keyIDCtx ctxKey = iota

// keyring stores only SHA-256 digests of the configured API keys.
type keyring struct{ digests [][32]byte }

func newKeyring(keys []string) *keyring {
	k := &keyring{}
	for _, raw := range keys {
		if raw = strings.TrimSpace(raw); raw != "" {
			k.digests = append(k.digests, sha256.Sum256([]byte(raw)))
		}
	}
	return k
}

// lookup returns a short non-reversible key ID, comparing against every
// digest in constant time so timing doesn't reveal which key matched.
func (k *keyring) lookup(presented string) (string, bool) {
	d := sha256.Sum256([]byte(presented))
	match := 0
	var id [32]byte
	for _, want := range k.digests {
		eq := subtle.ConstantTimeCompare(d[:], want[:])
		match |= eq
		if eq == 1 {
			id = want
		}
	}
	return hex.EncodeToString(id[:6]), match == 1 && presented != ""
}

func keyID(ctx context.Context) string {
	id, _ := ctx.Value(keyIDCtx).(string)
	return id
}

func bearer(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if len(h) > 7 && strings.EqualFold(h[:7], "bearer ") {
		return strings.TrimSpace(h[7:])
	}
	return ""
}

func (s *Server) requireKey(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, ok := s.keys.lookup(bearer(r))
		if !ok {
			authFailures.Inc()
			w.Header().Set("WWW-Authenticate", `Bearer realm="wordhunt"`)
			writeError(w, http.StatusUnauthorized, "unauthorized", "missing or invalid API key")
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), keyIDCtx, id)))
	})
}

// streamAuth accepts a bearer key, or the game's stream token in ?token=.
func (s *Server) streamAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if id, ok := s.keys.lookup(bearer(r)); ok {
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), keyIDCtx, id)))
			return
		}
		if g := s.store.get(r.PathValue("id")); g != nil && g.checkStreamToken(r.URL.Query().Get("token")) {
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), keyIDCtx, g.KeyID)))
			return
		}
		authFailures.Inc()
		writeError(w, http.StatusUnauthorized, "unauthorized", "missing or invalid API key or stream token")
	})
}

const (
	bucketCreate = "create"
	bucketWord   = "word"
	bucketRead   = "read"
)

type limiters struct {
	mu     sync.Mutex
	limits map[string]rate.Limit
	bursts map[string]int
	byKey  map[string]*rate.Limiter // bucket + "/" + keyID
}

func newLimiters(c Config) *limiters {
	return &limiters{
		limits: map[string]rate.Limit{bucketCreate: c.CreateRate, bucketWord: c.WordRate, bucketRead: c.ReadRate},
		bursts: map[string]int{bucketCreate: c.CreateBurst, bucketWord: c.WordBurst, bucketRead: c.ReadBurst},
		byKey:  map[string]*rate.Limiter{},
	}
}

func (l *limiters) get(bucket, key string) *rate.Limiter {
	l.mu.Lock()
	defer l.mu.Unlock()
	k := bucket + "/" + key
	lim, ok := l.byKey[k]
	if !ok {
		lim = rate.NewLimiter(l.limits[bucket], l.bursts[bucket])
		l.byKey[k] = lim
	}
	return lim
}

func (s *Server) rateLimit(bucket string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lim := s.limits.get(bucket, keyID(r.Context()))
		res := lim.Reserve()
		if d := res.Delay(); d > 0 {
			res.Cancel()
			rateLimited.WithLabelValues(bucket).Inc()
			w.Header().Set("Retry-After", strconv.Itoa(int(d/time.Second)+1))
			writeError(w, http.StatusTooManyRequests, "rate_limited", "rate limit exceeded for "+bucket)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) cors(next http.Handler) http.Handler {
	allowed := map[string]bool{}
	for _, o := range s.cfg.AllowedOrigins {
		if o = strings.TrimRight(strings.TrimSpace(o), "/"); o != "" {
			allowed[o] = true
		}
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" {
			w.Header().Add("Vary", "Origin")
			if !allowed[origin] {
				if r.Method == http.MethodOptions {
					writeError(w, http.StatusForbidden, "origin_not_allowed", "origin not allowed")
					return
				}
			} else {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Access-Control-Expose-Headers", "Retry-After")
				if r.Method == http.MethodOptions {
					w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
					w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
					w.Header().Set("Access-Control-Max-Age", "600")
					w.WriteHeader(http.StatusNoContent)
					return
				}
			}
		}
		next.ServeHTTP(w, r)
	})
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}

func recoverer(log *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if v := recover(); v != nil {
				if v == http.ErrAbortHandler {
					panic(v)
				}
				log.Error("panic", "route", r.Pattern, "err", v)
				writeError(w, http.StatusInternalServerError, "internal", "internal error")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	if s.status == 0 {
		s.status = code
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Unwrap() http.ResponseWriter { return s.ResponseWriter }

// instrument records metrics and an access log line. Request bodies, prompts
// and API keys are never logged.
func instrument(route string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w}
		next.ServeHTTP(rec, r)
		if rec.status == 0 {
			rec.status = http.StatusOK
		}
		d := time.Since(start)
		httpRequests.WithLabelValues(route, strconv.Itoa(rec.status)).Inc()
		httpDuration.WithLabelValues(route).Observe(d.Seconds())
		if rec.status >= 500 {
			slog.Warn("http", "route", route, "status", rec.status, "dur_ms", d.Milliseconds())
		}
	})
}
