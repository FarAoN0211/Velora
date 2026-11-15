package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/robfig/cron/v3"

	"velora/internal/db"
)

func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	base := os.Getenv("VELORA_TEST_DATABASE_URL")
	if base == "" {
		t.Skip("VELORA_TEST_DATABASE_URL no definida")
	}
	ctx := context.Background()
	admin, err := pgx.Connect(ctx, base)
	if err != nil {
		t.Fatal(err)
	}
	name := fmt.Sprintf("velora_jobs_%d", time.Now().UnixNano())
	if _, err := admin.Exec(ctx, "create database "+pgx.Identifier{name}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(base)
	u.Path = "/" + name
	t.Cleanup(func() {
		_, _ = admin.Exec(context.Background(), "drop database if exists "+pgx.Identifier{name}.Sanitize()+" with (force)")
		admin.Close(context.Background())
	})
	if err := db.Migrate(ctx, u.String()); err != nil {
		t.Fatal(err)
	}
	pool, err := db.Open(ctx, u.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func TestQueueDedupeAndWorker(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	q := NewQueue(pool)
	id, err := q.Enqueue(ctx, "eco", map[string]int{"n": 7}, "eco:1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := q.Enqueue(ctx, "eco", nil, "eco:1"); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("esperaba ErrDuplicate, obtuve %v", err)
	}
	calls := 0
	w := NewWorker(pool, map[string]Handler{"eco": func(_ context.Context, p json.RawMessage) (any, error) {
		calls++
		var in map[string]int
		_ = json.Unmarshal(p, &in)
		return map[string]int{"doble": in["n"] * 2}, nil
	}})
	ran, err := w.runOne(ctx, []string{}, true)
	if err != nil || !ran || calls != 1 {
		t.Fatalf("runOne: ran=%v calls=%d err=%v", ran, calls, err)
	}
	jobs, err := q.Recent(ctx, "eco", 1)
	if err != nil || len(jobs) != 1 || jobs[0].ID != id || jobs[0].State != "done" || string(jobs[0].Result) != `{"doble": 14}` {
		t.Fatalf("estado del trabajo: %+v %v", jobs, err)
	}
	// Terminado el trabajo, la misma clave vuelve a poder encolarse.
	if _, err := q.Enqueue(ctx, "eco", nil, "eco:1"); err != nil {
		t.Fatalf("tras terminar, la clave debería quedar libre: %v", err)
	}
}

func TestWorkerRetriesThenFails(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	q := NewQueue(pool)
	if _, err := q.Enqueue(ctx, "falla", nil, ""); err != nil {
		t.Fatal(err)
	}
	w := NewWorker(pool, map[string]Handler{"falla": func(context.Context, json.RawMessage) (any, error) {
		return nil, errors.New("fallo de prueba")
	}})
	for i := 0; i < 3; i++ {
		// Adelanta el reintento para no esperar la espera exponencial.
		if _, err := pool.Exec(ctx, `update jobs set run_after = now() where kind = 'falla'`); err != nil {
			t.Fatal(err)
		}
		if ran, err := w.runOne(ctx, []string{}, true); err != nil || !ran {
			t.Fatalf("intento %d: ran=%v err=%v", i+1, ran, err)
		}
	}
	jobs, _ := q.Recent(ctx, "falla", 1)
	if jobs[0].State != "failed" || jobs[0].Attempts != 3 || jobs[0].LastError == nil {
		t.Fatalf("tras 3 intentos debería quedar 'failed': %+v", jobs[0])
	}
}

func TestSchedulerEnqueuesDueTask(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	q := NewQueue(pool)
	if _, err := pool.Exec(ctx, `update scheduled_tasks set cron = '* * * * *' where id = 'backup'`); err != nil {
		t.Fatal(err)
	}
	s := NewScheduler(pool, q, time.UTC)
	parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
	now := time.Now().UTC()
	if err := s.tick(ctx, parser, now); err != nil {
		t.Fatal(err)
	}
	if err := s.tick(ctx, parser, now); err != nil { // la segunda pasada en el mismo minuto no duplica
		t.Fatal(err)
	}
	jobs, _ := q.Recent(ctx, "backup", 10)
	if len(jobs) != 1 {
		t.Fatalf("esperaba 1 copia encolada, hay %d", len(jobs))
	}
}

// Un manejador colgado (ignora la cancelación, como una lectura de un montaje FUSE caído) no detiene la cola.
func TestWorkerAbandonsHungJob(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	q := NewQueue(pool)
	if _, err := q.Enqueue(ctx, "colgado", nil, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := q.Enqueue(ctx, "eco", nil, ""); err != nil {
		t.Fatal(err)
	}
	release := make(chan struct{})
	defer close(release)
	old := abandonGrace
	abandonGrace = 100 * time.Millisecond
	defer func() { abandonGrace = old }()
	w := NewWorker(pool, map[string]Handler{
		"colgado": func(context.Context, json.RawMessage) (any, error) { <-release; return nil, nil },
		"eco":     func(context.Context, json.RawMessage) (any, error) { return "ok", nil },
	})
	w.Timeouts = map[string]time.Duration{"colgado": 200 * time.Millisecond}
	for i := 0; i < 2; i++ {
		if _, err := w.runOne(ctx, []string{}, true); err != nil {
			t.Fatal(err)
		}
	}
	var hung, echo string
	var lastErr *string
	_ = pool.QueryRow(ctx, `select state, last_error from jobs where kind = 'colgado'`).Scan(&hung, &lastErr)
	_ = pool.QueryRow(ctx, `select state from jobs where kind = 'eco'`).Scan(&echo)
	if hung == "running" || lastErr == nil || echo != "done" {
		t.Fatalf("colgado=%s (%v), eco=%s", hung, lastErr, echo)
	}
}

// Los escaneos van por su propio carril: uno colgado no retrasa los demás trabajos (copias, mantenimiento).
func TestSeparateLaneKeepsQueueMoving(t *testing.T) {
	pool := testPool(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	q := NewQueue(pool)
	release := make(chan struct{})
	defer close(release)
	w := NewWorker(pool, map[string]Handler{
		"escaneo": func(context.Context, json.RawMessage) (any, error) { <-release; return nil, nil },
		"eco":     func(context.Context, json.RawMessage) (any, error) { return "ok", nil },
	})
	w.poll = 50 * time.Millisecond
	_, _ = q.Enqueue(ctx, "escaneo", nil, "")
	_, _ = q.Enqueue(ctx, "eco", nil, "")
	go func() { _ = w.Run(ctx, "escaneo") }()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var state string
		_ = pool.QueryRow(ctx, `select state from jobs where kind = 'eco'`).Scan(&state)
		if state == "done" {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("el trabajo normal esperó al escaneo colgado")
}

// R1 (revisión de código): un intento antiguo no puede cerrar un trabajo que ya lleva otro intento.
func TestStaleAttemptCannotOverwriteNewOne(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	q := NewQueue(pool)
	id, err := q.Enqueue(ctx, "lento", nil, "")
	if err != nil {
		t.Fatal(err)
	}
	started, release := make(chan struct{}), make(chan struct{})
	w := NewWorker(pool, map[string]Handler{
		"lento": func(context.Context, json.RawMessage) (any, error) { close(started); <-release; return "viejo", nil },
	})
	done := make(chan struct{})
	go func() { _, _ = w.runOne(ctx, []string{}, true); close(done) }()
	<-started
	// Mientras tanto, otro intento se ha quedado con el trabajo (p. ej. tras recuperarlo de una caída).
	if _, err := pool.Exec(ctx, `update jobs set locked_by = 'otro-intento' where id = $1`, id); err != nil {
		t.Fatal(err)
	}
	close(release)
	<-done
	var state, lockedBy string
	_ = pool.QueryRow(ctx, `select state, coalesce(locked_by, '') from jobs where id = $1`, id).Scan(&state, &lockedBy)
	if state != "running" || lockedBy != "otro-intento" {
		t.Fatalf("el intento antiguo sobrescribió al nuevo: estado=%s locked_by=%s", state, lockedBy)
	}
}

// El progreso que publica un trabajo en curso se guarda y se lee con LatestByPayload (la barra del panel); fuera de un
// trabajo, ReportProgress no hace nada.
func TestReportProgress(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	q := NewQueue(pool)
	ReportProgress(ctx, map[string]int{"done": 1}) // sin trabajo: no falla ni escribe
	if _, err := q.Enqueue(ctx, "escaneo", map[string]string{"library_id": "a"}, ""); err != nil {
		t.Fatal(err)
	}
	var seen json.RawMessage
	w := NewWorker(pool, map[string]Handler{"escaneo": func(hctx context.Context, _ json.RawMessage) (any, error) {
		ReportProgress(hctx, map[string]any{"phase": "analyzing", "done": 3, "total": 10})
		latest, err := q.LatestByPayload(hctx, "escaneo", "library_id")
		if err != nil {
			return nil, err
		}
		if latest["a"].State != "running" {
			return nil, fmt.Errorf("estado durante el trabajo: %s", latest["a"].State)
		}
		seen = latest["a"].Progress
		return "ok", nil
	}})
	if ran, err := w.runOne(ctx, []string{}, true); !ran || err != nil {
		t.Fatalf("runOne: %v %v", ran, err)
	}
	var p map[string]any
	if err := json.Unmarshal(seen, &p); err != nil || p["phase"] != "analyzing" || p["done"] != float64(3) || p["total"] != float64(10) {
		t.Fatalf("progreso leído durante el trabajo: %s", seen)
	}
	// Un segundo escaneo de la misma biblioteca y otro de otra: LatestByPayload da el último de cada una.
	id2, _ := q.Enqueue(ctx, "escaneo", map[string]string{"library_id": "a"}, "")
	_, _ = q.Enqueue(ctx, "escaneo", map[string]string{"library_id": "b"}, "")
	latest, err := q.LatestByPayload(ctx, "escaneo", "library_id")
	if err != nil || len(latest) != 2 || latest["a"].ID != id2 || latest["a"].State != "queued" || latest["b"].State != "queued" {
		t.Fatalf("últimos por biblioteca: %+v %v", latest, err)
	}
}

// Un trabajo que queda «en ejecución» sin nadie que lo lleve vuelve a la cola pasado su plazo, y al pararse el worker el
// trabajo en curso vuelve a la cola al momento, sin contar como intento.
func TestOrphanedJobsReturnToQueue(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	q := NewQueue(pool)
	old, _ := q.Enqueue(ctx, "largo", nil, "")
	recent, _ := q.Enqueue(ctx, "largo", nil, "")
	short, _ := q.Enqueue(ctx, "corto", nil, "")
	if _, err := pool.Exec(ctx, `update jobs set state = 'running', locked_by = 'muerto:1', locked_at = now() - interval '130 minutes' where id = $1`, old); err != nil {
		t.Fatal(err)
	}
	_, _ = pool.Exec(ctx, `update jobs set state = 'running', locked_by = 'vivo:1', locked_at = now() - interval '90 minutes' where id = $1`, recent)
	_, _ = pool.Exec(ctx, `update jobs set state = 'running', locked_by = 'muerto:2', locked_at = now() - interval '90 minutes' where id = $1`, short)
	w := NewWorker(pool, map[string]Handler{})
	w.Timeouts = map[string]time.Duration{"largo": 2 * time.Hour}
	w.recoverStale(ctx)
	state := func(id int64) string {
		var s string
		_ = pool.QueryRow(ctx, `select state from jobs where id = $1`, id).Scan(&s)
		return s
	}
	if state(old) != "queued" || state(recent) != "running" || state(short) != "queued" {
		t.Fatalf("recuperación por plazo: %s %s %s", state(old), state(recent), state(short))
	}
	_, _ = pool.Exec(ctx, `delete from jobs`)

	// Parada del worker con un trabajo en curso.
	id, _ := q.Enqueue(ctx, "espera", nil, "")
	startedCh := make(chan struct{})
	w2 := NewWorker(pool, map[string]Handler{"espera": func(c context.Context, _ json.RawMessage) (any, error) {
		close(startedCh)
		<-c.Done()
		return nil, c.Err()
	}})
	w2.poll = 20 * time.Millisecond
	wctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	// En su propio carril, como la detección de intros: es el caso que quedaba huérfano.
	go func() { _ = w2.Run(wctx, "espera"); close(done) }()
	select {
	case <-startedCh:
	case <-time.After(10 * time.Second):
		t.Fatal("el trabajo no arrancó")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("el worker no se detuvo")
	}
	var st string
	var attempts int
	var locked *string
	_ = pool.QueryRow(ctx, `select state, attempts, locked_by from jobs where id = $1`, id).Scan(&st, &attempts, &locked)
	if st != "queued" || attempts != 0 || locked != nil {
		t.Fatalf("tras parar el worker: %s, intentos %d, %v", st, attempts, locked)
	}
}
