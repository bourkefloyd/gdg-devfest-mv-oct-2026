# wordrust-2

Separate Cloud Run service for the Word Hunt arena. The live service `wordrust` is not updated by this deploy.

## Image and revision

- Image: `us-central1-docker.pkg.dev/gen-lang-client-0189911611/wordrust/wordrust-2:56fdc3a9dbe0`
- Service: `wordrust-2` in `gen-lang-client-0189911611` / `us-central1`
- URL: https://wordrust-2-zah6ccgmva-uc.a.run.app
- Revision: `wordrust-2-00003-gzc`
- Runtime service account: `wordrust-run@gen-lang-client-0189911611.iam.gserviceaccount.com`
- Secrets, by reference only: `GATEWAY_API_KEYS=gateway-api-keys:latest` and `GEMINI_API_KEY=gemini-api-key:latest`

Shape matches the live gateway: 4 vCPU, 2Gi, no CPU throttling, startup CPU boost, min=max=1, concurrency 1000, session affinity, unauthenticated Cloud Run with the gateway's own bearer keys. `ARENA_GEMINI_MAX_TURNS=1`.

## App Hub

Regional application `wordrust-2` in `us-central1`, service `wordrust-2-service`.

- Service resource: `projects/gen-lang-client-0189911611/locations/us-central1/applications/wordrust-2/services/wordrust-2-service`
- Discovered service: `apphub-00000000-0000-0000-3313-e09fceec0e93` (`//run.googleapis.com/projects/958584846348/locations/us-central1/services/wordrust-2`)
- Display name: WordRust 2 Cloud Run Gateway
- Environment: PRODUCTION. Criticality: MISSION_CRITICAL
- Console: https://console.cloud.google.com/apphub/applications/us-central1/wordrust-2?project=gen-lang-client-0189911611
- Design Center application (space `default-space`): https://console.cloud.google.com/products/design-center/application/us-central1/default-space/wordrust-2?project=gen-lang-client-0189911611

## Smoke

`GET /api/health` returned `{"status":"ok"}`.

`POST /api/arena/runs` with `{"count":4,"duration_s":10}` finished run `1bcaf559bbd80d1a`: 4 seats, 3 scored, 1 Gemini seat scored 0 after 4 retries.

| Seat | Profile | Score | Words |
|---|---|---|---|
| Rocket Yak 02 | Gemini 3.8 Flash | 2400 | 9 |
| Clever Panda 04 | Gemma 4 26B A4B | 1200 | 6 |
| Fuzzy Falcon 03 | Gemma 4 26B A4B | 900 | 9 |
| Quirky Koala 01 | Gemini 3.8 Flash | 0 | 0 |
