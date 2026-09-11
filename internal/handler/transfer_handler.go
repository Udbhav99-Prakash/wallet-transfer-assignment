package handler

import (
	"encoding/json"
	"errors"
	"net/http"

	"wallet-transfer-assignment/internal/domain"
	"wallet-transfer-assignment/internal/service"
)

type TransferHandler struct {
	transferService *service.TransferService
}

func NewTransferHandler(transferService *service.TransferService) *TransferHandler {
	return &TransferHandler{transferService: transferService}
}

// CreateTransfer handles POST /transfers
func (h *TransferHandler) CreateTransfer(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Content-Type") != "application/json" && r.Header.Get("Content-Type") != "" {
		// Allow standard JSON
	}

	var req service.CreateTransferRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}

	resp, err := h.transferService.ExecuteTransfer(r.Context(), req)
	if err != nil {
		switch {
		case errors.Is(err, domain.ErrInsufficientFunds):
			// Return 422 with the transfer failure details if available
			if resp != nil {
				WriteJSON(w, http.StatusUnprocessableEntity, resp)
				return
			}
			WriteError(w, http.StatusUnprocessableEntity, err.Error())
			return

		case errors.Is(err, domain.ErrWalletNotFound):
			WriteError(w, http.StatusNotFound, err.Error())
			return

		case errors.Is(err, domain.ErrSameWalletTransfer), errors.Is(err, domain.ErrInvalidAmount):
			WriteError(w, http.StatusBadRequest, err.Error())
			return

		case errors.Is(err, domain.ErrIdempotencyConflict):
			WriteError(w, http.StatusConflict, err.Error())
			return

		case errors.Is(err, domain.ErrIdempotencyInProgress):
			WriteError(w, http.StatusConflict, err.Error())
			return

		default:
			WriteError(w, http.StatusInternalServerError, "internal server error: "+err.Error())
			return
		}
	}

	// If it's a replayed idempotent request, return 200 OK. If newly processed, return 201 Created.
	if resp.IsReplay {
		WriteJSON(w, http.StatusOK, resp)
		return
	}
	WriteJSON(w, http.StatusCreated, resp)
}

// GetTransfer handles GET /transfers/{transfer_id}
func (h *TransferHandler) GetTransfer(w http.ResponseWriter, r *http.Request) {
	transfer_id := r.PathValue("transfer_id")
	if transfer_id == "" {
		WriteError(w, http.StatusBadRequest, "transfer id is required")
		return
	}

	transfer, err := h.transferService.GetTransfer(r.Context(), transfer_id)
	if err != nil {
		WriteError(w, http.StatusNotFound, "transfer not found")
		return
	}

	WriteJSON(w, http.StatusOK, transfer)
}
