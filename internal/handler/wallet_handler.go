package handler

import (
	"encoding/json"
	"errors"
	"net/http"

	"wallet-transfer-assignment/internal/domain"
	"wallet-transfer-assignment/internal/service"
)

type WalletHandler struct {
	walletService *service.WalletService
}

func NewWalletHandler(walletService *service.WalletService) *WalletHandler {
	return &WalletHandler{walletService: walletService}
}

// CreateWallet handles POST /wallets
func (h *WalletHandler) CreateWallet(w http.ResponseWriter, r *http.Request) {
	var req service.CreateWalletRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}

	wallet, err := h.walletService.CreateWallet(r.Context(), req)
	if err != nil {
		WriteError(w, http.StatusBadRequest, err.Error())
		return
	}

	WriteJSON(w, http.StatusCreated, wallet)
}

// GetWallet handles GET /wallets/{wallet_id}
func (h *WalletHandler) GetWallet(w http.ResponseWriter, r *http.Request) {
	wallet_id := r.PathValue("wallet_id")
	if wallet_id == "" {
		WriteError(w, http.StatusBadRequest, "wallet id is required")
		return
	}

	wallet, err := h.walletService.GetWallet(r.Context(), wallet_id)
	if err != nil {
		if errors.Is(err, domain.ErrWalletNotFound) {
			WriteError(w, http.StatusNotFound, "wallet not found")
			return
		}
		WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}

	WriteJSON(w, http.StatusOK, wallet)
}

// GetWalletLedger handles GET /wallets/{wallet_id}/ledger
func (h *WalletHandler) GetWalletLedger(w http.ResponseWriter, r *http.Request) {
	wallet_id := r.PathValue("wallet_id")
	if wallet_id == "" {
		WriteError(w, http.StatusBadRequest, "wallet id is required")
		return
	}

	entries, err := h.walletService.GetWalletLedger(r.Context(), wallet_id)
	if err != nil {
		if errors.Is(err, domain.ErrWalletNotFound) {
			WriteError(w, http.StatusNotFound, "wallet not found")
			return
		}
		WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}

	WriteJSON(w, http.StatusOK, entries)
}

// ReconcileWallet handles GET /wallets/{wallet_id}/reconcile
func (h *WalletHandler) ReconcileWallet(w http.ResponseWriter, r *http.Request) {
	wallet_id := r.PathValue("wallet_id")
	if wallet_id == "" {
		WriteError(w, http.StatusBadRequest, "wallet id is required")
		return
	}

	storedBal, ledgerBal, isBalanced, err := h.walletService.ReconcileBalance(r.Context(), wallet_id)
	if err != nil {
		if errors.Is(err, domain.ErrWalletNotFound) {
			WriteError(w, http.StatusNotFound, "wallet not found")
			return
		}
		WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}

	WriteJSON(w, http.StatusOK, map[string]any{
		"walletId":      wallet_id,
		"storedBalance": storedBal,
		"ledgerBalance": ledgerBal,
		"isBalanced":    isBalanced,
	})
}
