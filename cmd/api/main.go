package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"wallet-transfer-assignment/internal/config"
	"wallet-transfer-assignment/internal/handler"
	"wallet-transfer-assignment/internal/repository"
	"wallet-transfer-assignment/internal/repository/postgres"
	"wallet-transfer-assignment/internal/service"
)

func main() {
	cfg := config.Load()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	log.Printf("Connecting to PostgreSQL at %s ...", cfg.DatabaseURL)
	pool, err := postgres.NewPool(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}
	defer pool.Close()

	log.Println("Applying database migrations...")
	if err := postgres.Migrate(ctx, pool); err != nil {
		log.Fatalf("Failed to apply migrations: %v", err)
	}
	log.Println("Database migrations applied successfully.")

	// Construct repositories
	repos := repository.Repositories{
		Wallets:     postgres.NewWalletRepository(pool),
		Transfers:   postgres.NewTransferRepository(pool),
		Ledger:      postgres.NewLedgerRepository(pool),
		Idempotency: postgres.NewIdempotencyRepository(pool),
	}
	txManager := postgres.NewTxManager(pool)

	// Construct services
	transferService := service.NewTransferService(txManager, repos)
	walletService := service.NewWalletService(repos)

	// Construct handlers
	transferHandler := handler.NewTransferHandler(transferService)
	walletHandler := handler.NewWalletHandler(walletService)

	// Construct router
	router := handler.NewRouter(handler.Config{
		TransferHandler: transferHandler,
		WalletHandler:   walletHandler,
	})

	server := &http.Server{
		Addr:         ":" + cfg.Port,
		Handler:      router,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// Server run context
	serverErrors := make(chan error, 1)
	go func() {
		log.Printf("Wallet Transfer Service listening on port %s", cfg.Port)
		serverErrors <- server.ListenAndServe()
	}()

	// Graceful shutdown
	shutdown := make(chan os.Signal, 1)
	signal.Notify(shutdown, os.Interrupt, syscall.SIGTERM)

	select {
	case err := <-serverErrors:
		if !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("Server error: %v", err)
		}
	case sig := <-shutdown:
		log.Printf("Received signal %v, initiating graceful shutdown...", sig)
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer shutdownCancel()

		if err := server.Shutdown(shutdownCtx); err != nil {
			log.Fatalf("Server forced to shutdown: %v", err)
		}
		log.Println("Server gracefully stopped.")
	}
}
