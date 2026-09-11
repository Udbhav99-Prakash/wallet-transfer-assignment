package service_test

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"wallet-transfer-assignment/internal/domain"
	"wallet-transfer-assignment/internal/repository"
	"wallet-transfer-assignment/internal/repository/postgres"
	"wallet-transfer-assignment/internal/service"
	"wallet-transfer-assignment/pkg/testutil"
)

func setupServicesWithRepos(t *testing.T) (*service.TransferService, *service.WalletService, repository.Repositories) {
	t.Helper()
	pool := testutil.SetupTestDB(t)

	repos := repository.Repositories{
		Wallets:     postgres.NewWalletRepository(pool),
		Transfers:   postgres.NewTransferRepository(pool),
		Ledger:      postgres.NewLedgerRepository(pool),
		Idempotency: postgres.NewIdempotencyRepository(pool),
	}
	txManager := postgres.NewTxManager(pool)

	transferService := service.NewTransferService(txManager, repos)
	walletService := service.NewWalletService(txManager, repos)

	return transferService, walletService, repos
}

func setupServices(t *testing.T) (*service.TransferService, *service.WalletService) {
	transferSvc, walletSvc, _ := setupServicesWithRepos(t)
	return transferSvc, walletSvc
}

func TestTransferService_Success(t *testing.T) {
	ctx := context.Background()
	transferSvc, walletSvc := setupServices(t)

	// 1. Seed two wallets
	w1, err := walletSvc.CreateWallet(ctx, service.CreateWalletRequest{
		ID:             "wallet_1",
		Name:           "Alice",
		InitialBalance: 500,
	})
	if err != nil {
		t.Fatalf("failed to create wallet 1: %v", err)
	}

	w2, err := walletSvc.CreateWallet(ctx, service.CreateWalletRequest{
		ID:             "wallet_2",
		Name:           "Bob",
		InitialBalance: 200,
	})
	if err != nil {
		t.Fatalf("failed to create wallet 2: %v", err)
	}

	// 2. Execute transfer of 150
	req := service.CreateTransferRequest{
		IdempotencyKey: "key_tx_1",
		FromWalletID:   w1.ID,
		ToWalletID:     w2.ID,
		Amount:         150,
	}

	resp, err := transferSvc.ExecuteTransfer(ctx, req)
	if err != nil {
		t.Fatalf("unexpected transfer error: %v", err)
	}

	if resp.Status != domain.TransferStatusProcessed {
		t.Fatalf("expected status PROCESSED, got %s", resp.Status)
	}
	if resp.Amount != 150 {
		t.Fatalf("expected amount 150, got %d", resp.Amount)
	}

	// 3. Verify updated balances
	updatedW1, err := walletSvc.GetWallet(ctx, w1.ID)
	if err != nil {
		t.Fatalf("failed to get wallet 1: %v", err)
	}
	if updatedW1.Balance != 350 {
		t.Fatalf("expected wallet 1 balance 350, got %d", updatedW1.Balance)
	}

	updatedW2, err := walletSvc.GetWallet(ctx, w2.ID)
	if err != nil {
		t.Fatalf("failed to get wallet 2: %v", err)
	}
	if updatedW2.Balance != 350 {
		t.Fatalf("expected wallet 2 balance 350, got %d", updatedW2.Balance)
	}

	// 4. Verify double-entry ledger entries
	ledgerW1, err := walletSvc.GetWalletLedger(ctx, w1.ID)
	if err != nil {
		t.Fatalf("failed to get wallet 1 ledger: %v", err)
	}
	// Initial deposit (credit 500) + transfer debit (debit 150) = 2 entries
	if len(ledgerW1) != 2 {
		t.Fatalf("expected 2 ledger entries for wallet 1, got %d", len(ledgerW1))
	}
	if ledgerW1[1].Type != domain.LedgerEntryTypeDebit || ledgerW1[1].Amount != 150 {
		t.Fatalf("expected debit entry of 150, got %+v", ledgerW1[1])
	}

	ledgerW2, err := walletSvc.GetWalletLedger(ctx, w2.ID)
	if err != nil {
		t.Fatalf("failed to get wallet 2 ledger: %v", err)
	}
	if len(ledgerW2) != 2 {
		t.Fatalf("expected 2 ledger entries for wallet 2, got %d", len(ledgerW2))
	}
	if ledgerW2[1].Type != domain.LedgerEntryTypeCredit || ledgerW2[1].Amount != 150 {
		t.Fatalf("expected credit entry of 150, got %+v", ledgerW2[1])
	}

	// 5. Verify ledger reconciliation
	storedBal1, ledgerBal1, isBalanced1, err := walletSvc.ReconcileBalance(ctx, w1.ID)
	if err != nil || !isBalanced1 {
		t.Fatalf("wallet 1 ledger reconciliation failed: stored=%d, ledger=%d, balanced=%v, err=%v", storedBal1, ledgerBal1, isBalanced1, err)
	}

	storedBal2, ledgerBal2, isBalanced2, err := walletSvc.ReconcileBalance(ctx, w2.ID)
	if err != nil || !isBalanced2 {
		t.Fatalf("wallet 2 ledger reconciliation failed: stored=%d, ledger=%d, balanced=%v, err=%v", storedBal2, ledgerBal2, isBalanced2, err)
	}
}

