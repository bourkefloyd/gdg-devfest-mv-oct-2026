# Word Hunt Arena: run and deploy

The arena is stateless between process restarts: active games and leaderboards
live only in the Go gateway's memory. The default `mock` mode uses the embedded
dictionary and solver, so it needs no model weights or API keys.

## Local

```bash
# Builds the Vite app, then serves UI + API on one port.
make arena
# http://localhost:8787/
```

Or with Docker:

```bash
docker compose up --build
# http://localhost:8787/
```

Set `ARENA_PORT=9090` to change the host port. The arena caps each launch at
100 games and starting a new run cancels the previous run.

## Cloud Run

Do not deploy from the hackathon workstation until the demo branch is frozen.
When ready, build and deploy the same container:

```bash
PROJECT_ID="your-project"
REGION="us-central1"
IMAGE="${REGION}-docker.pkg.dev/${PROJECT_ID}/word-hunt/arena:$(git rev-parse --short HEAD)"

gcloud artifacts repositories describe word-hunt \
  --project "$PROJECT_ID" --location "$REGION" ||
gcloud artifacts repositories create word-hunt \
  --project "$PROJECT_ID" --location "$REGION" \
  --repository-format docker

gcloud builds submit \
  --project "$PROJECT_ID" \
  --config hack/cloudbuild-arena.yaml \
  --substitutions "_IMAGE=$IMAGE" .

gcloud run deploy word-hunt-arena \
  --project "$PROJECT_ID" \
  --region "$REGION" \
  --image "$IMAGE" \
  --allow-unauthenticated \
  --set-env-vars "HOST=0.0.0.0,PLAYERS=mock,GATEWAY_API_KEYS=arena-local,ARENA_STATIC_DIR=/app/web" \
  --concurrency 100 \
  --cpu 2 \
  --memory 512Mi \
  --max-instances 4
```

Cloud Run supplies `$PORT`. Each replica has its own in-memory run state, so
production multi-replica routing needs session affinity or an external run
store. For this stateless demo, one active browser session per replica is the
intended mode.
