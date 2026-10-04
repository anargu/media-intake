package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/anargu/media-intake/internal/apierror"
)

func TestCapturePostOutboxFailureRollsBackAndCleansFrame(t *testing.T) {
	pool, handler, storageRoot := newCaptureIntegrationHandler(t)
	idempotencyKey := integrationKey("outbox-failure")
	cleanupCaptureByKey(t, pool, idempotencyKey)
	t.Cleanup(func() { cleanupCaptureByKey(t, pool, idempotencyKey) })

	triggerName := "fail_capture_outbox_" + strconv.FormatInt(time.Now().UnixNano(), 10)
	functionName := triggerName + "_fn"
	functionSQL := fmt.Sprintf(`
		CREATE FUNCTION public.%s() RETURNS trigger LANGUAGE plpgsql AS $function$
		BEGIN
			IF EXISTS (
				SELECT 1 FROM public.capture
				WHERE id = NEW.capture_id AND idempotency_key = '%s'
			) THEN
				RAISE EXCEPTION 'injected outbox failure for test';
			END IF;
			RETURN NEW;
		END;
		$function$`, functionName, idempotencyKey)
	if _, err := pool.Exec(context.Background(), functionSQL); err != nil {
		t.Fatalf("create outbox failure function: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = pool.Exec(ctx, fmt.Sprintf("DROP TRIGGER IF EXISTS %s ON public.outbox", triggerName))
		_, _ = pool.Exec(ctx, fmt.Sprintf("DROP FUNCTION IF EXISTS public.%s()", functionName))
	})

	triggerSQL := fmt.Sprintf(
		"CREATE TRIGGER %s BEFORE INSERT ON public.outbox FOR EACH ROW EXECUTE FUNCTION public.%s()",
		triggerName,
		functionName,
	)
	if _, err := pool.Exec(context.Background(), triggerSQL); err != nil {
		t.Fatalf("create outbox failure trigger: %v", err)
	}

	request := newCapturePostRequest(t, idempotencyKey, "12.34")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d; body = %s",
			response.Code,
			http.StatusInternalServerError,
			response.Body.String())
	}

	var got apierror.PublicError
	if err := json.NewDecoder(response.Body).Decode(&got); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	if got != apierror.InternalError {
		t.Errorf("error = %#v, want %#v", got, apierror.InternalError)
	}

	var captureCount int
	if err := pool.QueryRow(context.Background(), `
		SELECT count(*) FROM capture WHERE idempotency_key = $1`, idempotencyKey,
	).Scan(&captureCount); err != nil {
		t.Fatalf("count captures after rollback: %v", err)
	}
	if captureCount != 0 {
		t.Errorf("capture rows after rollback = %d, want 0", captureCount)
	}

	rootEntries, err := os.ReadDir(storageRoot)
	if err != nil {
		t.Fatalf("read storage root: %v", err)
	}
	for _, entry := range rootEntries {
		if entry.Name() != ".staging" {
			t.Errorf("unexpected published artifact after rollback: %q", entry.Name())
		}
	}
	stagingEntries, err := os.ReadDir(filepath.Join(storageRoot, ".staging"))
	if err != nil {
		t.Fatalf("read staging directory: %v", err)
	}
	if len(stagingEntries) != 0 {
		t.Errorf("staging directory after rollback contains %d entries, want none", len(stagingEntries))
	}
}
