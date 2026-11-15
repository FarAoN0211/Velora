// Package jobs implementa la cola de trabajos en PostgreSQL (FOR UPDATE SKIP LOCKED) y el planificador de tareas.
package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/robfig/cron/v3"
)

// Handler ejecuta un trabajo y devuelve un resultado que se guarda en jobs.result.
type Handler func(ctx context.Context, payload json.RawMessage) (any, error)

// Queue es la cola de trabajos.
type Queue struct{ pool *pgxpool.Pool }

// NewQueue crea la cola.
func NewQueue(pool *pgxpool.Pool) *Queue { return &Queue{pool: pool} }

// ErrDuplicate indica que ya hay un trabajo vivo con la misma clave de deduplicación.
var ErrDuplicate = errors.New("ya hay un trabajo igual en cola o en ejecución")

// Enqueue añade un trabajo. Con dedupeKey, no se duplica mientras haya otro igual en cola o en ejecución (idempotencia, §14).
func (q *Queue) Enqueue(ctx context.Context, kind string, payload any, dedupeKey string) (int64, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return 0, err
	}
	var id int64
	err = q.pool.QueryRow(ctx,
		`insert into jobs (kind, payload, dedupe_key) values ($1, $2, nullif($3, '')) returning id`,
		kind, raw, dedupeKey).Scan(&id)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return 0, ErrDuplicate
	}
	return id, err
}

// Job es el estado público de un trabajo.
type Job struct {
	ID         int64           `json:"id"`
	Kind       string          `json:"kind"`
	State      string          `json:"state"`
	Attempts   int             `json:"attempts"`
	LastError  *string         `json:"last_error,omitempty"`
	Result     json.RawMessage `json:"result,omitempty"`
	CreatedAt  time.Time       `json:"created_at"`
	FinishedAt *time.Time      `json:"finished_at,omitempty"`
	Progress   json.RawMessage `json:"progress,omitempty"`
}

// Recent devuelve los últimos trabajos de un tipo.
func (q *Queue) Recent(ctx context.Context, kind string, limit int) ([]Job, error) {
	rows, err := q.pool.Query(ctx,
		`select id, kind, state, attempts, last_error, result, created_at, finished_at, progress
		 from jobs where kind = $1 order by id desc limit $2`, kind, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Job, error) {
		var j Job
		err := r.Scan(&j.ID, &j.Kind, &j.State, &j.Attempts, &j.LastError, &j.Result, &j.CreatedAt, &j.FinishedAt, &j.Progress)
		return j, err
	})
}

// Worker toma trabajos de la cola y los ejecuta de uno en uno (en el N100, la concurrencia del worker es baja a propósito).
type Worker struct {
	pool     *pgxpool.Pool
	handlers map[string]Handler
	name     string
	poll     time.Duration
	// Timeouts fija el plazo por tipo de trabajo (por defecto DefaultTimeout). Al vencer, el trabajo se da por fallido
	// y el worker sigue con la cola aunque el manejador siga bloqueado (p. ej. en una lectura de un montaje FUSE colgado).
	Timeouts map[string]time.Duration
}

// DefaultTimeout es el plazo de un trabajo sin plazo propio.
const DefaultTimeout = time.Hour

// abandonGrace es lo que se espera a un manejador tras cancelarlo antes de abandonarlo.
var abandonGrace = 30 * time.Second

// NewWorker crea un worker con sus manejadores por tipo de trabajo.
func NewWorker(pool *pgxpool.Pool, handlers map[string]Handler) *Worker {
	host, _ := os.Hostname()
	return &Worker{pool: pool, handlers: handlers, name: fmt.Sprintf("%s-%d", host, os.Getpid()), poll: 2 * time.Second}
}