func TestTransferService_InsufficientFunds(t *testing.T) {
	ctx := context.Background()
	transferSvc, walletSvc := setupServices(t)

	w1, _ := walletSvc.CreateWallet(ctx, service.CreateWalletRequest{
		ID:             "wallet_broke",
		Name:           "Poor Charlie",
		InitialBalance: 50,
	})
	w2, _ := walletSvc.CreateWallet(ctx, service.CreateWalletRequest{
		ID:             "wallet_dest",
		Name:           "Recipient",
		InitialBalance: 100,
	})

	req := service.CreateTransferRequest{
		IdempotencyKey: "key_fail_1",
		FromWalletID:   w1.ID,
		ToWalletID:     w2.ID,
		Amount:         100, // Attempting to transfer 100 with only 50
	}

	resp, err := transferSvc.ExecuteTransfer(ctx, req)
	if !errors.Is(err, domain.ErrInsufficientFunds) {
		t.Fatalf("expected ErrInsufficientFunds, got %v", err)
	}

	if resp == nil || resp.Status != domain.TransferStatusFailed {
		t.Fatalf("expected FAILED response status, got %+v", resp)
	}

	// Balances must remain unchanged
	updatedW1, _ := walletSvc.GetWallet(ctx, w1.ID)
	if updatedW1.Balance != 50 {
		t.Fatalf("expected wallet 1 balance unchanged (50), got %d", updatedW1.Balance)
	}
	updatedW2, _ := walletSvc.GetWallet(ctx, w2.ID)
	if updatedW2.Balance != 100 {
		t.Fatalf("expected wallet 2 balance unchanged (100), got %d", updatedW2.Balance)
	}

	// Replaying with identical key must return cached failed response
	replayResp, replayErr := transferSvc.ExecuteTransfer(ctx, req)
	if !errors.Is(replayErr, domain.ErrInsufficientFunds) {
		t.Fatalf("expected replayed ErrInsufficientFunds, got %v", replayErr)
	}
	if !replayResp.IsReplay {
		t.Fatalf("expected IsReplay = true on duplicate request")
	}
	if replayResp.TransferID != resp.TransferID {
		t.Fatalf("expected identical transfer ID on replay, got %s vs %s", replayResp.TransferID, resp.TransferID)
	}
}

func TestTransferService_IdempotencyReplay(t *testing.T) {
	ctx := context.Background()
	transferSvc, walletSvc := setupServices(t)

	w1, _ := walletSvc.CreateWallet(ctx, service.CreateWalletRequest{
		ID:             "wallet_idem_from",
		Name:           "Sender",
		InitialBalance: 1000,
	})
	w2, _ := walletSvc.CreateWallet(ctx, service.CreateWalletRequest{
		ID:             "wallet_idem_to",
		Name:           "Receiver",
		InitialBalance: 0,
	})

	req := service.CreateTransferRequest{
		IdempotencyKey: "unique_idem_key_42",
		FromWalletID:   w1.ID,
		ToWalletID:     w2.ID,
		Amount:         250,
	}

	// First execution
	resp1, err := transferSvc.ExecuteTransfer(ctx, req)
	if err != nil {
		t.Fatalf("first transfer failed: %v", err)
	}
	if resp1.IsReplay {
		t.Fatalf("first response should not be marked as replay")
	}

	// Second execution with identical key and payload
	resp2, err := transferSvc.ExecuteTransfer(ctx, req)
	if err != nil {
		t.Fatalf("replay transfer failed: %v", err)
	}
	if !resp2.IsReplay {
		t.Fatalf("second response must be marked as replay")
	}
	if resp2.TransferID != resp1.TransferID {
		t.Fatalf("expected same transfer ID, got %s and %s", resp1.TransferID, resp2.TransferID)
	}
	if resp1.ResponseCode != 201 {
		t.Fatalf("expected first response code 201, got %d", resp1.ResponseCode)
	}
	if resp2.ResponseCode != 201 {
		t.Fatalf("expected replayed response code 201, got %d", resp2.ResponseCode)
	}

	// Verify balance was only deducted ONCE
	w1After, _ := walletSvc.GetWallet(ctx, w1.ID)
	if w1After.Balance != 750 {
		t.Fatalf("expected balance 750 (single debit), got %d", w1After.Balance)
	}
	w2After, _ := walletSvc.GetWallet(ctx, w2.ID)
	if w2After.Balance != 250 {
		t.Fatalf("expected balance 250 (single credit), got %d", w2After.Balance)
	}
}

