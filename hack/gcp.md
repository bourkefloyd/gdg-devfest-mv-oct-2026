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
```

These are one-time creation commands. Use `describe` before rerunning a create
command, or omit that command when the resource already exists.

## Vertex AI smoke test

On 2026-10-01, `gemini-2.5-flash` returned HTTP 200 with the expected `OK`
response from the `us-central1` Vertex AI endpoint in **1,994 ms**.

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

Do not run this until the application has a production `Dockerfile` whose
server listens on `0.0.0.0:$PORT`. Run these commands from the repository root.

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

The load-test profile keeps one warm instance, caps scale-out at 20 instances,
and permits 40 concurrent requests per instance.

```bash
gcloud run deploy "$SERVICE" \
  --project="$PROJECT_ID" \
  --region="$REGION" \
  --platform=managed \
  --image="$IMAGE" \
  --service-account="wordrust-run@${PROJECT_ID}.iam.gserviceaccount.com" \
  --set-secrets="GEMINI_API_KEY=gemini-api-key:latest" \
  --set-env-vars="GOOGLE_CLOUD_PROJECT=${PROJECT_ID},GOOGLE_CLOUD_LOCATION=${REGION},GOOGLE_GENAI_USE_VERTEXAI=true" \
  --port=8080 \
  --cpu=2 \
  --memory=2Gi \
  --timeout=300 \
  --min-instances=1 \
  --max-instances=20 \
  --concurrency=40 \
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
