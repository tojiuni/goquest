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
	"github.com/tojiuni/lymphhub/authz"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"

	"github.com/tojiuni/goquest/internal/audit"
	"github.com/tojiuni/goquest/internal/auth"
	"github.com/tojiuni/goquest/internal/brainclient"
	"github.com/tojiuni/goquest/internal/comment"
	"github.com/tojiuni/goquest/internal/config"
	"github.com/tojiuni/goquest/internal/contract"
	"github.com/tojiuni/goquest/internal/device"
	"github.com/tojiuni/goquest/internal/directory"
	"github.com/tojiuni/goquest/internal/githubapp"
	"github.com/tojiuni/goquest/internal/handler"
	"github.com/tojiuni/goquest/internal/idempotency"
	"github.com/tojiuni/goquest/internal/integration"
	"github.com/tojiuni/goquest/internal/macro"
	"github.com/tojiuni/goquest/internal/magic"
	"github.com/tojiuni/goquest/internal/mention"
	"github.com/tojiuni/goquest/internal/migrate"
	"github.com/tojiuni/goquest/internal/notify"
	"github.com/tojiuni/goquest/internal/observability"
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
	"github.com/tojiuni/goquest/internal/vcs"
	"github.com/tojiuni/goquest/internal/webhook"
	"github.com/tojiuni/goquest/internal/workspace"
	"github.com/tojiuni/goquest/internal/xp"
)