func TestTransferService_IdempotencyConflict(t *testing.T) {
	ctx := context.Background()
	transferSvc, walletSvc := setupServices(t)

	w1, _ := walletSvc.CreateWallet(ctx, service.CreateWalletRequest{
		ID:             "wallet_c_1",
		Name:           "Sender",
		InitialBalance: 1000,
	})
	w2, _ := walletSvc.CreateWallet(ctx, service.CreateWalletRequest{
		ID:             "wallet_c_2",
		Name:           "Receiver",
		InitialBalance: 100,
	})

	// First transfer with key K
	req1 := service.CreateTransferRequest{
		IdempotencyKey: "shared_key_100",
		FromWalletID:   w1.ID,
		ToWalletID:     w2.ID,
		Amount:         100,
	}
	_, err := transferSvc.ExecuteTransfer(ctx, req1)
	if err != nil {
		t.Fatalf("initial transfer failed: %v", err)
	}

	// Second transfer with SAME key K but DIFFERENT amount (200)
	req2 := service.CreateTransferRequest{
		IdempotencyKey: "shared_key_100",
		FromWalletID:   w1.ID,
		ToWalletID:     w2.ID,
		Amount:         200,
	}
	_, err = transferSvc.ExecuteTransfer(ctx, req2)
	if !errors.Is(err, domain.ErrIdempotencyConflict) {
		t.Fatalf("expected ErrIdempotencyConflict, got %v", err)
	}
}

func TestTransferService_IdempotencyConflict_InvalidPayload(t *testing.T) {
	ctx := context.Background()
	transferSvc, walletSvc := setupServices(t)

	w1, _ := walletSvc.CreateWallet(ctx, service.CreateWalletRequest{
		ID:             "wallet_cp_1",
		Name:           "Sender",
		InitialBalance: 1000,
	})
	w2, _ := walletSvc.CreateWallet(ctx, service.CreateWalletRequest{
		ID:             "wallet_cp_2",
		Name:           "Receiver",
		InitialBalance: 100,
	})

	// 1. First transfer with valid payload and key K
	key := "conflict_invalid_payload_key"
	reqValid := service.CreateTransferRequest{
		IdempotencyKey: key,
		FromWalletID:   w1.ID,
		ToWalletID:     w2.ID,
		Amount:         100,
	}
	_, err := transferSvc.ExecuteTransfer(ctx, reqValid)
	if err != nil {
		t.Fatalf("initial transfer failed: %v", err)
	}

	// 2. Reuse key K with negative amount: must return ErrIdempotencyConflict, NOT ErrInvalidAmount
	reqNegAmount := service.CreateTransferRequest{
		IdempotencyKey: key,
		FromWalletID:   w1.ID,
		ToWalletID:     w2.ID,
		Amount:         -50,
	}
	_, err = transferSvc.ExecuteTransfer(ctx, reqNegAmount)
	if !errors.Is(err, domain.ErrIdempotencyConflict) {
		t.Fatalf("expected ErrIdempotencyConflict on negative amount reuse, got %v", err)
	}

	// 3. Reuse key K with zero amount: must return ErrIdempotencyConflict, NOT ErrInvalidAmount
	reqZeroAmount := service.CreateTransferRequest{
		IdempotencyKey: key,
		FromWalletID:   w1.ID,
		ToWalletID:     w2.ID,
		Amount:         0,
	}
	_, err = transferSvc.ExecuteTransfer(ctx, reqZeroAmount)
	if !errors.Is(err, domain.ErrIdempotencyConflict) {
		t.Fatalf("expected ErrIdempotencyConflict on zero amount reuse, got %v", err)
	}

	// 4. Reuse key K with missing from_wallet_id: must return ErrIdempotencyConflict, NOT ErrWalletNotFound
	reqMissingWallet := service.CreateTransferRequest{
		IdempotencyKey: key,
		FromWalletID:   "",
		ToWalletID:     w2.ID,
		Amount:         100,
	}
	_, err = transferSvc.ExecuteTransfer(ctx, reqMissingWallet)
	if !errors.Is(err, domain.ErrIdempotencyConflict) {
		t.Fatalf("expected ErrIdempotencyConflict on missing wallet reuse, got %v", err)
	}

	// 5. Reuse key K with same wallet ID: must return ErrIdempotencyConflict, NOT ErrSameWalletTransfer
	reqSameWallet := service.CreateTransferRequest{
		IdempotencyKey: key,
		FromWalletID:   w1.ID,
		ToWalletID:     w1.ID,
		Amount:         100,
	}
	_, err = transferSvc.ExecuteTransfer(ctx, reqSameWallet)
	if !errors.Is(err, domain.ErrIdempotencyConflict) {
		t.Fatalf("expected ErrIdempotencyConflict on same-wallet reuse, got %v", err)
	}

	// 6. Contrast with a BRAND NEW key: invalid payloads must return their specific validation errors
	newKey := "brand_new_unused_key"
	_, err = transferSvc.ExecuteTransfer(ctx, service.CreateTransferRequest{
		IdempotencyKey: newKey,
		FromWalletID:   w1.ID,
		ToWalletID:     w2.ID,
		Amount:         -10,
	})
	if !errors.Is(err, domain.ErrInvalidAmount) {
		t.Fatalf("expected ErrInvalidAmount for brand new key with negative amount, got %v", err)
	}

	// Missing idempotency key
	_, err = transferSvc.ExecuteTransfer(ctx, service.CreateTransferRequest{
		IdempotencyKey: "",
		FromWalletID:   w1.ID,
		ToWalletID:     w2.ID,
		Amount:         100,
	})
	if !errors.Is(err, domain.ErrMissingIdempotencyKey) {
		t.Fatalf("expected ErrMissingIdempotencyKey for empty idempotency key, got %v", err)
	}

	// Missing wallet ID (empty string)
	_, err = transferSvc.ExecuteTransfer(ctx, service.CreateTransferRequest{
		IdempotencyKey: newKey,
		FromWalletID:   "",
		ToWalletID:     w2.ID,
		Amount:         100,
	})
	if !errors.Is(err, domain.ErrMissingWalletID) {
		t.Fatalf("expected ErrMissingWalletID for brand new key with missing wallet, got %v", err)
	}

	// Non-existent wallet ID (wallet does not exist in DB)
	_, err = transferSvc.ExecuteTransfer(ctx, service.CreateTransferRequest{
		IdempotencyKey: "non_existent_wallet_test_key",
		FromWalletID:   "non_existent_sender_id",
		ToWalletID:     w2.ID,
		Amount:         100,
	})
	if !errors.Is(err, domain.ErrWalletNotFound) {
		t.Fatalf("expected ErrWalletNotFound when wallet does not exist in DB, got %v", err)
	}
}

