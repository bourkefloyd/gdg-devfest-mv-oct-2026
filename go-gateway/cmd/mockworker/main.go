// Command mockworker runs a deterministic, single-concurrency gRPC inference
// worker for gateway integration and load tests.
package main

import (
	"log"
	"math/rand"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	pb "go-gateway/proto"

	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
)

type server struct {
	pb.UnimplementedInferenceServiceServer
	delay  time.Duration
	jitter time.Duration
	slots  chan struct{}
	words  string
}

func (s *server) StreamGenerate(req *pb.GenerateRequest, stream pb.InferenceService_StreamGenerateServer) error {
	select {
	case s.slots <- struct{}{}:
		defer func() { <-s.slots }()
	case <-stream.Context().Done():
		return stream.Context().Err()
	}

	delay := s.delay
	if s.jitter > 0 {
		delay += time.Duration(rand.Int63n(int64(2*s.jitter)+1)) - s.jitter
	}
	if delay > 0 {
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-stream.Context().Done():
			return stream.Context().Err()
		}
	}

	maxTokens := int(req.MaxTokens)
	if maxTokens <= 0 {
		maxTokens = 128
	}
	tokens := strings.Fields(s.words)
	if len(tokens) > maxTokens {
		tokens = tokens[:maxTokens]
	}
	for i, token := range tokens {
		if i < len(tokens)-1 {
			token += " "
		}
		if err := stream.Send(&pb.GenerateResponse{Token: token}); err != nil {
			return err
		}
	}
	return stream.Send(&pb.GenerateResponse{IsFinal: true})
}

func durationEnv(name string, fallback time.Duration) time.Duration {
	value := os.Getenv(name)
	if value == "" {
		return fallback
	}
	d, err := time.ParseDuration(value)
	if err != nil {
		log.Fatalf("invalid %s=%q: %v", name, value, err)
	}
	return d
}

func intEnv(name string, fallback int) int {
	value := os.Getenv(name)
	if value == "" {
		return fallback
	}
	n, err := strconv.Atoi(value)
	if err != nil || n < 1 {
		log.Fatalf("invalid %s=%q", name, value)
	}
	return n
}

func main() {
	addr := os.Getenv("MOCK_WORKER_ADDR")
	if addr == "" {
		addr = "127.0.0.1:50061"
	}
	words := os.Getenv("MOCK_WORKER_RESPONSE")
	if words == "" {
		words = "WORDS: cat, tone, stone, rate"
	}

	listener, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatal(err)
	}
	svc := &server{
		delay:  durationEnv("MOCK_WORKER_DELAY", 2*time.Second),
		jitter: durationEnv("MOCK_WORKER_JITTER", 500*time.Millisecond),
		slots:  make(chan struct{}, intEnv("MOCK_WORKER_CONCURRENCY", 1)),
		words:  words,
	}
	grpcServer := grpc.NewServer()
	pb.RegisterInferenceServiceServer(grpcServer, svc)
	healthServer := health.NewServer()
	healthServer.SetServingStatus("", healthpb.HealthCheckResponse_SERVING)
	healthpb.RegisterHealthServer(grpcServer, healthServer)
	log.Printf("mock worker listening on %s (delay=%s jitter=%s concurrency=%d)",
		addr, svc.delay, svc.jitter, cap(svc.slots))
	log.Fatal(grpcServer.Serve(listener))
}
