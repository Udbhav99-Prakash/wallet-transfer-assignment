package service_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/google/uuid"

	"wallet-transfer-assignment/internal/domain"
	"wallet-transfer-assignment/internal/repository"
	"wallet-transfer-assignment/internal/repository/postgres"
	"wallet-transfer-assignment/internal/service"
	"wallet-transfer-assignment/pkg/testutil"
)

func setupServices(t *testing.T) (*service.TransferService, *service.WalletService) {
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
	walletService := service.NewWalletService(repos)

	return transferService, walletService
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

func TestTransferService_ConcurrentDebits_NoDoubleSpend(t *testing.T) {
	ctx := context.Background()
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

	wg.Wait()

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
	ctx := context.Background()
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

	wg.Wait()
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