func TestTransferService_ConcurrentDebits_NoDoubleSpend(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	transferSvc, walletSvc := setupServices(t)

	// Seed source wallet with 1,000 cents ($10.00)
	wFrom, _ := walletSvc.CreateWallet(ctx, service.CreateWalletRequest{
		ID:             "wallet_hotspot",
		Name:           "Hot Wallet",
		InitialBalance: 1000,
	})
	wTo, _ := walletSvc.CreateWallet(ctx, service.CreateWalletRequest{
		ID:             "wallet_sink",
		Name:           "Sink Wallet",
		InitialBalance: 0,
	})

	// Launch 10 concurrent requests, each attempting to debit 200 cents ($2.00)
	// Total requested = 10 * 200 = 2,000 cents.
	// Only 5 can possibly succeed (5 * 200 = 1,000). The remaining 5 must fail with insufficient funds.
	const totalRoutines = 10
	var wg sync.WaitGroup
	wg.Add(totalRoutines)

	type result struct {
		success bool
		err     error
	}
	results := make([]result, totalRoutines)

	for i := 0; i < totalRoutines; i++ {
		idx := i
		go func() {
			defer wg.Done()
			req := service.CreateTransferRequest{
				IdempotencyKey: fmt.Sprintf("concurrent_debit_key_%d", idx),
				FromWalletID:   wFrom.ID,
				ToWalletID:     wTo.ID,
				Amount:         200,
			}
			_, err := transferSvc.ExecuteTransfer(ctx, req)
			if err == nil {
				results[idx] = result{success: true}
			} else {
				results[idx] = result{success: false, err: err}
			}
		}()
	}

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// Succeeded within timeout
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for concurrent transfers to complete (possible deadlock or blocked row lock)")
	}

	var successCount, failedCount int
	for _, r := range results {
		if r.success {
			successCount++
		} else {
			failedCount++
			if !errors.Is(r.err, domain.ErrInsufficientFunds) {
				t.Errorf("expected ErrInsufficientFunds, got: %v", r.err)
			}
		}
	}

	if successCount != 5 {
		t.Fatalf("expected exactly 5 successes, got %d", successCount)
	}
	if failedCount != 5 {
		t.Fatalf("expected exactly 5 failures, got %d", failedCount)
	}

	// Validate final balances
	finalFrom, _ := walletSvc.GetWallet(ctx, wFrom.ID)
	if finalFrom.Balance != 0 {
		t.Fatalf("expected source wallet balance to be exactly 0, got %d", finalFrom.Balance)
	}

	finalTo, _ := walletSvc.GetWallet(ctx, wTo.ID)
	if finalTo.Balance != 1000 {
		t.Fatalf("expected sink wallet balance to be exactly 1000, got %d", finalTo.Balance)
	}

	// Verify ledger reconciliation for both wallets
	_, _, balancedFrom, err := walletSvc.ReconcileBalance(ctx, wFrom.ID)
	if err != nil || !balancedFrom {
		t.Fatalf("source wallet ledger is not balanced: %v", err)
	}

	_, _, balancedTo, err := walletSvc.ReconcileBalance(ctx, wTo.ID)
	if err != nil || !balancedTo {
		t.Fatalf("sink wallet ledger is not balanced: %v", err)
	}
}

