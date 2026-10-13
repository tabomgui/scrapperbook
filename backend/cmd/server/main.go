// Command server runs the API, the scheduler and the lease reaper.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"
	_ "time/tzdata" // the runtime image has no zoneinfo

	"github.com/jackc/pgx/v5/pgxpool"

	"scrapperbook/backend/internal/config"
	"scrapperbook/backend/internal/db"
	"scrapperbook/backend/internal/httpapi"
	"scrapperbook/backend/internal/ingest"
	"scrapperbook/backend/internal/jobs"
	"scrapperbook/backend/internal/notify"
	"scrapperbook/backend/internal/scheduler"
	"scrapperbook/backend/internal/source/facebook"
	"scrapperbook/backend/internal/sweep"
	"scrapperbook/backend/internal/workers"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := run(log); err != nil {
		log.Error("server stopped", "err", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg, err := config.Load(os.Getenv)
	if err != nil {
		return err
	}
	pool, err := db.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	if err := db.Migrate(ctx, pool); err != nil {
		return err
	}

	queue := jobs.New(pool, cfg.Limits)
	sched := scheduler.New(pool, queue, []string{facebook.Source}, log)
	internal := &httpapi.Internal{
		Token:   cfg.WorkerToken,
		Pool:    pool,
		Queue:   queue,
		Workers: workers.New(pool),
		Ingest:  ingest.New(ingest.NewRegistry(facebook.Normalizer{}), log),
		Log:     log,
	}
	public := &httpapi.Public{Pool: pool, Queue: queue, Workers: workers.New(pool), Log: log}

	// A nil drainer disables the Discord drain step in background() below —
	// there's nothing to send to without a webhook URL, and that's a valid,
	// expected single-user setup (not an error).
	var drainer *notify.Drainer
	if cfg.DiscordWebhookURL != "" {
		drainer = notify.NewDrainer(pool, notify.NewDiscordNotifier(cfg.DiscordWebhookURL), log)
	} else {
		log.Info("discord notifications disabled: DISCORD_WEBHOOK_URL not set")
	}

	mux := http.NewServeMux()
	internal.Routes(mux)
	public.Routes(mux)
	httpapi.Metrics(mux)
	httpapi.Health(mux, pool)

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		background(ctx, log, sched, queue, drainer, pool, cfg)
	}()
	go func() {
		defer wg.Done()
		backgroundRetention(ctx, log, pool, cfg)
	}()
	// Wait for both background loops to actually exit before pool.Close()
	// (deferred above) runs: without this, a ticker firing right as ctx is
	// canceled could still be mid-query against pool when Close() is called.
	defer wg.Wait()

	srv := &http.Server{Addr: cfg.HTTPAddr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()
	log.Info("server listening", "addr", cfg.HTTPAddr)

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// background runs the scheduler tick, lease reaper, Discord drain and
// unseen sweep once a minute. A panic in any one step is recovered
// per-iteration so one bad tick can't crash the whole process (and take
// the HTTP server down with it) — it logs and the loop just tries again
// next minute.
func background(
	ctx context.Context, log *slog.Logger, sched *scheduler.Scheduler, queue *jobs.Queue,
	drainer *notify.Drainer, pool *pgxpool.Pool, cfg config.Config,
) {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		runTickSafely(ctx, log, sched, queue, drainer, pool, cfg)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func runTickSafely(
	ctx context.Context, log *slog.Logger, sched *scheduler.Scheduler, queue *jobs.Queue,
	drainer *notify.Drainer, pool *pgxpool.Pool, cfg config.Config,
) {
	defer func() {
		if r := recover(); r != nil {
			log.Error("background tick panicked", "panic", r)
		}
	}()
	if n, err := sched.Tick(ctx); err != nil {
		log.Error("scheduler tick", "err", err)
	} else if n > 0 {
		log.Info("jobs enqueued", "count", n)
	}
	if n, err := queue.ReapExpiredLeases(ctx); err != nil {
		log.Error("reap leases", "err", err)
	} else if n > 0 {
		log.Warn("leases expired", "count", n)
	}
	if drainer != nil {
		if n, err := drainer.DrainPending(ctx); err != nil {
			log.Error("drain notifications", "err", err)
		} else if n > 0 {
			log.Info("notifications sent", "count", n)
		}
	}
	if n, err := sweep.MarkUnseen(ctx, pool, time.Now(), cfg.ListingUnseenAfter); err != nil {
		log.Error("mark unseen", "err", err)
	} else if n > 0 {
		log.Info("listings marked unseen", "count", n)
	}
}

// backgroundRetention purges old raw_captures rows once an hour — a
// DELETE that can touch a lot of rows, so it runs far less often than the
// per-minute loop above.
func backgroundRetention(ctx context.Context, log *slog.Logger, pool *pgxpool.Pool, cfg config.Config) {
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		runRetentionSafely(ctx, log, pool, cfg)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func runRetentionSafely(ctx context.Context, log *slog.Logger, pool *pgxpool.Pool, cfg config.Config) {
	defer func() {
		if r := recover(); r != nil {
			log.Error("retention tick panicked", "panic", r)
		}
	}()
	if n, err := sweep.PurgeOldRawCaptures(ctx, pool, time.Now(), cfg.RawCapturesRetention); err != nil {
		log.Error("purge raw captures", "err", err)
	} else if n > 0 {
		log.Info("raw captures purged", "count", n)
	}
}
