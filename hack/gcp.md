# WordRust GCP setup and Cloud Run deployment

Project: `gen-lang-client-0189911611` (`wordrust-hack`)  
Region: `us-central1`

## Provisioned resources

- Billing is enabled on billing account `011799-DAA324-5464E4`.
- Enabled APIs:
  - `run.googleapis.com`
  - `artifactregistry.googleapis.com`
  - `cloudbuild.googleapis.com`
  - `aiplatform.googleapis.com`
  - `secretmanager.googleapis.com`
  - `iam.googleapis.com`
  - `logging.googleapis.com`
  - `monitoring.googleapis.com`
- Artifact Registry Docker repository:
  `us-central1-docker.pkg.dev/gen-lang-client-0189911611/wordrust`
- Runtime service account:
  `wordrust-run@gen-lang-client-0189911611.iam.gserviceaccount.com`
- The runtime account has only these project roles:
  - `roles/aiplatform.user`
  - `roles/secretmanager.secretAccessor`
  - `roles/logging.logWriter`
  - `roles/monitoring.metricWriter`
- Secret Manager secret `gemini-api-key` has an enabled version and grants the
  runtime service account `roles/secretmanager.secretAccessor` on the secret.
- Secret Manager secret `gateway-api-keys` contains three generated bearer
  keys. The runtime service account can access it, and a mode-`0600`,
  gitignored local copy is stored in `.env` as `GATEWAY_API_KEYS`.

The setup commands are:

```bash
export PROJECT_ID=gen-lang-client-0189911611
export REGION=us-central1
export RUNTIME_SA=wordrust-run
export RUNTIME_SA_EMAIL="${RUNTIME_SA}@${PROJECT_ID}.iam.gserviceaccount.com"

gcloud config set project "$PROJECT_ID"

gcloud services enable \
  run.googleapis.com \
  artifactregistry.googleapis.com \
  cloudbuild.googleapis.com \
  aiplatform.googleapis.com \
  secretmanager.googleapis.com \
  iam.googleapis.com \
  logging.googleapis.com \
  monitoring.googleapis.com

gcloud artifacts repositories create wordrust \
  --project="$PROJECT_ID" \
  --location="$REGION" \
  --repository-format=docker \
  --description="WordRust Hackathon container images"

gcloud iam service-accounts create "$RUNTIME_SA" \
  --project="$PROJECT_ID" \
  --display-name="WordRust Cloud Run" \
  --description="Runtime identity for the Word Hunt hackathon app"

for role in \
  roles/aiplatform.user \
  roles/secretmanager.secretAccessor \
  roles/logging.logWriter \
  roles/monitoring.metricWriter
do
  gcloud projects add-iam-policy-binding "$PROJECT_ID" \
    --member="serviceAccount:${RUNTIME_SA_EMAIL}" \
    --role="$role" \
    --condition=None
done

gcloud secrets create gemini-api-key \
  --project="$PROJECT_ID" \
  --replication-policy=automatic

# The value goes through stdin and is never printed.
test -n "${GEMINI_API_KEY:?GEMINI_API_KEY is not set}"
printf '%s' "$GEMINI_API_KEY" |
  gcloud secrets versions add gemini-api-key \
    --project="$PROJECT_ID" \
    --data-file=-

gcloud secrets add-iam-policy-binding gemini-api-key \
  --project="$PROJECT_ID" \
  --member="serviceAccount:${RUNTIME_SA_EMAIL}" \
  --role=roles/secretmanager.secretAccessor

gcloud secrets create gateway-api-keys \
  --project="$PROJECT_ID" \
  --replication-policy=automatic

# Generate three bearer keys, save a gitignored local copy, and stream the same
# comma-separated value to Secret Manager without printing it.
python3 - .env <<'PY' |
import os
import secrets
import sys

path = sys.argv[1]
value = ",".join(secrets.token_urlsafe(32) for _ in range(3))
lines = []
if os.path.exists(path):
    with open(path) as env_file:
        lines = [
            line.rstrip("\n")
            for line in env_file
            if not line.startswith(("GATEWAY_API_KEYS=", "export GATEWAY_API_KEYS="))
        ]
lines.append(f"GATEWAY_API_KEYS={value}")
with open(path, "w") as env_file:
    env_file.write("\n".join(lines) + "\n")
os.chmod(path, 0o600)
sys.stdout.write(value)
PY
  gcloud secrets versions add gateway-api-keys \
    --project="$PROJECT_ID" \
    --data-file=-

gcloud secrets add-iam-policy-binding gateway-api-keys \
  --project="$PROJECT_ID" \
  --member="serviceAccount:${RUNTIME_SA_EMAIL}" \
  --role=roles/secretmanager.secretAccessor
```

These are one-time creation commands. Use `describe` before rerunning a create
command, or omit that command when the resource already exists.

## Vertex AI smoke test

On 2026-10-01, `gemini-2.5-flash` returned HTTP 200 with the expected `OK`
response from the `us-central1` Vertex AI endpoint in **1,994 ms**.

The serving probe returned:

- `gemini-3.8-flash`: 404 in `us-central1`; 200 from Vertex `global`
- `gemini-3.5-flash-lite`: 404, not served or not accessible in `us-central1`
- `gemini-3.1-flash-lite`: 404, not served or not accessible in `us-central1`
- `gemini-2.5-flash`: 200, served in `us-central1`