func TestTransferService_ConcurrentBidirectional_NoDeadlocks(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	transferSvc, walletSvc := setupServices(t)

	// Seed two wallets with ample balance
	wA, _ := walletSvc.CreateWallet(ctx, service.CreateWalletRequest{
		ID:             "wallet_bidir_A",
		Name:           "Wallet A",
		InitialBalance: 10000,
	})
	wB, _ := walletSvc.CreateWallet(ctx, service.CreateWalletRequest{
		ID:             "wallet_bidir_B",
		Name:           "Wallet B",
		InitialBalance: 10000,
	})

	// Concurrently perform:
	// 20 transfers of 10 from A to B
	// 20 transfers of 10 from B to A
	// If deterministic lock ordering works, NO deadlocks will happen!
	const count = 20
	var wg sync.WaitGroup
	wg.Add(count * 2)

	errorsChan := make(chan error, count*2)

	// Transfers A -> B
	for i := 0; i < count; i++ {
		go func(idx int) {
			defer wg.Done()
			req := service.CreateTransferRequest{
				IdempotencyKey: fmt.Sprintf("bidir_a_to_b_%s", uuid.NewString()),
				FromWalletID:   wA.ID,
				ToWalletID:     wB.ID,
				Amount:         10,
			}
			_, err := transferSvc.ExecuteTransfer(ctx, req)
			if err != nil {
				errorsChan <- fmt.Errorf("A->B error: %w", err)
			}
		}(i)
	}

	// Transfers B -> A
	for i := 0; i < count; i++ {
		go func(idx int) {
			defer wg.Done()
			req := service.CreateTransferRequest{
				IdempotencyKey: fmt.Sprintf("bidir_b_to_a_%s", uuid.NewString()),
				FromWalletID:   wB.ID,
				ToWalletID:     wA.ID,
				Amount:         10,
			}
			_, err := transferSvc.ExecuteTransfer(ctx, req)
			if err != nil {
				errorsChan <- fmt.Errorf("B->A error: %w", err)
			}
		}(i)
	}

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// Succeeded within timeout
	case <-time.After(15 * time.Second):
		t.Fatal("timed out waiting for concurrent bidirectional transfers to complete (possible deadlock or blocked row lock)")
	}
	close(errorsChan)

	for err := range errorsChan {
		t.Fatalf("detected deadlock or concurrent transfer error: %v", err)
	}

	// Balances should both remain 10,000 because 20*10 left and 20*10 arrived
	finalA, _ := walletSvc.GetWallet(ctx, wA.ID)
	finalB, _ := walletSvc.GetWallet(ctx, wB.ID)

	if finalA.Balance != 10000 {
		t.Fatalf("expected wallet A balance 10000, got %d", finalA.Balance)
	}
	if finalB.Balance != 10000 {
		t.Fatalf("expected wallet B balance 10000, got %d", finalB.Balance)
	}
}

func TestTransferService_IdempotencyInProgress(t *testing.T) {
	ctx := context.Background()
	transferSvc, walletSvc, repos := setupServicesWithRepos(t)

	w1, _ := walletSvc.CreateWallet(ctx, service.CreateWalletRequest{
		ID:             "w_in_progress_1",
		Name:           "Sender",
		InitialBalance: 1000,
	})
	w2, _ := walletSvc.CreateWallet(ctx, service.CreateWalletRequest{
		ID:             "w_in_progress_2",
		Name:           "Receiver",
		InitialBalance: 0,
	})

	req := service.CreateTransferRequest{
		IdempotencyKey: "in_progress_key_1",
		FromWalletID:   w1.ID,
		ToWalletID:     w2.ID,
		Amount:         100,
	}

	// Pre-reserve the key with status IN_PROGRESS to simulate an active concurrent execution
	reqHash := domain.ComputeRequestHash(req.FromWalletID, req.ToWalletID, req.Amount)
	_, _, err := repos.Idempotency.ReserveIdempotency(ctx, &domain.IdempotencyRecord{
		IdempotencyKey: req.IdempotencyKey,
		RequestHash:    reqHash,
		Status:         domain.IdempotencyStatusInProgress,
	}, 30*time.Second)
	if err != nil {
		t.Fatalf("failed to seed in-progress idempotency reservation: %v", err)
	}

	// Overlapping concurrent request must immediately receive ErrIdempotencyInProgress without blocking
	_, err = transferSvc.ExecuteTransfer(ctx, req)
	if !errors.Is(err, domain.ErrIdempotencyInProgress) {
		t.Fatalf("expected ErrIdempotencyInProgress, got %v", err)
	}
}

