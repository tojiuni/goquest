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
	"github.com/go-chi/cors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"github.com/tojiuni/goquest/internal/apikey"

	"github.com/tojiuni/goquest/internal/audit"
	"github.com/tojiuni/goquest/internal/contract"
	"github.com/tojiuni/goquest/internal/auth"
	"github.com/tojiuni/goquest/internal/comment"
	"github.com/tojiuni/goquest/internal/config"
	"github.com/tojiuni/goquest/internal/device"
	"github.com/tojiuni/goquest/internal/handler"
	"github.com/tojiuni/goquest/internal/integration"
	"github.com/tojiuni/goquest/internal/macro"
	"github.com/tojiuni/goquest/internal/magic"
	"github.com/tojiuni/goquest/internal/migrate"
	"github.com/tojiuni/goquest/internal/notify"
	"github.com/tojiuni/goquest/internal/pii"
	"github.com/tojiuni/goquest/internal/project"
	"github.com/tojiuni/goquest/internal/queue"
	"github.com/tojiuni/goquest/internal/resolve"
	"github.com/tojiuni/goquest/internal/sla"
	"github.com/tojiuni/goquest/internal/spicedb"
	"github.com/tojiuni/goquest/internal/sse"
	"github.com/tojiuni/goquest/internal/tag"
	"github.com/tojiuni/goquest/internal/template"
	"github.com/tojiuni/goquest/internal/ticket"
	"github.com/tojiuni/goquest/internal/webhook"
	"github.com/tojiuni/goquest/internal/workspace"
	"github.com/tojiuni/goquest/internal/xp"
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
	xpStore := xp.NewPGStore(pool)
	xpSvc := xp.NewService(xpStore)

	magicStore := magic.NewPGStore(pool)
	magicSvc := magic.NewService(magicStore)

	store := ticket.NewPGStore(pool)
	pub := queue.NewPublisher(rdb)
	nuclexEmitter := integration.NewNuclexEmitter(rdb, cfg.NuclexEventsStream)
	webhookDeliverer := webhook.New()
	svc := ticket.NewService(store, pub, nuclexEmitter, xpSvc, webhookDeliverer, magicSvc)

	wsStore := workspace.NewPGStore(pool)

	pjStore := project.NewPGStore(pool)

	tagStore := tag.NewPGStore(pool)
	tagSvc := tag.NewService(tagStore)

	// SpiceDB — optional; falls back to noop when endpoint is unavailable.
	var (
		sdbChecker spicedb.PermissionChecker  = &spicedb.NoopChecker{}
		sdbWriter  spicedb.RelationshipWriter = &spicedb.NoopWriter{}
	)
	if cfg.SpiceDBEndpoint != "" {
		sdbClient, sdbErr := spicedb.NewClient(cfg.SpiceDBEndpoint, cfg.SpiceDBToken, cfg.SpiceDBInsecure)
		if sdbErr != nil {
			log.Printf("warn: spicedb unavailable (%v) — running without fine-grained authz", sdbErr)
		} else {
			sdbWriter = sdbClient.Writer() // dual-write always active when connected
			if cfg.SpiceDBEnforce {
				sdbChecker = sdbClient.Checker()
				log.Printf("spicedb connected + ENFORCING: %s", cfg.SpiceDBEndpoint)
			} else {
				log.Printf("spicedb connected (shadow, checks permissive): %s", cfg.SpiceDBEndpoint)
			}
		}
	}

	wsSvc := workspace.NewService(wsStore, sdbWriter)
	pjSvc := project.NewService(pjStore, sdbWriter)

	apiKeyStore := apikey.NewPGStore(pool)
	apiKeySvc := apikey.NewService(apiKeyStore)
	apiKeyH := handler.NewAPIKeyHandler(apiKeySvc)

	tmplStore := template.NewPGStore(pool)
	tmplSvc := template.NewService(tmplStore)
	tmplH := handler.NewTemplateHandler(tmplSvc, svc)

	projectResolver := resolve.New(wsStore, pjStore)
	// auditLogger must be constructed before ticketH so ref add/remove handlers
	// can record audit entries. The audit handler (auditH) reuses the same logger
	// further down.
	auditLogger := audit.New(pool)
	ticketH := handler.NewTicketHandler(svc, sdbChecker, sdbWriter, projectResolver, auditLogger)
	requireTicketView := auth.RequireTicketView(svc, sdbChecker)
	healthH := handler.NewHealthHandler(pool, rdb)
	wsH := handler.NewWorkspaceHandler(wsSvc)
	pjH := handler.NewProjectHandler(pjSvc)
	tagH := handler.NewTagHandler(tagSvc)
	xpH := handler.NewXPHandler(xpSvc)

	commentStore := comment.NewPGStore(pool)
	commentSvc := comment.NewService(commentStore)
	// Phase H3: customer-token contexts (magic-link) MUST NOT see internal comments.
	commentH := handler.NewCommentHandler(commentSvc, func(r *http.Request) bool {
		return !auth.IsCustomerRequest(r)
	})

	macroStore := macro.NewPGStore(pool)
	macroSvc := macro.NewService(macroStore)
	macroH := handler.NewMacroHandler(macroSvc)

	publicURL := cfg.PublicURL
	if publicURL == "" {
		publicURL = "https://goquest.toji.homes"
	}
	mailer := notify.New(notify.Config{
		Host:     cfg.SMTPHost,
		Port:     cfg.SMTPPort,
		Username: cfg.SMTPUser,
		Password: cfg.SMTPPassword,
		From:     cfg.SMTPFrom,
	})
	magicH := handler.NewMagicHandler(magicSvc, mailer, publicURL)

	auditH := handler.NewAuditHandler(auditLogger)
	kpiH := handler.NewHelpdeskKPIHandler(pool)

	deviceStore := device.NewPGStore(pool)
	deviceSvc := device.NewService(deviceStore)
	deviceH := handler.NewDeviceHandler(deviceSvc)

	contractClient := contract.NewClient(cfg.ContractPlaneURL, &http.Client{Timeout: 10 * time.Second})
	contractSvc := contract.NewService(contractClient, sdbChecker, auditLogger)
	contractH := handler.NewContractHandler(contractSvc)

	// Adapter: apikey.Service implements VerifyAPIKey (string) but VerifyAPIKeyMeta
	// returns *apikey.MetaInfo. Wrap it so the auth middleware can take a
	// RichKeyVerifier returning *auth.APIKeyMeta without an apikey↔auth import cycle.
	richKeyVerifier := &keyVerifierAdapter{svc: apiKeySvc}

	// SSE hub — reads all ticket streams and fans out to connected clients.
	sseHub := sse.New(rdb,
		queue.StreamCreated,
		queue.StreamAssigned,
		queue.StreamUpdated,
		queue.StreamCompleted,
	)

	// --- Router ---
	r := chi.NewRouter()
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins:   cfg.CORSOrigins,
		AllowedMethods:   []string{"GET", "POST", "PATCH", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Content-Type", "X-API-Key", "Authorization"},
		AllowCredentials: false,
		MaxAge:           300,
	}))
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

		// ── Public magic-link endpoints (Phase H3) ─────────────
		// No JWT required — caller is anonymous and identifies themselves
		// via email; the issued ct_* token is then used for subsequent calls.
		r.Post("/auth/magic-link/request", magicH.Request)
		r.Post("/auth/magic-link/exchange", magicH.Exchange)

		r.Group(func(r chi.Router) {
			// API key check runs before JWT so agents can use X-API-Key header.
			// APIKeyMiddlewareRich also stores APIKeyMeta in the context so per-route
			// capability/origin/rate-limit middleware can enforce policy.
			r.Use(auth.APIKeyMiddlewareRich(richKeyVerifier))
			// Customer-token middleware runs before JWT so magic-link
			// (Bearer ct_*) is consumed first. JWT path remains for staff.
			r.Use(auth.CustomerTokenMiddleware(&customerTokenParser{}))
			r.Use(authMiddleware)
			r.Use(auth.EnforceKeyOrigin())
			r.Use(auth.EnforceKeyRateLimit(rdb))

			// ── Devices (mobile push, Phase 7) ────────────────────
			r.Post("/devices/register", deviceH.Register)
			r.Get("/devices", deviceH.List)
			r.Delete("/devices/{id}", deviceH.Delete)

			// ── Legacy ticket routes (no project context) ──────────
			r.Post("/tickets", ticketH.Create)
			r.Get("/tickets", ticketH.List)
			r.Route("/tickets/{id}", func(r chi.Router) {
				// View-only floor for all /tickets/{id}/* routes. Manage-level checks for
				// mutating routes (Update/Cancel/Approve) are deferred to a later phase.
				r.Use(requireTicketView)
				r.Get("/", ticketH.Get)
				r.Patch("/", ticketH.Update)
				r.Delete("/", ticketH.Cancel)
				r.Post("/approve", ticketH.Approve)
				r.Get("/subtasks", ticketH.ListSubtasks)
				r.Post("/attempts", ticketH.SubmitAttempt)
				r.Get("/attempts", ticketH.ListAttempts)

				// ── Gopedia refs (gopedia ref-doc feature) ────────
				r.Get("/refs", ticketH.ListRefs)
				r.Post("/refs", ticketH.AddRefs)
				r.Delete("/refs/{l1_id}", ticketH.RemoveRef)
				r.Patch("/refs/reorder", ticketH.ReorderRefs)

				// ── Helpdesk: comments (Phase H1 + H5 publish) ───
				r.Post("/comments", commentH.Create)
				r.Get("/comments", commentH.List)
				r.Patch("/comments/{cid}", commentH.Update)
				r.Delete("/comments/{cid}", commentH.Delete)
				r.Post("/comments/{cid}/publish", commentH.Publish)
			})

			// Reverse-lookup: which tickets reference a given gopedia doc?
			// Top-level (no /tickets/{id}/) and gated only by auth — the handler applies
			// the per-user SpiceDB visibility filter internally.
			r.Get("/refs", ticketH.ReverseLookupRefs)

			// ── XP Levels ─────────────────────────────────────────
			r.Get("/xp/levels", xpH.ListLevels)

			// ── SSE event stream ───────────────────────────────────
			r.Get("/events", sseHub.ServeHTTP)

			// ── Workspace ──────────────────────────────────────────
			r.Post("/workspaces", wsH.Create)
			r.Get("/workspaces", wsH.List)

			r.Route("/workspaces/{wid}", func(r chi.Router) {
				r.With(requireWsViewer).Get("/", wsH.Get)
				r.With(requireWsAdmin).Patch("/", wsH.Update)
				r.With(requireWsOwner).Delete("/", wsH.Delete)

				r.With(requireWsViewer).Get("/xp/me", xpH.GetMyXP)
				r.With(requireWsViewer).Get("/xp/leaderboard", xpH.Leaderboard)
				r.With(requireWsViewer).Get("/xp/history", xpH.GetXPHistory)

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

				// ── Macros (Phase H2) ─────────────────────────────
				r.With(requireWsAdmin).Post("/macros", macroH.Create)
				r.With(requireWsViewer).Get("/macros", macroH.List)
				r.Route("/macros/{mid}", func(r chi.Router) {
					r.With(requireWsAdmin).Patch("/", macroH.Update)
					r.With(requireWsAdmin).Delete("/", macroH.Delete)
				})

				// ── Helpdesk KPI + Audit (Phase H6) ───────────────
				r.With(requireWsViewer).Get("/helpdesk/kpi", kpiH.Get)
				r.With(requireWsAdmin).Get("/audit", auditH.List)

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

					// ── API Keys ──────────────────────────────────────
					r.With(requireWsAdmin, requirePjAdmin).Post("/api-keys", apiKeyH.Create)
					r.With(requireWsViewer, requirePjViewer).Get("/api-keys", apiKeyH.List)
					r.With(requireWsAdmin, requirePjAdmin).Delete("/api-keys/{kid}", apiKeyH.Delete)

					// ── Templates ─────────────────────────────────────
					r.With(requireWsAdmin, requirePjAdmin).Post("/templates", tmplH.Create)
					r.With(requireWsViewer, requirePjViewer).Get("/templates", tmplH.List)
					r.Route("/templates/{tid}", func(r chi.Router) {
						r.With(requireWsViewer, requirePjViewer).Get("/", tmplH.Get)
						r.With(requireWsAdmin, requirePjAdmin).Delete("/", tmplH.Delete)
						r.With(requireWsViewer, requirePjViewer).Post("/instantiate", tmplH.Instantiate)
					})
				})
			})

			// ── Invitation accept (global, no workspace middleware) ─
			r.Post("/invitations/{token}/accept", wsH.AcceptInvitation)

			// ── Contracts proxy ────────────────────────────────────
			r.Get("/contracts", contractH.List)
			r.Route("/contracts/{wf_id}", func(r chi.Router) {
				r.Get("/", contractH.Get)
				r.Post("/approve", contractH.Approve)
				r.Post("/abort", contractH.Abort)
				r.Post("/reject", contractH.RejectPlan)
			})

			// ── Goals proxy (submission gate) ──────────────────────
			r.Route("/goals/{wf_id}", func(r chi.Router) {
				r.Post("/submission-approve", contractH.SubmissionApprove)
				r.Post("/reject", contractH.RejectSubmission)
			})
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

	// --- SSE hub (background) ---
	go func() {
		if err := sseHub.Run(ctx); err != nil && ctx.Err() == nil {
			log.Printf("sse hub exited: %v", err)
		}
	}()

	// --- SLA worker (background) ---
	slaWorker := sla.New(store, svc)
	go func() {
		if err := slaWorker.Run(ctx); err != nil && ctx.Err() == nil {
			log.Printf("sla worker exited: %v", err)
		}
	}()

	// --- PII masking cron (Phase H7, daily) ---
	piiMasker := pii.New(pool)
	go func() {
		// First run after 5 min (let the service finish booting), then every 24h.
		t := time.NewTimer(5 * time.Minute)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if _, err := piiMasker.Run(ctx); err != nil {
					log.Printf("pii masker run: %v", err)
				}
				t.Reset(24 * time.Hour)
			}
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

