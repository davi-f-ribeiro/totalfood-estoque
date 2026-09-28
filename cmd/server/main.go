package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

type config struct {
	Addr          string
	DatabaseURL   string
	MigrationsDir string
	RunMigrations bool
	MaxOpenConns  int
	MaxIdleConns  int
	ConnMaxLife   time.Duration
	ConnMaxIdle   time.Duration
}

type app struct {
	db *sql.DB
}

func main() {
	if err := run(); err != nil {
		log.Printf("server stopped: %v", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	db, err := openDatabase(ctx, cfg)
	if err != nil {
		log.Printf("database connection failed; verify DATABASE_URL, network and credentials")
		return fmt.Errorf("database unavailable")
	}
	defer db.Close()

	if cfg.RunMigrations {
		if err := runMigrations(ctx, db, cfg.MigrationsDir); err != nil {
			log.Printf("migration failed in %s: %v", cfg.MigrationsDir, err)
			return fmt.Errorf("migrations failed")
		}
	}

	a := &app{db: db}
	if err := a.bootstrapDirector(ctx); err != nil {
		log.Printf("bootstrap director failed: %v", err)
		return fmt.Errorf("bootstrap failed")
	}

	srv := newHTTPServer(cfg.Addr, a)

	errCh := make(chan error, 1)
	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
			return
		}
		errCh <- nil
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	case err := <-errCh:
		return err
	}
}

func loadConfig() (config, error) {
	addr := os.Getenv("HTTP_ADDR")
	if addr == "" {
		addr = ":" + getenv("PORT", "8080")
	}
	cfg := config{
		Addr:          addr,
		DatabaseURL:   os.Getenv("DATABASE_URL"),
		MigrationsDir: getenv("MIGRATIONS_DIR", "migrations"),
		RunMigrations: getenv("RUN_MIGRATIONS", "true") != "false",
		MaxOpenConns:  getenvInt("DB_MAX_OPEN_CONNS", 10),
		MaxIdleConns:  getenvInt("DB_MAX_IDLE_CONNS", 5),
		ConnMaxLife:   time.Duration(getenvInt("DB_CONN_MAX_LIFETIME_SECONDS", 1800)) * time.Second,
		ConnMaxIdle:   time.Duration(getenvInt("DB_CONN_MAX_IDLE_SECONDS", 300)) * time.Second,
	}
	if cfg.DatabaseURL == "" {
		return cfg, fmt.Errorf("DATABASE_URL is required")
	}
	return cfg, nil
}

func openDatabase(ctx context.Context, cfg config) (*sql.DB, error) {
	db, err := sql.Open("pgx", cfg.DatabaseURL)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(cfg.MaxOpenConns)
	db.SetMaxIdleConns(cfg.MaxIdleConns)
	db.SetConnMaxLifetime(cfg.ConnMaxLife)
	db.SetConnMaxIdleTime(cfg.ConnMaxIdle)

	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := db.PingContext(pingCtx); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

func newHTTPServer(addr string, a *app) *http.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", a.health)
	a.registerUIHandlers(mux)
	a.registerAdminHandlers(mux)
	a.registerCatalogHandlers(mux)
	a.registerAssetHandlers(mux)
	a.registerFiscalHandlers(mux)
	a.registerAuthHandlers(mux)
	a.registerAccountHandlers(mux)
	a.registerMovementHandlers(mux)

	return &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}
}

func (a *app) health(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()

	if err := a.db.PingContext(ctx); err != nil {
		http.Error(w, "service unavailable\n", http.StatusServiceUnavailable)
		return
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok\n"))
}

func runMigrations(ctx context.Context, db *sql.DB, dir string) error {
	if _, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version text PRIMARY KEY,
		applied_at timestamptz NOT NULL DEFAULT now()
	)`); err != nil {
		return err
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("read migrations dir %q: %w", dir, err)
	}
	var files []string
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		files = append(files, entry.Name())
	}
	sort.Strings(files)

	for _, name := range files {
		version := strings.TrimSuffix(name, ".sql")
		var exists bool
		if err := db.QueryRowContext(ctx, "SELECT EXISTS (SELECT 1 FROM schema_migrations WHERE version = $1)", version).Scan(&exists); err != nil {
			return err
		}
		if exists {
			continue
		}

		contents, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return fmt.Errorf("read migration %s: %w", name, err)
		}

		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("begin migration %s: %w", name, err)
		}
		if _, err := tx.ExecContext(ctx, string(contents)); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("apply migration %s: %w", name, err)
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO schema_migrations (version) VALUES ($1)", version); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("record migration %s: %w", name, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit migration %s: %w", name, err)
		}
	}
	return nil
}

func getenv(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func getenvInt(key string, fallback int) int {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	var n int
	if _, err := fmt.Sscanf(value, "%d", &n); err != nil || n < 0 {
		return fallback
	}
	return n
}