Use `GOOGLE_CLOUD_LOCATION=global` with `gemini-3.8-flash` for the Vertex
player, fallback, and commentator. The Gemini API-key failover tier also uses
its verified `gemini-3.8-flash` model through the separate
`GEMINI_API_PLAYER_MODEL` and `GEMINI_API_FALLBACK_MODEL` variables.

The existing local Application Default Credentials (ADC) needed interactive
reauthentication, so that smoke used the same active `gcloud` user principal's
OAuth access token. Before an ADC-based local test, run:

```bash
gcloud auth application-default login
gcloud auth application-default set-quota-project gen-lang-client-0189911611
```

Then smoke-test with ADC:

```bash
export PROJECT_ID=gen-lang-client-0189911611
export REGION=us-central1
export MODEL_ID=gemini-2.5-flash
export ACCESS_TOKEN
ACCESS_TOKEN="$(gcloud auth application-default print-access-token)"

curl --fail-with-body --silent --show-error \
  -X POST \
  -H "Authorization: Bearer ${ACCESS_TOKEN}" \
  -H "Content-Type: application/json; charset=utf-8" \
  "https://${REGION}-aiplatform.googleapis.com/v1/projects/${PROJECT_ID}/locations/${REGION}/publishers/google/models/${MODEL_ID}:generateContent" \
  -d '{
    "contents": [{
      "role": "user",
      "parts": [{"text": "Reply with exactly: OK"}]
    }],
    "generationConfig": {
      "maxOutputTokens": 8,
      "temperature": 0
    }
  }'

unset ACCESS_TOKEN
```

## Exact Cloud Run deployment recipe

Run these commands from the repository root. The image contains only the Go
gateway; the Rust worker remains outside Cloud Run.

### 1. Build with Cloud Build

```bash
set -euo pipefail

export PROJECT_ID=gen-lang-client-0189911611
export REGION=us-central1
export REPOSITORY=wordrust
export SERVICE=wordrust
export TAG="$(git rev-parse --short=12 HEAD)"
export IMAGE="${REGION}-docker.pkg.dev/${PROJECT_ID}/${REPOSITORY}/${SERVICE}:${TAG}"

gcloud config set project "$PROJECT_ID"
gcloud builds submit \
  --project="$PROJECT_ID" \
  --tag="$IMAGE" \
  .
```

If Cloud Build reports that its build identity cannot push to Artifact
Registry, identify that identity in the error and grant it
`roles/artifactregistry.writer` on the `wordrust` repository. Do not grant that
role to the runtime service account.

### 2. Deploy

The gateway stores games in memory, so the load-test profile pins it to one
always-on instance. A concurrency of 1,000 leaves room for long-lived SSE
connections. Session affinity is defense in depth, not a substitute for the
single-instance cap.

```bash
gcloud run deploy "$SERVICE" \
  --project="$PROJECT_ID" \
  --region="$REGION" \
  --platform=managed \
  --image="$IMAGE" \
  --service-account="wordrust-run@${PROJECT_ID}.iam.gserviceaccount.com" \
  --set-secrets="GATEWAY_API_KEYS=gateway-api-keys:latest,GEMINI_API_KEY=gemini-api-key:latest" \
  --set-env-vars="HOST=0.0.0.0,PLAYERS=real,GOOGLE_GENAI_USE_VERTEXAI=true,GOOGLE_CLOUD_PROJECT=${PROJECT_ID},GOOGLE_CLOUD_LOCATION=global,GEMINI_PLAYER_MODEL=gemini-3.8-flash,GEMINI_FALLBACK_MODEL=gemini-3.8-flash,GEMINI_COMMENTATOR_MODEL=gemini-3.8-flash,GEMINI_API_PLAYER_MODEL=gemini-3.8-flash,GEMINI_API_FALLBACK_MODEL=gemini-3.8-flash,MAX_ACTIVE_GAMES=2000,ARENA_MAX_REAL_GAMES=12,ARENA_PROFILE_GEMINI_AGENT_PERCENT=10,ARENA_PROFILE_GEMINI_AGENT_MODEL=gemini-3.8-flash,ARENA_PROFILE_GEMMA_BASELINE_PERCENT=30,ARENA_PROFILE_GEMMA_BASELINE_MODEL=gemma-4-26b-a4b-it,ARENA_PROFILE_GEMMA_DIFFUSION_PERCENT=30,ARENA_PROFILE_GEMMA_DIFFUSION_MODEL=gemma-4-31b-it,ARENA_PROFILE_GEMMA_JEV_PERCENT=30,ARENA_PROFILE_GEMMA_JEV_MODEL=gemma-4-31b-it" \
  --port=8080 \
  --cpu=4 \
  --memory=2Gi \
  --timeout=300 \
  --min-instances=1 \
  --max-instances=1 \
  --concurrency=1000 \
  --no-cpu-throttling \
  --session-affinity \
  --allow-unauthenticated
```

Remove `--allow-unauthenticated` if the load generator uses authenticated
requests. No Cloud Run service was deployed during infrastructure setup.

### 3. Confirm the deployment

```bash
gcloud run services describe "$SERVICE" \
  --project="$PROJECT_ID" \
  --region="$REGION" \
  --format='yaml(status.url,status.latestReadyRevisionName,spec.template.metadata.annotations)'
```