// Run procesa trabajos hasta que se cancele el contexto. Cada tipo de separate va por un carril propio: un escaneo largo
// o colgado de un montaje remoto no retrasa las copias de seguridad ni el mantenimiento, y la detección de intros (E2),
// que puede durar horas, no retrasa los escaneos.
func (w *Worker) Run(ctx context.Context, separate ...string) error {
	w.recoverStale(ctx)
	go w.recoverLoop(ctx)
	// Al parar se espera a todos los carriles: cada uno devuelve a la cola el trabajo que llevaba. Sin esperar, el
	// proceso terminaba con el carril principal y los trabajos de los otros quedaban «en ejecución» sin dueño.
	var wg sync.WaitGroup
	for _, kind := range separate {
		wg.Add(1)
		go func(k string) {
			defer wg.Done()
			_ = w.lane(ctx, []string{k}, false)
		}(kind)
	}
	err := w.lane(ctx, separate, true)
	wg.Wait()
	return err
}

// lane procesa los trabajos cuyo tipo está en kinds (exclude=false) o no está (exclude=true).
func (w *Worker) lane(ctx context.Context, kinds []string, exclude bool) error {
	if kinds == nil {
		kinds = []string{}
	}
	for {
		ran, err := w.runOne(ctx, kinds, exclude)
		if err != nil && ctx.Err() == nil {
			slog.Error("error en la cola de trabajos", "error", err)
		}
		if ran {
			continue
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(w.poll):
		}
	}
}

// recoverStale devuelve a la cola los trabajos que quedaron "en ejecución" si el worker murió (más de 1 h sin terminar).
func (w *Worker) recoverStale(ctx context.Context) {
	// Un trabajo no puede seguir en ejecución pasado su plazo más el margen de abandono: si lo está, quien lo llevaba
	// murió (reinicio o despliegue) y nadie lo va a cerrar. Se mira el plazo de cada tipo: con un umbral fijo de 1 h,
	// un trabajo cortado por un reinicio rápido se quedaba «en ejecución» hasta el siguiente arranque.
	kinds, secs := []string{}, []float64{}
	for k, t := range w.Timeouts {
		kinds = append(kinds, k)
		secs = append(secs, t.Seconds())
	}
	tag, err := w.pool.Exec(ctx, `update jobs j set state = 'queued', locked_by = null, locked_at = null
		where j.state = 'running' and j.locked_at < now() - make_interval(secs =>
		  coalesce((select t.s from unnest($1::text[], $2::float8[]) as t(k, s) where t.k = j.kind), $3) + $4)`,
		kinds, secs, DefaultTimeout.Seconds(), (abandonGrace + 5*time.Minute).Seconds())
	if err != nil {
		slog.Error("no se pudieron recuperar trabajos atascados", "error", err)
	} else if tag.RowsAffected() > 0 {
		slog.Warn("trabajos atascados devueltos a la cola", "trabajos", tag.RowsAffected())
	}
}

// recoverLoop repite la recuperación mientras el worker vive (no solo al arrancar).
func (w *Worker) recoverLoop(ctx context.Context) {
	tick := time.NewTicker(5 * time.Minute)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			w.recoverStale(ctx)
		}
	}
}