func TestTransferService_StaleOwnerRecovery(t *testing.T) {
	ctx := context.Background()
	transferSvc, walletSvc, repos := setupServicesWithRepos(t)

	// Configure a short stale timeout for testing
	transferSvc.SetStaleTimeout(50 * time.Millisecond)

	w1, _ := walletSvc.CreateWallet(ctx, service.CreateWalletRequest{
		ID:             "w_stale_1",
		Name:           "Sender",
		InitialBalance: 1000,
	})
	w2, _ := walletSvc.CreateWallet(ctx, service.CreateWalletRequest{
		ID:             "w_stale_2",
		Name:           "Receiver",
		InitialBalance: 0,
	})

	req := service.CreateTransferRequest{
		IdempotencyKey: "stale_key_1",
		FromWalletID:   w1.ID,
		ToWalletID:     w2.ID,
		Amount:         100,
	}

	// Seed an IN_PROGRESS reservation
	reqHash := domain.ComputeRequestHash(req.FromWalletID, req.ToWalletID, req.Amount)
	_, _, err := repos.Idempotency.ReserveIdempotency(ctx, &domain.IdempotencyRecord{
		IdempotencyKey: req.IdempotencyKey,
		RequestHash:    reqHash,
		Status:         domain.IdempotencyStatusInProgress,
	}, 30*time.Second)
	if err != nil {
		t.Fatalf("failed to seed in-progress idempotency reservation: %v", err)
	}

	// Wait for the stale timeout to elapse
	time.Sleep(70 * time.Millisecond)

	// The stale lock should be reclaimed and the transfer should succeed
	resp, err := transferSvc.ExecuteTransfer(ctx, req)
	if err != nil {
		t.Fatalf("expected successful transfer after stale lock recovery, got %v", err)
	}
	if resp.Status != domain.TransferStatusProcessed {
		t.Fatalf("expected transfer status PROCESSED, got %s", resp.Status)
	}

	// Verify balance was updated
	w1After, _ := walletSvc.GetWallet(ctx, w1.ID)
	if w1After.Balance != 900 {
		t.Fatalf("expected sender balance 900, got %d", w1After.Balance)
	}
	w2After, _ := walletSvc.GetWallet(ctx, w2.ID)
	if w2After.Balance != 100 {
		t.Fatalf("expected receiver balance 100, got %d", w2After.Balance)
	}
}

func TestTransferService_DeferredCleanupOnFailure(t *testing.T) {
	ctx := context.Background()
	transferSvc, walletSvc, repos := setupServicesWithRepos(t)

	w1, _ := walletSvc.CreateWallet(ctx, service.CreateWalletRequest{
		ID:             "w_cleanup_1",
		Name:           "Sender",
		InitialBalance: 1000,
	})
	w2, _ := walletSvc.CreateWallet(ctx, service.CreateWalletRequest{
		ID:             "w_cleanup_2",
		Name:           "Receiver",
		InitialBalance: 0,
	})

	req := service.CreateTransferRequest{
		IdempotencyKey: "cleanup_key_1",
		FromWalletID:   w1.ID,
		ToWalletID:     w2.ID,
		Amount:         100,
	}

	// Create a pre-cancelled context to simulate an unexpected abort/timeout during transfer execution
	cancelledCtx, cancel := context.WithCancel(ctx)
	cancel()

	_, err := transferSvc.ExecuteTransfer(cancelledCtx, req)
	if err == nil {
		t.Fatalf("expected transfer with cancelled context to fail")
	}

	// The deferred cleanup should have deleted the IN_PROGRESS reservation.
	// Verify that the key is not locked in idempotency_records:
	rec, err := repos.Idempotency.GetIdempotency(ctx, req.IdempotencyKey)
	if err != nil {
		t.Fatalf("unexpected error fetching idempotency record: %v", err)
	}
	if rec != nil {
		t.Fatalf("expected idempotency record to be deleted by deferred cleanup, but found record with status %s", rec.Status)
	}

	// Subsequent transfer with the same key should succeed immediately without waiting for stale timeout
	resp, err := transferSvc.ExecuteTransfer(ctx, req)
	if err != nil {
		t.Fatalf("retry after failure failed: %v", err)
	}
	if resp.Status != domain.TransferStatusProcessed {
		t.Fatalf("expected transfer status PROCESSED, got %s", resp.Status)
	}
}

func TestTransferService_DestinationBalanceOverflow(t *testing.T) {
	ctx := context.Background()
	transferSvc, walletSvc, repos := setupServicesWithRepos(t)

	// Create sender wallet with 500
	w1, err := walletSvc.CreateWallet(ctx, service.CreateWalletRequest{
		ID:             "w_ovf_sender",
		Name:           "Sender",
		InitialBalance: 500,
	})
	if err != nil {
		t.Fatalf("failed to create sender wallet: %v", err)
	}

	// Create destination wallet with near-maximum balance (math.MaxInt64 - 50)
	destInitialBalance := int64(math.MaxInt64 - 50)
	w2 := &domain.Wallet{
		ID:       "w_ovf_dest",
		Name:     "Destination",
		Balance:  destInitialBalance,
		Currency: "USD",
	}
	if err := repos.Wallets.CreateWallet(ctx, w2); err != nil {
		t.Fatalf("failed to create destination wallet: %v", err)
	}

	// Attempt to transfer 100 to destination wallet, which would exceed math.MaxInt64
	req := service.CreateTransferRequest{
		IdempotencyKey: "key_overflow_test_1",
		FromWalletID:   w1.ID,
		ToWalletID:     w2.ID,
		Amount:         100,
	}

	resp, err := transferSvc.ExecuteTransfer(ctx, req)
	if err == nil {
		t.Fatalf("expected ErrBalanceOverflow, but got nil error and resp: %+v", resp)
	}
	if !errors.Is(err, domain.ErrBalanceOverflow) {
		t.Fatalf("expected errors.Is(err, domain.ErrBalanceOverflow), got %v", err)
	}

	// Verify sender balance is unchanged (500)
	w1After, err := walletSvc.GetWallet(ctx, w1.ID)
	if err != nil {
		t.Fatalf("failed to get sender wallet: %v", err)
	}
	if w1After.Balance != 500 {
		t.Fatalf("expected sender balance to remain 500, got %d", w1After.Balance)
	}

	// Verify destination balance is unchanged (math.MaxInt64 - 50)
	w2After, err := walletSvc.GetWallet(ctx, w2.ID)
	if err != nil {
		t.Fatalf("failed to get destination wallet: %v", err)
	}
	if w2After.Balance != destInitialBalance {
		t.Fatalf("expected destination balance to remain %d, got %d", destInitialBalance, w2After.Balance)
	}

	// Verify no idempotency lock is left stuck in IN_PROGRESS
	rec, err := repos.Idempotency.GetIdempotency(ctx, req.IdempotencyKey)
	if err != nil {
		t.Fatalf("failed to get idempotency record: %v", err)
	}
	if rec != nil {
		t.Fatalf("expected idempotency record to be cleaned up after overflow abort, but found: %+v", rec)
	}
}

