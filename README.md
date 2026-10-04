# Media Intake

It accepts captures, saves the frame on a volume, commits capture and outbox rows in PostgreSQL, then delivers to a stub downstream outside the client request.

## Run and inspect

Requires Docker 24+ & Compose v2. Migrations run automatically.

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
docker compose exec -T db psql -U media_intake -d media_intake -c 'SELECT o.id,o.status,o.attempt_count,o.next_attempt_at,o.last_error,c.frame_path FROM outbox o JOIN capture c ON c.id=o.capture_id ORDER BY o.created_at;'
```

A new key should return `201`; repeats return `200` with the original capture. Watch the outbox change from `pending` through `retry` to `delivered` or `dead`. The worker sends metadata and original frame bytes with the stable outbox UUID as its delivery key; downstream must deduplicate retries. `STUB_STATUS=503 docker compose up -d downstream` exercises retries. `docker compose restart app` exercises recovery.

## Schema and guarantees

`capture.idempotency_key` is unique: under `READ COMMITTED`, an insert elects one winner and a fresh query returns that winner to the loser. Different keys remain distinct. `NUMERIC(20,2)` preserves decimal money; checks constrain amount and frame size. `outbox.capture_id` is unique, and state checks guard `pending`, `retry`, `delivered`, and `dead`. A partial index finds due work. Capture and outbox commit together. The frame volume is separate, so a crash or uncertain commit may leave an orphan; possibly committed frames are preserved.

Workers hold `FOR UPDATE SKIP LOCKED` through bounded HTTP and state update. Delivery is at least once, with exponential jittered retry and 8 attempts. Holding a database connection during HTTP favors simple concurrency safety over throughput.

To re-drive a `dead` event, inspect `last_error` and the capture's `frame_path`; fix the cause first. In `psql`, replace the UUID and run:

```sql
UPDATE outbox SET status = 'pending', attempt_count = 0,
    next_attempt_at = now(), last_error = NULL
WHERE id = 'REPLACE_WITH_OUTBOX_UUID'::uuid AND status = 'dead'
RETURNING id, status;
```

Expect one returned row. This keeps the delivery key stable for downstream deduplication.

## Tests and dependencies

PostgreSQL integration tests need the Compose database. Unit tests run in the same command; without `TEST_DATABASE_URL`, integration tests skip.

```sh
docker compose up -d db
export DATABASE_URL='postgres://media_intake:media_intake_dev@localhost:5432/media_intake?sslmode=disable'
export TEST_DATABASE_URL="$DATABASE_URL"
export CAPTURE_STORAGE_DIR="$(mktemp -d)"
go test ./...
```

Built with Go 1.27.1 and PostgreSQL 17. Used packages: chi/v5 5.3.2 (routing), pgx/v5 5.11.0 (SQL/pool), Tern/v2 2.4.3 (migrations), and shopspring/decimal 1.4.0 (exact amounts).

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

HTTP requests have JSON logs with request IDs, but capture-service warnings use the default logger without that ID.
Manifest and frame contents are not logged.
The stub's deduplication is in memory.
The documented guarded re-drive procedure remains untested end to end; combined in-flight signal tests and clean-clone verification are also missing. 
Optional gRPC, broker, S3, and OpenAPI extensions were cut.