func (w *Worker) runOne(ctx context.Context, kinds []string, exclude bool) (bool, error) {
	var id int64
	var kind string
	var payload json.RawMessage
	var attempts, maxAttempts int
	// Cada intento lleva su propia marca (fencing token) en locked_by: solo el intento que tiene la marca puede cerrar
	// el trabajo. Un intento antiguo (abandonado o recuperado tras una caída) nunca sobrescribe a uno nuevo.
	token := w.name + ":" + uuid.NewString()
	err := w.pool.QueryRow(ctx,
		`update jobs set state = 'running', locked_by = $1, locked_at = now(), attempts = attempts + 1
		 where id = (select id from jobs where state = 'queued' and run_after <= now()
		               and (kind = any($2)) <> $3
		             order by priority, id for update skip locked limit 1)
		 returning id, kind, payload, attempts, max_attempts`, token, kinds, exclude).
		Scan(&id, &kind, &payload, &attempts, &maxAttempts)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	h, ok := w.handlers[kind]
	if !ok {
		_, err = w.pool.Exec(ctx, `update jobs set state = 'failed', last_error = $2, finished_at = now() where id = $1`,
			id, "tipo de trabajo desconocido: "+kind)
		return true, err
	}
	slog.Info("trabajo iniciado", "id", id, "tipo", kind, "intento", attempts)
	_, _ = w.pool.Exec(ctx, `update jobs set progress = null where id = $1`, id)
	hctx := context.WithValue(ctx, progressKey{}, progressTarget{w: w, id: id, token: token})
	result, runErr := w.runHandler(hctx, kind, h, payload)
	if ctx.Err() != nil {
		// El worker se está parando (reinicio o despliegue): el trabajo vuelve a la cola ya, sin contar como intento
		// fallido. Con el contexto cancelado no se podía escribir y se quedaba «en ejecución» sin nadie que lo llevara.
		cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_, err := w.pool.Exec(cctx, `update jobs set state = 'queued', locked_by = null, locked_at = null, run_after = now(),
			attempts = greatest(attempts - 1, 0) where id = $1 and state = 'running' and locked_by = $2`, id, token)
		return true, err
	}
	if runErr != nil {
		state, retryIn := "queued", time.Duration(attempts*attempts)*time.Minute
		if attempts >= maxAttempts {
			state = "failed"
		}
		slog.Error("trabajo fallido", "id", id, "tipo", kind, "error", runErr, "estado", state)
		tag, err := w.pool.Exec(ctx,
			`update jobs set state = $2, last_error = $3, locked_by = null, run_after = now() + make_interval(secs => $4),
			 finished_at = case when $2 = 'failed' then now() end where id = $1 and state = 'running' and locked_by = $5`,
			id, state, runErr.Error(), retryIn.Seconds(), token)
		w.warnIfStale(tag.RowsAffected(), err, id, kind)
		return true, err
	}
	raw, _ := json.Marshal(result)
	tag, err := w.pool.Exec(ctx,
		`update jobs set state = 'done', result = $2, last_error = null, locked_by = null, finished_at = now()
		 where id = $1 and state = 'running' and locked_by = $3`, id, raw, token)
	w.warnIfStale(tag.RowsAffected(), err, id, kind)
	slog.Info("trabajo terminado", "id", id, "tipo", kind)
	return true, err
}

func (w *Worker) warnIfStale(rows int64, err error, id int64, kind string) {
	if err == nil && rows == 0 {
		slog.Warn("resultado de un intento obsoleto descartado: el trabajo ya lo lleva otro intento", "id", id, "tipo", kind)
	}
}

// runHandler ejecuta el manejador con su plazo. Si no vuelve tras cancelarlo, se abandona (la gorutina queda bloqueada
// en el sistema, pero la cola, incluidas las copias de seguridad, no se detiene).
func (w *Worker) runHandler(ctx context.Context, kind string, h Handler, payload json.RawMessage) (any, error) {
	timeout := DefaultTimeout
	if t, ok := w.Timeouts[kind]; ok {
		timeout = t
	}
	hctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	type outcome struct {
		res any
		err error
	}
	done := make(chan outcome, 1)
	go func() {
		res, err := h(hctx, payload)
		done <- outcome{res, err}
	}()
	select {
	case o := <-done:
		return o.res, o.err
	case <-hctx.Done():
	}
	select {
	case o := <-done:
		if o.err == nil && ctx.Err() == nil && hctx.Err() != nil {
			return o.res, fmt.Errorf("tiempo agotado tras %s", timeout)
		}
		return o.res, o.err
	case <-time.After(abandonGrace):
		slog.Error("trabajo abandonado: no respondió tras agotar su plazo", "tipo", kind, "plazo", timeout)
		return nil, fmt.Errorf("tiempo agotado tras %s; trabajo abandonado (¿montaje colgado?)", timeout)
	}
}

