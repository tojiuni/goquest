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
	"github.com/tojiuni/goquest/internal/project"
	"github.com/tojiuni/goquest/internal/queue"
	"github.com/tojiuni/goquest/internal/spicedb"
	"github.com/tojiuni/goquest/internal/tag"
	"github.com/tojiuni/goquest/internal/ticket"
	"github.com/tojiuni/goquest/internal/workspace"
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

	wsStore := workspace.NewPGStore(pool)
	wsSvc := workspace.NewService(wsStore)

	pjStore := project.NewPGStore(pool)
	pjSvc := project.NewService(pjStore)

	tagStore := tag.NewPGStore(pool)
	tagSvc := tag.NewService(tagStore)

	// SpiceDB — optional; falls back to noop when endpoint is unavailable.
	var (
		sdbChecker spicedb.PermissionChecker = &spicedb.NoopChecker{}
		sdbWriter  spicedb.RelationshipWriter = &spicedb.NoopWriter{}
	)
	if cfg.SpiceDBEndpoint != "" {
		sdbClient, sdbErr := spicedb.NewClient(cfg.SpiceDBEndpoint, cfg.SpiceDBToken, cfg.SpiceDBInsecure)
		if sdbErr != nil {
			log.Printf("warn: spicedb unavailable (%v) — running without fine-grained authz", sdbErr)
		} else {
			sdbChecker = sdbClient.Checker()
			sdbWriter = sdbClient.Writer()
			log.Printf("spicedb connected: %s", cfg.SpiceDBEndpoint)
		}
	}

	ticketH := handler.NewTicketHandler(svc, sdbChecker, sdbWriter)
	healthH := handler.NewHealthHandler(pool, rdb)
	wsH := handler.NewWorkspaceHandler(wsSvc)
	pjH := handler.NewProjectHandler(pjSvc)
	tagH := handler.NewTagHandler(tagSvc)

	// --- Router ---
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)

	authMiddleware := auth.Middleware(cfg.JWKSUrl, cfg.SkipAuth)
	requireWsViewer := auth.RequireWorkspaceRole(wsStore, workspace.RoleViewer)
	requireWsAdmin := auth.RequireWorkspaceRole(wsStore, workspace.RoleAdmin)
	requireWsOwner := auth.RequireWorkspaceRole(wsStore, workspace.RoleOwner)
	requirePjViewer := auth.RequireProjectRole(pjStore, project.RoleViewer)
	requirePjAdmin := auth.RequireProjectRole(pjStore, project.RoleAdmin)

	r.Get("/healthz", healthH.Health)

	r.Route("/api/v1", func(r chi.Router) {
		r.Get("/health", healthH.Health)
		r.Get("/health/deps", healthH.Deps)

		r.Group(func(r chi.Router) {
			r.Use(authMiddleware)

			// ── Legacy ticket routes (no project context) ──────────
			r.Post("/tickets", ticketH.Create)
			r.Get("/tickets", ticketH.List)
			r.Route("/tickets/{id}", func(r chi.Router) {
				r.Get("/", ticketH.Get)
				r.Patch("/", ticketH.Update)
				r.Delete("/", ticketH.Cancel)
				r.Get("/subtasks", ticketH.ListSubtasks)
				r.Post("/attempts", ticketH.SubmitAttempt)
				r.Get("/attempts", ticketH.ListAttempts)
			})

			// ── Workspace ──────────────────────────────────────────
			r.Post("/workspaces", wsH.Create)
			r.Get("/workspaces", wsH.List)

			r.Route("/workspaces/{wid}", func(r chi.Router) {
				r.With(requireWsViewer).Get("/", wsH.Get)
				r.With(requireWsAdmin).Patch("/", wsH.Update)
				r.With(requireWsOwner).Delete("/", wsH.Delete)

				r.With(requireWsViewer).Get("/members", wsH.ListMembers)
				r.With(requireWsOwner).Patch("/members/{uid}/role", wsH.SetMemberRole)
				r.With(requireWsAdmin).Delete("/members/{uid}", wsH.RemoveMember)

				r.With(requireWsAdmin).Post("/invitations", wsH.CreateInvitation)
				r.With(requireWsAdmin).Get("/invitations", wsH.ListInvitations)
				r.With(requireWsAdmin).Delete("/invitations/{iid}", wsH.DeleteInvitation)

				// ── Tags ──────────────────────────────────────────
				r.With(requireWsAdmin).Post("/tags", tagH.Create)
				r.With(requireWsViewer).Get("/tags", tagH.List)
				r.Route("/tags/{tid}", func(r chi.Router) {
					r.With(requireWsViewer).Get("/", tagH.Get)
					r.With(requireWsAdmin).Patch("/", tagH.Update)
					r.With(requireWsAdmin).Delete("/", tagH.Delete)
				})

				// ── Project ────────────────────────────────────────
				r.With(requireWsAdmin).Post("/projects", pjH.Create)
				r.With(requireWsViewer).Get("/projects", pjH.List)

				r.Route("/projects/{pid}", func(r chi.Router) {
					r.With(requireWsViewer, requirePjViewer).Get("/", pjH.Get)
					r.With(requireWsViewer, requirePjAdmin).Patch("/", pjH.Update)
					r.With(requireWsAdmin).Delete("/", pjH.Delete)

					r.With(requireWsViewer, requirePjViewer).Get("/members", pjH.ListMembers)
					r.With(requireWsViewer, requirePjAdmin).Post("/members", pjH.AddMember)
					r.With(requireWsViewer, requirePjAdmin).Patch("/members/{uid}/role", pjH.SetMemberRole)
					r.With(requireWsViewer, requirePjAdmin).Delete("/members/{uid}", pjH.RemoveMember)
				})
			})

			// ── Invitation accept (global, no workspace middleware) ─
			r.Post("/invitations/{token}/accept", wsH.AcceptInvitation)
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