func TestWalletService_SystemTreasuryReconciliation(t *testing.T) {
	ctx := context.Background()
	_, walletSvc, _ := setupServicesWithRepos(t)

	const treasuryOpeningBalance int64 = 100000000000000

	// 1. Immediately on clean database before any user wallet is created,
	// system_treasury must reconcile with its opening ledger funding record.
	storedBal, ledgerBal, isBalanced, err := walletSvc.ReconcileBalance(ctx, "system_treasury")
	if err != nil {
		t.Fatalf("failed to reconcile system_treasury: %v", err)
	}
	if !isBalanced {
		t.Fatalf("expected system_treasury to be balanced on startup, got stored=%d ledger=%d", storedBal, ledgerBal)
	}
	if storedBal != treasuryOpeningBalance || ledgerBal != treasuryOpeningBalance {
		t.Fatalf("expected balances to equal %d, got stored=%d ledger=%d", treasuryOpeningBalance, storedBal, ledgerBal)
	}

	// 2. Create a user wallet with initial balance 500
	userWallet, err := walletSvc.CreateWallet(ctx, service.CreateWalletRequest{
		ID:             "w_treasury_test_user",
		Name:           "User",
		InitialBalance: 500,
	})
	if err != nil {
		t.Fatalf("failed to create user wallet: %v", err)
	}

	// 3. User wallet must reconcile
	uStored, uLedger, uBalanced, err := walletSvc.ReconcileBalance(ctx, userWallet.ID)
	if err != nil || !uBalanced {
		t.Fatalf("expected user wallet to be balanced, got stored=%d ledger=%d err=%v", uStored, uLedger, err)
	}
	if uStored != 500 || uLedger != 500 {
		t.Fatalf("expected user wallet balance 500, got stored=%d ledger=%d", uStored, uLedger)
	}

	// 4. System treasury must remain balanced after funding the user wallet
	tStored, tLedger, tBalanced, err := walletSvc.ReconcileBalance(ctx, "system_treasury")
	if err != nil || !tBalanced {
		t.Fatalf("expected system_treasury to remain balanced after user funding, got stored=%d ledger=%d err=%v", tStored, tLedger, err)
	}
	expectedTreasuryBal := treasuryOpeningBalance - 500
	if tStored != expectedTreasuryBal || tLedger != expectedTreasuryBal {
		t.Fatalf("expected treasury balance %d, got stored=%d ledger=%d", expectedTreasuryBal, tStored, tLedger)
	}
}

func TestTransferService_CurrencyMismatch(t *testing.T) {
	ctx := context.Background()
	transferSvc, walletSvc, repos := setupServicesWithRepos(t)

	// Create USD wallet
	wUSD, err := walletSvc.CreateWallet(ctx, service.CreateWalletRequest{
		ID:             "w_curr_usd",
		Name:           "USD User",
		InitialBalance: 500,
		Currency:       "USD",
	})
	if err != nil {
		t.Fatalf("failed to create USD wallet: %v", err)
	}

	// Create EUR wallet (0 initial balance so no treasury currency conversion is needed)
	wEUR, err := walletSvc.CreateWallet(ctx, service.CreateWalletRequest{
		ID:             "w_curr_eur",
		Name:           "EUR User",
		InitialBalance: 0,
		Currency:       "EUR",
	})
	if err != nil {
		t.Fatalf("failed to create EUR wallet: %v", err)
	}

	// Attempt transfer from USD to EUR wallet
	req := service.CreateTransferRequest{
		IdempotencyKey: "curr_mismatch_key_1",
		FromWalletID:   wUSD.ID,
		ToWalletID:     wEUR.ID,
		Amount:         100,
	}

	resp, err := transferSvc.ExecuteTransfer(ctx, req)
	if err == nil {
		t.Fatalf("expected ErrCurrencyMismatch, got nil error and resp: %+v", resp)
	}
	if !errors.Is(err, domain.ErrCurrencyMismatch) {
		t.Fatalf("expected errors.Is(err, domain.ErrCurrencyMismatch), got %v", err)
	}

	// Assert balances remained unchanged
	u1, _ := walletSvc.GetWallet(ctx, wUSD.ID)
	if u1.Balance != 500 {
		t.Fatalf("expected USD wallet balance 500, got %d", u1.Balance)
	}
	u2, _ := walletSvc.GetWallet(ctx, wEUR.ID)
	if u2.Balance != 0 {
		t.Fatalf("expected EUR wallet balance 0, got %d", u2.Balance)
	}

	// Assert idempotency record was cleaned up
	rec, err := repos.Idempotency.GetIdempotency(ctx, req.IdempotencyKey)
	if err != nil {
		t.Fatalf("unexpected error querying idempotency: %v", err)
	}
	if rec != nil {
		t.Fatalf("expected idempotency record to be cleaned up after currency mismatch error, but found: %+v", rec)
	}
}