func main() {
	cfg := config.Load()

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	// --- Tracing (OpenTelemetry) — no-op unless OTEL_EXPORTER_OTLP_ENDPOINT is set ---
	tracerShutdown, err := observability.InitTracer(ctx)
	if err != nil {
		log.Printf("warn: otel tracer init: %v (continuing without tracing)", err)
		tracerShutdown = func(context.Context) error { return nil }
	}
	defer func() {
		sctx, scancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer scancel()
		_ = tracerShutdown(sctx)
	}()

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
	tagResolver := resolve.NewTagResolver(pjStore, tagSvc)

	commentStore := comment.NewPGStore(pool)
	commentSvc := comment.NewService(commentStore)

	mentionStore := mention.NewPGStore(pool)
	mentionSvc := mention.NewService(mentionStore, projectMemberLister{pjSvc})

	commentH := handler.NewCommentHandler(commentSvc, func(r *http.Request) bool {
		return canSeeInternal(r, wsSvc)
	}).WithMentions(svc, mentionSvc, nuclexEmitter)

	// vcsSvc must be constructed before ticketH, which takes it as the
	// ticket-GET links provider (embeds vcs_links without ticket->vcs import).
	vcsStore := vcs.NewPGStore(pool)
	vcsSvc := vcs.NewService(vcsStore, svc, commentSvc)
	vcsH := handler.NewVCSHandler(vcsSvc, vcs.NewGitHub(), projectResolver, cfg.VCSGitHubWebhookSecret)

	// auditLogger must be constructed before ticketH so ref add/remove handlers
	// can record audit entries. The audit handler (auditH) reuses the same logger
	// further down.
	auditLogger := audit.New(pool)
	ticketH := handler.NewTicketHandler(svc, sdbChecker, sdbWriter, projectResolver, tagResolver, auditLogger, vcsSvc)
	requireTicketView := auth.RequireTicketView(svc, sdbChecker)
	requireTicketManage := auth.RequireTicketManage(svc, sdbChecker)
	idempotent := idempotency.Middleware(idempotency.NewRedisStore(rdb))
	healthH := handler.NewHealthHandler(pool, rdb)
	openapiH, err := handler.NewOpenAPIHandler()
	if err != nil {
		log.Fatalf("openapi: %v", err)
	}
	wsH := handler.NewWorkspaceHandler(wsSvc)
	pjH := handler.NewProjectHandler(pjSvc)
	tagH := handler.NewTagHandler(tagSvc)
	xpH := handler.NewXPHandler(xpSvc)

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

	// Lymphhub actor-lookup proxy (goq-user-lookup-proxy) — resolves
	// requester/assignee ids to display names. Unconfigured (empty
	// LymphhubOIDCURL) → noopLookuper, so /users/lookup stays live but always
	// returns [] until the lymphhub service client secrets are provisioned.
	var userLookup directory.Lookuper = noopLookuper{}
	if cfg.LymphhubOIDCURL != "" && cfg.LymphhubServiceClientID != "" {
		directoryClient := directory.NewClient(cfg.LymphhubOIDCURL, cfg.LymphhubOIDCHost, cfg.LymphhubServiceClientID, cfg.LymphhubServiceClientSecret, http.DefaultClient)
		userLookup = directory.NewCachedLookuper(directoryClient, 5*time.Minute)
	}
	usersH := handler.NewUsersHandler(userLookup)
	mentionH := handler.NewMentionHandler(mentionSvc, userLookup)

	// brain-backed ticket reference docs + GitHub-backed content
	// (goq-refdoc-goquest-brain-client). Both degrade to inert responses
	// when unconfigured — see noopBrainLookuper/noopContentFetcher below.
	var refDocsBrain handler.BrainLookuper = noopBrainLookuper{}
	if cfg.BrainGRPCAddr != "" {
		brainClient, err := brainclient.New(cfg.BrainGRPCAddr, cfg.BrainTenant)
		if err != nil {
			log.Printf("warn: brainclient.New: %v (ticket docs will be unavailable)", err)
		} else {
			refDocsBrain = brainClient
		}
	}
	var refDocsGitHub handler.ContentFetcher = noopContentFetcher{}
	if cfg.GitHubAppID != "" && cfg.GitHubAppPrivateKeyPEM != "" {
		ghClient, err := githubapp.NewClient(cfg.GitHubAppID, cfg.GitHubAppInstallationID, cfg.GitHubAppPrivateKeyPEM, http.DefaultClient)
		if err != nil {
			log.Printf("warn: githubapp.NewClient: %v (doc content will be unavailable)", err)
		} else {
			refDocsGitHub = ghClient
		}
	}
	refDocsH := handler.NewRefDocsHandler(refDocsBrain, refDocsGitHub, wsSvc, cfg.GenesoRepoOwner, cfg.GenesoRepoName)

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
	// Outermost: create a server span per request and extract inbound trace
	// context. No-op spans when tracing is disabled.
	r.Use(otelhttp.NewMiddleware("goquest-service",
		otelhttp.WithSpanNameFormatter(func(_ string, req *http.Request) string {
			return req.Method + " " + req.URL.Path
		}),
	))
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
		r.Get("/openapi.json", openapiH.JSON)
		r.Get("/openapi.yaml", openapiH.YAML)

		// ── Public magic-link endpoints (Phase H3) ─────────────
		// No JWT required — caller is anonymous and identifies themselves
		// via email; the issued ct_* token is then used for subsequent calls.
		r.Post("/auth/magic-link/request", magicH.Request)
		r.Post("/auth/magic-link/exchange", magicH.Exchange)

		// ── Public VCS webhook — HMAC-verified, not API-key gated ──
		r.Post("/vcs/webhooks/github", vcsH.GitHubWebhook)

		r.Group(func(r chi.Router) {
			// API key check runs before JWT so agents can use X-API-Key header.
			// APIKeyMiddlewareRich also stores APIKeyMeta in the context so per-route
			// capability/origin/rate-limit middleware can enforce policy.
			r.Use(auth.APIKeyMiddlewareRich(richKeyVerifier))
			// Customer-token middleware runs before JWT so magic-link
			// (Bearer ct_*) is consumed first. JWT path remains for staff.
			r.Use(auth.CustomerTokenMiddleware(&customerTokenParser{}))
			// Lymphhub CLI SSO session middleware — opt-in via LYMPHHUB_SESSION_ADDR.
			// Only acts on "Bearer ls_..." tokens; uses the session-only client
			// (NewSessionClient) so it never touches SpiceDB and cannot fail on
			// missing/unreachable SpiceDB config. Runs before JWT so lymphhub
			// sessions are consumed first, after API-key/customer-token.
			if cfg.LymphhubSessionAddr != "" {
				log.Printf("lymphhub session middleware enabled: %s", cfg.LymphhubSessionAddr)
				r.Use(auth.LymphhubSessionMiddleware(authz.NewSessionClient(cfg.LymphhubSessionAddr)))
			}
			r.Use(authMiddleware)
			r.Use(auth.EnforceKeyOrigin())
			r.Use(auth.EnforceKeyRateLimit(rdb))

			// ── Devices (mobile push, Phase 7) ────────────────────
			r.Post("/devices/register", deviceH.Register)
			r.Get("/devices", deviceH.List)
			r.Delete("/devices/{id}", deviceH.Delete)

			// ── Legacy ticket routes (no project context) ──────────
			r.Post("/vcs/repos", vcsH.RegisterRepo)
			r.Get("/vcs/repos", vcsH.ListRepos)

			r.With(idempotent).Post("/tickets", ticketH.Create)
			r.Get("/tickets", ticketH.List)
			r.Get("/users/lookup", usersH.Lookup)
			// Mention inbox (goq-mention-api): scoped to the caller's own
			// identity — the actor id comes from auth, never a parameter.
			r.Get("/users/mentions", mentionH.ListMine)
			r.Post("/mentions/{id}/seen", mentionH.MarkSeen)
			// GitHub-backed doc content (goq-refdoc-goquest-brain-client)
			r.Get("/docs/content", refDocsH.Content)
			// Ticket analytics: COUNT(*) grouped by a whitelisted column. The
			// handler applies the same SpiceDB visibility scope as List.
			r.Get("/analytics/tickets", ticketH.TicketAnalytics)
			// Bulk update: manage authz is enforced per-id inside the handler
			// (ids arrive in the body, not the path), so this stays out of the
			// /tickets/{id} manage-gated subrouter.
			r.Post("/tickets/bulk", ticketH.BulkUpdate)
			r.Get("/tickets/doc/{doc_id}", ticketH.GetDoc)
			r.Get("/tickets/by-name/{title_name}", ticketH.GetByName)
			r.Route("/tickets/{id}", func(r chi.Router) {
				// View-only floor for all /tickets/{id}/* routes; the lifecycle-mutating
				// routes additionally require manage access. Both gates are permissive in
				// shadow mode (NoopChecker) and enforced when SPICEDB_ENFORCE is set.
				r.Use(requireTicketView)
				r.Get("/", ticketH.Get)
				r.With(requireTicketManage).Patch("/", ticketH.Update)
				r.With(requireTicketManage).Delete("/", ticketH.Cancel)
				r.With(requireTicketManage).Post("/approve", ticketH.Approve)
				r.With(requireTicketManage).Post("/reopen", ticketH.Reopen)
				r.Post("/org-reassign", ticketH.OrgReassign)
				r.Get("/subtasks", ticketH.ListSubtasks)
				r.Post("/attempts", ticketH.SubmitAttempt)
				r.Get("/attempts", ticketH.ListAttempts)

				// ── Gopedia refs (gopedia ref-doc feature) ────────
				r.Get("/refs", ticketH.ListRefs)
				r.Post("/refs", ticketH.AddRefs)
				r.Delete("/refs/{l1_id}", ticketH.RemoveRef)
				r.Patch("/refs/reorder", ticketH.ReorderRefs)

				// brain-backed ticket reference docs (goq-refdoc-goquest-brain-client)
				r.Get("/docs", refDocsH.TicketDocs)

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

// canSeeInternal reports whether the caller may see is_internal=true comments.
// Phase H3: customer-token (magic-link) contexts MUST NOT see internal comments.
// goq-org-backend Task 6: external-org callers (admin or member) are outsiders
// to staff conversation, same as magic-link customers — must not see internal
// comments either. goq-org-claim-staff-content-comment-bypass: an org claim
// alone does NOT make a caller external — a caller who is ALSO staff (a
// member of any goquest workspace) must keep full internal visibility, so
// this defers to auth.IsExternalOnly rather than treating org-claim presence
// as sufficient on its own.
func canSeeInternal(r *http.Request, staff auth.StaffChecker) bool {
	return !auth.IsExternalOnly(r.Context(), r, staff)
}

// noopLookuper is the /users/lookup fallback when lymphhub's service client
// isn't configured — the route stays live and returns [] rather than being
// absent, so goquest-ui can call it unconditionally (goq-user-lookup-proxy).
type noopLookuper struct{}

func (noopLookuper) Lookup(_ context.Context, _ []string) ([]directory.Actor, error) {
	return nil, nil
}

// projectMemberLister adapts project.Service to mention.MemberLister —
// mention deliberately doesn't import project (its dependency is just
// {UserID string}), so this small shape conversion lives at the wiring
// layer instead.
type projectMemberLister struct{ svc *project.Service }

func (a projectMemberLister) ListMembers(ctx context.Context, projectID uuid.UUID) ([]*mention.ProjectMember, error) {
	members, err := a.svc.ListMembers(ctx, projectID)
	if err != nil {
		return nil, err
	}
	out := make([]*mention.ProjectMember, len(members))
	for i, m := range members {
		out[i] = &mention.ProjectMember{UserID: m.UserID}
	}
	return out, nil
}

// noopBrainLookuper is the /tickets/{id}/docs fallback when brain isn't
// configured — the route stays live and returns an empty list rather than
// being absent (goq-refdoc-goquest-brain-client).
type noopBrainLookuper struct{}

func (noopBrainLookuper) TicketDocs(context.Context, string, string) ([]brainclient.RelatedDoc, error) {
	return nil, nil
}

// noopContentFetcher is the /docs/content fallback when the GitHub App isn't
// configured — always reports not-found rather than erroring differently
// per-caller.
type noopContentFetcher struct{}

func (noopContentFetcher) Content(context.Context, string, string, string) (string, error) {
	return "", githubapp.ErrNotFound
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
