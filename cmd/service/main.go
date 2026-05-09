package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"github.com/tojiuni/goquest/internal/auth"
	"github.com/tojiuni/goquest/internal/config"
	"github.com/tojiuni/goquest/internal/handler"
	"github.com/tojiuni/goquest/internal/integration"
	"github.com/tojiuni/goquest/internal/migrate"
	"github.com/tojiuni/goquest/internal/queue"
	"github.com/tojiuni/goquest/internal/ticket"
)

func main() {
	cfg := config.Load()

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	// --- Postgres ---
	pool, err := pgxpool.New(ctx, cfg.PostgresDSN())
	if err != nil {
		log.Fatalf("pgxpool.New: %v", err)
	}
	defer pool.Close()

	if err := pool.Ping(ctx); err != nil {
		log.Fatalf("postgres ping: %v", err)
	}

	if err := migrate.Run(ctx, pool); err != nil {
		log.Fatalf("migrations: %v", err)
	}

	// --- Redis ---
	redisOpts, err := redis.ParseURL(cfg.RedisURL)
	if err != nil {
		log.Fatalf("parse redis url: %v", err)
	}
	rdb := redis.NewClient(redisOpts)
	defer rdb.Close()

	if err := rdb.Ping(ctx).Err(); err != nil {
		log.Fatalf("redis ping: %v", err)
	}

	// --- Wiring ---
	store := ticket.NewPGStore(pool)
	pub := queue.NewPublisher(rdb)
	nuclexEmitter := integration.NewNuclexEmitter(rdb, cfg.NuclexEventsStream)
	svc := ticket.NewService(store, pub, nuclexEmitter)

	ticketH := handler.NewTicketHandler(svc)
	healthH := handler.NewHealthHandler(pool, rdb)

	// --- Router ---
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)

	authMiddleware := auth.Middleware(cfg.JWKSUrl, cfg.SkipAuth)

	r.Get("/healthz", healthH.Health)

	r.Route("/api/v1", func(r chi.Router) {
		r.Get("/health", healthH.Health)
		r.Get("/health/deps", healthH.Deps)

		r.Group(func(r chi.Router) {
			r.Use(authMiddleware)
			r.Post("/tickets", ticketH.Create)
			r.Get("/tickets", ticketH.List)
			r.Get("/tickets/{id}", ticketH.Get)
			r.Patch("/tickets/{id}", ticketH.Update)
			r.Delete("/tickets/{id}", ticketH.Cancel)
		})
	})

	// --- Incoming event consumer (background) ---
	consumer := queue.NewConsumer(rdb, func(ctx context.Context, data string) error {
		log.Printf("incoming event: %s", data)
		return nil
	})
	go func() {
		if err := consumer.Run(ctx); err != nil && ctx.Err() == nil {
			log.Printf("consumer exited: %v", err)
		}
	}()

	// --- HTTP server ---
	srv := &http.Server{
		Addr:         ":" + cfg.ServicePort,
		Handler:      r,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	go func() {
		log.Printf("goquest-service listening on :%s", cfg.ServicePort)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("listen: %v", err)
		}
	}()

	<-ctx.Done()
	log.Println("shutting down...")

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer shutdownCancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("shutdown: %v", err)
	}
}
