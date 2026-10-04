# Media Intake

A Go service accepts captures, saves the frame on a volume, commits capture and outbox rows in PostgreSQL, then delivers to a stub downstream outside the client request.

## Run and inspect

Requires Docker 24+ with Compose v2. Migrations run automatically.

```sh
# run service (one command)
docker compose up -d
# call requests
curl --fail http://localhost:8080/readyz
printf 'frame bytes' >/tmp/media-frame.jpg
send() { curl -sS -H "Idempotency-Key: $1" -F 'manifest={"capturedAt":"2026-10-03T12:00:00Z","amount":"12.34","currency":"PEN"}' -F 'frame=@/tmp/media-frame.jpg;type=image/jpeg' http://localhost:8080/v1/captures; }
send demo-1
send demo-1

# set key
key="race-$(date +%s)"
send "$key" & send "$key" & wait

curl --fail http://localhost:8080/v1/captures/demo-1
docker compose logs app downstream
docker compose exec -T db psql -U media_intake -d media_intake -c 'SELECT id,status,attempt_count,next_attempt_at FROM outbox ORDER BY created_at;'
```

A new key should return `201`; repeats return `200` with the original capture. Watch the outbox change from `pending` through `retry` to `delivered` or `dead`. The worker sends metadata and original frame bytes with the stable outbox UUID as its delivery key; downstream must deduplicate retries. `STUB_STATUS=503 docker compose up -d downstream` exercises retries. `docker compose restart app` exercises recovery.

## Schema and guarantees

`capture.idempotency_key` is unique: under `READ COMMITTED`, an insert elects one winner and a fresh query returns that winner to the loser. Different keys remain distinct. `NUMERIC(20,2)` preserves decimal money; checks constrain amount and frame size. `outbox.capture_id` is unique, and state checks guard `pending`, `retry`, `delivered`, and `dead`. A partial index finds due work. Capture and outbox commit together. The frame volume is separate, so a crash or uncertain commit may leave an orphan; possibly committed frames are preserved.

Workers hold `FOR UPDATE SKIP LOCKED` through bounded HTTP and state update. Delivery is at least once, with exponential jittered retry and 8 attempts. Dead events need guarded manual re-drive. Holding a database connection during HTTP favors simple concurrency safety over throughput.

## Tests and dependencies

PostgreSQL integration tests need the Compose database. Unit tests run in the same command; without `TEST_DATABASE_URL`, integration tests skip.

```sh
docker compose up -d db
export DATABASE_URL='postgres://media_intake:media_intake_dev@localhost:5432/media_intake?sslmode=disable'
export TEST_DATABASE_URL="$DATABASE_URL"
export CAPTURE_STORAGE_DIR="$(mktemp -d)"
go test ./...
```

Built with Go 1.27.1 and PostgreSQL 17. Directly used packages: chi/v5 5.3.2 (routing), pgx/v5 5.11.0 (SQL/pool), Tern/v2 2.4.3 (migrations), and shopspring/decimal 1.4.0 (exact amounts).

## Public error codes

Responses include `message`, `code`, `errorClass`, and `httpCode`; the code prefix matches the HTTP status.

| Code | Class | Code | Class |
| --- | --- | --- | --- |
| 400001 | IdempotencyKeyInvalid | 400002 | MultipartInvalid |
| 404001 | CaptureNotFound | 404002 | NotFound |
| 405001 | MethodNotAllowed | 413001 | FrameTooLarge |
| 413002 | ManifestTooLarge | 413003 | RequestBodyTooLarge |
| 422003 | ManifestInvalid | 422004 | FrameInvalid |
| 500001 | InternalError | 503001 | ServiceUnavailable |
| 503002 | CaptureStorageUnavailable | | |

## Known gaps

HTTP requests have JSON logs with request IDs, but capture-service warnings use the default logger without that ID. Manifest and frame contents are not logged. The stub's deduplication is in memory. Guarded re-drive, combined in-flight signal tests, and clean-clone verification remain untested. Optional gRPC, broker, S3, and OpenAPI extensions were cut.
