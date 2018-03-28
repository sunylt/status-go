package utils

import (
	"context"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/pborman/uuid"
)

type contextKey string // in order to make sure that our context key does not collide with keys from other packages

const (
	// MessageIDKey is a key for message ID
	// This ID is required to track from which chat a given send transaction request is coming.
	MessageIDKey = contextKey("message_id")
)

// RawDiscardTransactionResult is list of results from CompleteTransactions() (used internally)
type RawDiscardTransactionResult struct {
	Error error
}

// QueuedTxID queued transaction identifier
type QueuedTxID string

// QueuedTx holds enough information to complete the queued transaction.
type QueuedTx struct {
	ID      QueuedTxID
	Context context.Context
	Args    SendTxArgs
	Result  chan TransactionResult
}

// SendTxArgs represents the arguments to submit a new transaction into the transaction pool.
// This struct is based on go-ethereum's type in internal/ethapi/api.go, but we have freedom
// over the exact layout of this struct.
type SendTxArgs struct {
	From     common.Address  `json:"from"`
	To       *common.Address `json:"to"`
	Gas      *hexutil.Uint64 `json:"gas"`
	GasPrice *hexutil.Big    `json:"gasPrice"`
	Value    *hexutil.Big    `json:"value"`
	Nonce    *hexutil.Uint64 `json:"nonce"`
	Input    hexutil.Bytes   `json:"input"`
}

// TransactionResult is a JSON returned from transaction complete function (used internally)
type TransactionResult struct {
	Hash  common.Hash
	Error error
}

// CompleteTransactionResult is a JSON returned from transaction complete function (used in exposed method)
type CompleteTransactionResult struct {
	ID    string `json:"id"`
	Hash  string `json:"hash"`
	Error string `json:"error"`
}

// CompleteTransactionsResult is list of results from CompleteTransactions() (used in exposed method)
type CompleteTransactionsResult struct {
	Results map[string]CompleteTransactionResult `json:"results"`
}

// DiscardTransactionResult is a JSON returned from transaction discard function
type DiscardTransactionResult struct {
	ID    string `json:"id"`
	Error string `json:"error"`
}

// DiscardTransactionsResult is a list of results from DiscardTransactions()
type DiscardTransactionsResult struct {
	Results map[string]DiscardTransactionResult `json:"results"`
}

// CreateTransaction returns a transaction object.
func CreateTransaction(ctx context.Context, args SendTxArgs) *QueuedTx {
	return &QueuedTx{
		ID:      QueuedTxID(uuid.New()),
		Context: ctx,
		Args:    args,
		Result:  make(chan TransactionResult, 1),
	}
}

// MessageIDFromContext returns message id from context (if exists)
func MessageIDFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	if messageID, ok := ctx.Value(MessageIDKey).(string); ok {
		return messageID
	}

	return ""
}