// Scheduler encola las tareas de scheduled_tasks según su expresión cron, en la zona horaria de la instalación.
type Scheduler struct {
	pool  *pgxpool.Pool
	queue *Queue
	loc   *time.Location
}

// NewScheduler crea el planificador.
func NewScheduler(pool *pgxpool.Pool, queue *Queue, loc *time.Location) *Scheduler {
	return &Scheduler{pool: pool, queue: queue, loc: loc}
}

// Run comprueba cada minuto qué tareas tocan. Solo encola: la ejecución la hace el worker.
func (s *Scheduler) Run(ctx context.Context) error {
	parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
	tick := time.NewTicker(time.Minute)
	defer tick.Stop()
	for {
		if err := s.tick(ctx, parser, time.Now().In(s.loc)); err != nil && ctx.Err() == nil {
			slog.Error("error en el planificador", "error", err)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-tick.C:
		}
	}
}

func (s *Scheduler) tick(ctx context.Context, parser cron.Parser, now time.Time) error {
	rows, err := s.pool.Query(ctx, `select id, cron, last_run_at from scheduled_tasks where enabled`)
	if err != nil {
		return err
	}
	type task struct {
		id, spec string
		last     *time.Time
	}
	tasks, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (task, error) {
		var t task
		return t, r.Scan(&t.id, &t.spec, &t.last)
	})
	if err != nil {
		return err
	}
	for _, t := range tasks {
		sched, err := parser.Parse(t.spec)
		if err != nil {
			slog.Error("expresión cron no válida", "tarea", t.id, "cron", t.spec)
			continue
		}
		from := now.Add(-time.Minute)
		if t.last != nil && t.last.After(from) {
			from = t.last.In(s.loc)
		}
		if next := sched.Next(from); next.After(now) {
			continue
		}
		_, err = s.queue.Enqueue(ctx, t.id, map[string]any{"scheduled": true}, "scheduled:"+t.id)
		if err != nil && !errors.Is(err, ErrDuplicate) {
			return err
		}
		if _, err := s.pool.Exec(ctx, `update scheduled_tasks set last_run_at = now(), last_status = 'encolada' where id = $1`, t.id); err != nil {
			return err
		}
	}
	return nil
}

type progressKey struct{}

type progressTarget struct {
	w     *Worker
	id    int64
	token string
}

// ReportProgress guarda el progreso del trabajo en curso (lo lee el panel). Solo escribe el intento que tiene la marca
// del trabajo; fuera de un trabajo no hace nada. Quien llama decide cada cuánto (no más de una vez por segundo).
func ReportProgress(ctx context.Context, progress any) {
	t, ok := ctx.Value(progressKey{}).(progressTarget)
	if !ok {
		return
	}
	raw, err := json.Marshal(progress)
	if err != nil {
		return
	}
	_, _ = t.w.pool.Exec(context.WithoutCancel(ctx), `update jobs set progress = $2 where id = $1 and state = 'running' and locked_by = $3`,
		t.id, raw, t.token)
}

// LatestByPayload devuelve, para cada valor de payload->>key, el último trabajo de ese tipo (p. ej. el último escaneo de
// cada biblioteca, con su estado y progreso).
func (q *Queue) LatestByPayload(ctx context.Context, kind, key string) (map[string]Job, error) {
	rows, err := q.pool.Query(ctx,
		`select distinct on (payload->>$2) payload->>$2, id, kind, state, attempts, last_error, result, created_at, finished_at, progress
		 from jobs where kind = $1 and payload ? $2 order by payload->>$2, id desc`, kind, key)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]Job{}
	for rows.Next() {
		var k string
		var j Job
		if err := rows.Scan(&k, &j.ID, &j.Kind, &j.State, &j.Attempts, &j.LastError, &j.Result, &j.CreatedAt, &j.FinishedAt, &j.Progress); err != nil {
			return nil, err
		}
		out[k] = j
	}
	return out, rows.Err()
}
