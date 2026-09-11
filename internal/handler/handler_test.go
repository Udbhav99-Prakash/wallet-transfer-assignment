package handler_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"wallet-transfer-assignment/internal/domain"
	"wallet-transfer-assignment/internal/handler"
	"wallet-transfer-assignment/internal/repository"
	"wallet-transfer-assignment/internal/repository/postgres"
	"wallet-transfer-assignment/internal/service"
	"wallet-transfer-assignment/pkg/testutil"
)

func setupTestServer(t *testing.T) http.Handler {
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

	transferHandler := handler.NewTransferHandler(transferService)
	walletHandler := handler.NewWalletHandler(walletService)

	return handler.NewRouter(handler.Config{
		TransferHandler: transferHandler,
		WalletHandler:   walletHandler,
	})
}

func TestHandler_HealthCheck(t *testing.T) {
	router := setupTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}
}

func TestHandler_TransferWorkflow(t *testing.T) {
	router := setupTestServer(t)

	// 1. Create Wallet 1
	w1ReqBody, _ := json.Marshal(service.CreateWalletRequest{
		ID:             "hw_1",
		Name:           "Sender Wallet",
		InitialBalance: 1000,
	})
	w1Req := httptest.NewRequest(http.MethodPost, "/wallets", bytes.NewReader(w1ReqBody))
	w1Req.Header.Set("Content-Type", "application/json")
	w1Recorder := httptest.NewRecorder()
	router.ServeHTTP(w1Recorder, w1Req)

	if w1Recorder.Code != http.StatusCreated {
		t.Fatalf("expected 201 Created for wallet 1, got %d", w1Recorder.Code)
	}

	// 2. Create Wallet 2
	w2ReqBody, _ := json.Marshal(service.CreateWalletRequest{
		ID:             "hw_2",
		Name:           "Receiver Wallet",
		InitialBalance: 100,
	})
	w2Req := httptest.NewRequest(http.MethodPost, "/wallets", bytes.NewReader(w2ReqBody))
	w2Req.Header.Set("Content-Type", "application/json")
	w2Recorder := httptest.NewRecorder()
	router.ServeHTTP(w2Recorder, w2Req)

	if w2Recorder.Code != http.StatusCreated {
		t.Fatalf("expected 201 Created for wallet 2, got %d", w2Recorder.Code)
	}

	// 3. POST /transfers - Successful transfer
	transferReqBody, _ := json.Marshal(service.CreateTransferRequest{
		IdempotencyKey: "http_idem_key_1",
		FromWalletID:   "hw_1",
		ToWalletID:     "hw_2",
		Amount:         300,
	})
	txReq := httptest.NewRequest(http.MethodPost, "/transfers", bytes.NewReader(transferReqBody))
	txReq.Header.Set("Content-Type", "application/json")
	txRecorder := httptest.NewRecorder()
	router.ServeHTTP(txRecorder, txReq)

	if txRecorder.Code != http.StatusCreated {
		t.Fatalf("expected 201 Created for transfer, got %d: %s", txRecorder.Code, txRecorder.Body.String())
	}

	var createdTx service.TransferResponse
	if err := json.Unmarshal(txRecorder.Body.Bytes(), &createdTx); err != nil {
		t.Fatalf("failed to decode transfer response: %v", err)
	}
	if createdTx.Status != domain.TransferStatusProcessed {
		t.Fatalf("expected status PROCESSED, got %s", createdTx.Status)
	}

	// 4. POST /transfers - Replay with same idempotency key -> 200 OK
	txReplayReq := httptest.NewRequest(http.MethodPost, "/transfers", bytes.NewReader(transferReqBody))
	txReplayReq.Header.Set("Content-Type", "application/json")
	txReplayRecorder := httptest.NewRecorder()
	router.ServeHTTP(txReplayRecorder, txReplayReq)

	if txReplayRecorder.Code != http.StatusOK {
		t.Fatalf("expected 200 OK on replay, got %d: %s", txReplayRecorder.Code, txReplayRecorder.Body.String())
	}

	var replayTx service.TransferResponse
	if err := json.Unmarshal(txReplayRecorder.Body.Bytes(), &replayTx); err != nil {
		t.Fatalf("failed to decode replay response: %v", err)
	}
	if !replayTx.IsReplay {
		t.Fatalf("expected isReplay = true on duplicate request")
	}

	// 5. GET /transfers/{id}
	getTxReq := httptest.NewRequest(http.MethodGet, "/transfers/"+createdTx.TransferID, nil)
	getTxRecorder := httptest.NewRecorder()
	router.ServeHTTP(getTxRecorder, getTxReq)

	if getTxRecorder.Code != http.StatusOK {
		t.Fatalf("expected 200 OK on get transfer, got %d", getTxRecorder.Code)
	}

	// 6. GET /wallets/{id}
	getW1Req := httptest.NewRequest(http.MethodGet, "/wallets/hw_1", nil)
	getW1Recorder := httptest.NewRecorder()
	router.ServeHTTP(getW1Recorder, getW1Req)

	if getW1Recorder.Code != http.StatusOK {
		t.Fatalf("expected 200 OK on get wallet 1, got %d", getW1Recorder.Code)
	}
	var fetchedW1 domain.Wallet
	_ = json.Unmarshal(getW1Recorder.Body.Bytes(), &fetchedW1)
	if fetchedW1.Balance != 700 {
		t.Fatalf("expected wallet 1 balance 700, got %d", fetchedW1.Balance)
	}

	// 7. GET /wallets/{id}/ledger
	getLedgerReq := httptest.NewRequest(http.MethodGet, "/wallets/hw_1/ledger", nil)
	getLedgerRecorder := httptest.NewRecorder()
	router.ServeHTTP(getLedgerRecorder, getLedgerReq)

	if getLedgerRecorder.Code != http.StatusOK {
		t.Fatalf("expected 200 OK on get ledger, got %d", getLedgerRecorder.Code)
	}

	// 8. GET /wallets/{id}/reconcile
	getReconcileReq := httptest.NewRequest(http.MethodGet, "/wallets/hw_1/reconcile", nil)
	getReconcileRecorder := httptest.NewRecorder()
	router.ServeHTTP(getReconcileRecorder, getReconcileReq)

	if getReconcileRecorder.Code != http.StatusOK {
		t.Fatalf("expected 200 OK on reconcile, got %d", getReconcileRecorder.Code)
	}

	// 9. Insufficient funds transfer -> 422 Unprocessable Entity
	overdraftBody, _ := json.Marshal(service.CreateTransferRequest{
		IdempotencyKey: "overdraft_tx",
		FromWalletID:   "hw_1",
		ToWalletID:     "hw_2",
		Amount:         999999,
	})
	overdraftReq := httptest.NewRequest(http.MethodPost, "/transfers", bytes.NewReader(overdraftBody))
	overdraftReq.Header.Set("Content-Type", "application/json")
	overdraftRecorder := httptest.NewRecorder()
	router.ServeHTTP(overdraftRecorder, overdraftReq)

	if overdraftRecorder.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 Unprocessable Entity, got %d: %s", overdraftRecorder.Code, overdraftRecorder.Body.String())
	}

	// 10. Same wallet transfer -> 400 Bad Request
	selfTransferBody, _ := json.Marshal(service.CreateTransferRequest{
		IdempotencyKey: "self_tx",
		FromWalletID:   "hw_1",
		ToWalletID:     "hw_1",
		Amount:         50,
	})
	selfReq := httptest.NewRequest(http.MethodPost, "/transfers", bytes.NewReader(selfTransferBody))
	selfRecorder := httptest.NewRecorder()
	router.ServeHTTP(selfRecorder, selfReq)

	if selfRecorder.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 Bad Request on self transfer, got %d", selfRecorder.Code)
	}
}