// customerTokenParser adapts magic.ParseCustomerAccessToken to auth.CustomerTokenParser
// (kept here to avoid an auth↔magic import cycle).
type customerTokenParser struct{}

func (c *customerTokenParser) ParseCustomerAccessToken(raw string) (string, *uuid.UUID, error) {
	tok, err := magic.ParseCustomerAccessToken(raw)
	if err != nil {
		return "", nil, err
	}
	return tok.Email, tok.TicketID, nil
}

// keyVerifierAdapter wraps apikey.Service to satisfy auth.RichKeyVerifier.
// apikey.Service.VerifyAPIKeyMeta returns *apikey.MetaInfo (a per-package
// struct chosen to avoid an apikey↔auth import cycle); this adapter converts
// that to *auth.APIKeyMeta which the middleware understands.
type keyVerifierAdapter struct{ svc *apikey.Service }

func (a *keyVerifierAdapter) VerifyAPIKey(ctx context.Context, plainKey string) (string, error) {
	return a.svc.VerifyAPIKey(ctx, plainKey)
}

func (a *keyVerifierAdapter) VerifyAPIKeyMeta(ctx context.Context, plainKey string) (string, *auth.APIKeyMeta, error) {
	rid, info, err := a.svc.VerifyAPIKeyMeta(ctx, plainKey)
	if err != nil {
		return "", nil, err
	}
	return rid, &auth.APIKeyMeta{
		ID:                  info.ID,
		ProjectID:           info.ProjectID,
		Scope:               info.Scope,
		AllowedCapabilities: info.AllowedCapabilities,
		AllowedOrigins:      info.AllowedOrigins,
		RateLimitPerMin:     info.RateLimitPerMin,
	}, nil
}