func TestWalletService_CreateWallet_TransactionalRollback(t *testing.T) {
	ctx := context.Background()
	_, walletSvc, repos := setupServicesWithRepos(t)

	// Attempt to create a wallet with currency EUR and initial balance 500.
	// Since system_treasury is USD, this will fail with ErrCurrencyMismatch during initial funding.
	// The transaction must roll back the wallet creation entirely.
	failedID := "w_atomic_rollback_test"
	_, err := walletSvc.CreateWallet(ctx, service.CreateWalletRequest{
		ID:             failedID,
		Name:           "EUR User",
		InitialBalance: 500,
		Currency:       "EUR",
	})
	if err == nil {
		t.Fatalf("expected error creating cross-currency funded wallet, got nil")
	}
	if !errors.Is(err, domain.ErrCurrencyMismatch) {
		t.Fatalf("expected ErrCurrencyMismatch, got %v", err)
	}

	// Verify wallet row was NOT committed
	w, err := repos.Wallets.GetWalletByID(ctx, failedID)
	if err == nil || !errors.Is(err, domain.ErrWalletNotFound) {
		t.Fatalf("expected wallet to not exist after rollback, got %v (wallet: %+v)", err, w)
	}

	// Verify the same ID can now be created cleanly with valid parameters
	wCreated, err := walletSvc.CreateWallet(ctx, service.CreateWalletRequest{
		ID:             failedID,
		Name:           "USD User",
		InitialBalance: 100,
		Currency:       "USD",
	})
	if err != nil {
		t.Fatalf("failed to create wallet with reused ID after previous rollback: %v", err)
	}
	if wCreated.ID != failedID || wCreated.Balance != 100 {
		t.Fatalf("unexpected wallet state: %+v", wCreated)
	}
}

func TestWalletService_ReconcileBalance_ConcurrentTransfers(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	transferSvc, walletSvc := setupServices(t)

	w1, err := walletSvc.CreateWallet(ctx, service.CreateWalletRequest{
		ID:             "reconcile_conc_w1",
		Name:           "Conc Sender",
		InitialBalance: 10000,
	})
	if err != nil {
		t.Fatalf("failed to create wallet 1: %v", err)
	}

	w2, err := walletSvc.CreateWallet(ctx, service.CreateWalletRequest{
		ID:             "reconcile_conc_w2",
		Name:           "Conc Receiver",
		InitialBalance: 0,
	})
	if err != nil {
		t.Fatalf("failed to create wallet 2: %v", err)
	}

	var wg sync.WaitGroup
	stopReconcile := make(chan struct{})
	reconcileErrCh := make(chan error, 50)

	// Run concurrent transfers
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			_, _ = transferSvc.ExecuteTransfer(ctx, service.CreateTransferRequest{
				IdempotencyKey: fmt.Sprintf("reconcile-tx-%d", idx),
				FromWalletID:   w1.ID,
				ToWalletID:     w2.ID,
				Amount:         50,
			})
		}(i)
	}

	// Concurrently reconcile balance
	reconcileWg := sync.WaitGroup{}
	reconcileWg.Add(1)
	go func() {
		defer reconcileWg.Done()
		for {
			select {
			case <-stopReconcile:
				return
			default:
				stored, ledger, isBalanced, err := walletSvc.ReconcileBalance(ctx, w1.ID)
				if err != nil {
					reconcileErrCh <- fmt.Errorf("reconcile error: %w", err)
					return
				}
				if !isBalanced {
					reconcileErrCh <- fmt.Errorf("false mismatch detected! stored=%d, ledger=%d", stored, ledger)
					return
				}
			}
		}
	}()

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// Completed within timeout
	case <-time.After(15 * time.Second):
		close(stopReconcile)
		t.Fatal("timed out waiting for concurrent transfers to complete (possible deadlock or blocked row lock)")
	}

	close(stopReconcile)
	reconcileWg.Wait()
	close(reconcileErrCh)

	for err := range reconcileErrCh {
		t.Fatal(err)
	}

	// Final verification
	stored, ledger, isBalanced, err := walletSvc.ReconcileBalance(ctx, w1.ID)
	if err != nil || !isBalanced {
		t.Fatalf("final reconcile failed: stored=%d, ledger=%d, balanced=%v, err=%v", stored, ledger, isBalanced, err)
	}
}
