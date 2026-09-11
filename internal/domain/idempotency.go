package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"
)

// IdempotencyStatus tracks the execution phase of an idempotent operation.
type IdempotencyStatus string

const (
	IdempotencyStatusInProgress IdempotencyStatus = "IN_PROGRESS"
	IdempotencyStatusCompleted  IdempotencyStatus = "COMPLETED"
	IdempotencyStatusFailed     IdempotencyStatus = "FAILED"
)

// IdempotencyRecord stores request fingerprints and cached responses for duplicate request deduplication.
type IdempotencyRecord struct {
	IdempotencyKey string            `json:"idempotencyKey"`
	RequestHash    string            `json:"requestHash"`
	TransferID     *string           `json:"transferId,omitempty"`
	Status         IdempotencyStatus `json:"status"`
	ResponseCode   int               `json:"responseCode"`
	ResponseBody   string            `json:"responseBody"`
	CreatedAt      time.Time         `json:"createdAt"`
	UpdatedAt      time.Time         `json:"updatedAt"`
}

// ComputeRequestHash creates a deterministic SHA-256 digest of transfer parameters.
func ComputeRequestHash(fromWalletID, toWalletID string, amount int64) string {
	payload := fmt.Sprintf("%s:%s:%d", fromWalletID, toWalletID, amount)
	hash := sha256.Sum256([]byte(payload))
	return hex.EncodeToString(hash[:])
}
