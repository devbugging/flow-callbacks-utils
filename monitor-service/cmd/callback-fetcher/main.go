package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/onflow/flow/protobuf/go/flow/access"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// CallbackResult represents the callback execution information
type CallbackResult struct {
	TransactionID   string                            `json:"transaction_id"`
	BlockID         string                            `json:"block_id"`
	BlockHeight     uint64                            `json:"block_height"`
	Status          string                            `json:"status"`
	ErrorMessage    string                            `json:"error_message,omitempty"`
	Events          []CallbackEvent                   `json:"events"`
	ExecutionResult *access.TransactionResultResponse `json:"execution_result,omitempty"`
}

// CallbackEvent represents events emitted during callback execution
type CallbackEvent struct {
	Type        string                 `json:"type"`
	Values      map[string]interface{} `json:"values"`
	BlockID     string                 `json:"block_id"`
	BlockHeight uint64                 `json:"block_height"`
	TxID        string                 `json:"transaction_id"`
	TxIndex     uint32                 `json:"transaction_index"`
	EventIndex  uint32                 `json:"event_index"`
}

// CallbackFetcher handles fetching callback execution data using Flow protobuf gRPC API
type CallbackFetcher struct {
	client  access.AccessAPIClient
	conn    *grpc.ClientConn
	network string
}

// NewCallbackFetcher creates a new callback fetcher instance
func NewCallbackFetcher(network string) (*CallbackFetcher, error) {
	var accessNode string
	switch network {
	case "mainnet":
		accessNode = "access.mainnet.nodes.onflow.org:9000"
	case "migrationnet":
		accessNode = "access-001.migrationtestnet1.nodes.onflow.org:9000"
	case "testnet":
		accessNode = "access.devnet.nodes.onflow.org:9000"
	case "localnet", "emulator":
		accessNode = "localhost:3569"
	default:
		return nil, fmt.Errorf("unsupported network: %s", network)
	}

	// establish gRPC connection
	conn, err := grpc.Dial(accessNode, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, fmt.Errorf("failed to connect to Flow Access API at %s: %v", accessNode, err)
	}

	client := access.NewAccessAPIClient(conn)

	return &CallbackFetcher{
		client:  client,
		conn:    conn,
		network: network,
	}, nil
}

// Close closes the gRPC connection
func (cf *CallbackFetcher) Close() {
	if cf.conn != nil {
		cf.conn.Close()
	}
}

// hexToBytes converts a hex string to bytes, handling both 0x-prefixed and plain hex
func hexToBytes(hexStr string) ([]byte, error) {
	// remove 0x prefix if present
	if strings.HasPrefix(hexStr, "0x") {
		hexStr = hexStr[2:]
	}

	// validate length (should be 64 characters for Flow IDs)
	if len(hexStr) != 64 {
		return nil, fmt.Errorf("invalid hex string length: expected 64 characters, got %d", len(hexStr))
	}

	return hex.DecodeString(hexStr)
}

// getSystemTransaction fetches system transaction using GetSystemTransaction method
func (cf *CallbackFetcher) getSystemTransaction(ctx context.Context, txID string, blockID string) (*CallbackResult, error) {
	// convert hex strings to bytes
	txIDBytes, err := hexToBytes(txID)
	if err != nil {
		return nil, fmt.Errorf("invalid transaction ID format: %v", err)
	}
	blockIDBytes, err := hexToBytes(blockID)
	if err != nil {
		return nil, fmt.Errorf("invalid block ID format: %v", err)
	}

	// create GetSystemTransactionRequest
	req := &access.GetSystemTransactionRequest{
		Id:      txIDBytes,
		BlockId: blockIDBytes,
	}

	// call GetSystemTransaction
	_, err = cf.client.GetSystemTransaction(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to get system transaction: %v", err)
	}

	// get transaction result for the system transaction
	resultReq := &access.GetSystemTransactionResultRequest{
		Id:      txIDBytes,
		BlockId: blockIDBytes,
	}

	resultResp, err := cf.client.GetSystemTransactionResult(ctx, resultReq)
	if err != nil {
		return nil, fmt.Errorf("failed to get system transaction result: %v", err)
	}

	// get block details for block height
	blockReq := &access.GetBlockByIDRequest{
		Id: blockIDBytes,
	}

	blockResp, err := cf.client.GetBlockByID(ctx, blockReq)
	if err != nil {
		return nil, fmt.Errorf("failed to get block details: %v", err)
	}

	callbackResult := &CallbackResult{
		TransactionID:   txID,
		BlockID:         blockID,
		BlockHeight:     blockResp.Block.Height,
		Status:          resultResp.Status.String(),
		ErrorMessage:    resultResp.ErrorMessage,
		Events:          []CallbackEvent{},
		ExecutionResult: resultResp,
	}

	// parse events from transaction result
	for _, event := range resultResp.Events {
		callbackEvent := CallbackEvent{
			Type:        event.Type,
			Values:      cf.parseEventPayload(event.Payload),
			BlockID:     blockID,
			BlockHeight: blockResp.Block.Height,
			TxID:        txID,
			TxIndex:     event.TransactionIndex,
			EventIndex:  event.EventIndex,
		}

		callbackResult.Events = append(callbackResult.Events, callbackEvent)
	}

	return callbackResult, nil
}

// parseEventPayload parses the event payload (simplified implementation)
func (cf *CallbackFetcher) parseEventPayload(payload []byte) map[string]interface{} {
	result := make(map[string]interface{})

	// for now, just include the raw payload as a hex string
	// in a full implementation, you would decode the Cadence event payload
	if len(payload) > 0 {
		result["payload_hex"] = hex.EncodeToString(payload)
		result["payload_raw"] = string(payload)
	}

	return result
}

// fetchCallbackByTxAndBlockID fetches callback execution information by transaction ID and block ID
func (cf *CallbackFetcher) fetchCallbackByTxAndBlockID(txID string, blockID string) (*CallbackResult, error) {
	log.Printf("Fetching system transaction callback for transaction ID: %s in block: %s", txID, blockID)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	result, err := cf.getSystemTransaction(ctx, txID, blockID)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch system transaction: %v", err)
	}

	log.Printf("Transaction status: %s", result.Status)
	log.Printf("Block height: %d", result.BlockHeight)
	log.Printf("Found %d events", len(result.Events))

	return result, nil
}

// printResult prints the callback result in a formatted way
func (cf *CallbackFetcher) printResult(result *CallbackResult, format string) error {
	switch format {
	case "json":
		data, err := json.MarshalIndent(result, "", "  ")
		if err != nil {
			return fmt.Errorf("failed to marshal result to JSON: %v", err)
		}
		fmt.Println(string(data))

	case "table":
		fmt.Printf("Transaction ID: %s\n", result.TransactionID)
		fmt.Printf("Block ID: %s\n", result.BlockID)
		fmt.Printf("Block Height: %d\n", result.BlockHeight)
		fmt.Printf("Status: %s\n", result.Status)
		if result.ErrorMessage != "" {
			fmt.Printf("Error: %s\n", result.ErrorMessage)
		}

		if len(result.Events) > 0 {
			fmt.Printf("\nEvents (%d):\n", len(result.Events))
			for i, event := range result.Events {
				fmt.Printf("  %d. Type: %s\n", i+1, event.Type)
				fmt.Printf("     Block Height: %d\n", event.BlockHeight)
				fmt.Printf("     Transaction Index: %d\n", event.TxIndex)
				fmt.Printf("     Event Index: %d\n", event.EventIndex)
				if len(event.Values) > 0 {
					fmt.Printf("     Values:\n")
					for k, v := range event.Values {
						fmt.Printf("       %s: %v\n", k, v)
					}
				}
			}
		}

		// show execution details if available
		if result.ExecutionResult != nil {
			fmt.Printf("\nExecution Details:\n")
			fmt.Printf("  Status: %s\n", result.ExecutionResult.Status.String())
			if result.ExecutionResult.ErrorMessage != "" {
				fmt.Printf("  Error Message: %s\n", result.ExecutionResult.ErrorMessage)
			}
		}

	default:
		return fmt.Errorf("unsupported format: %s", format)
	}

	return nil
}

func main() {
	var (
		network = flag.String("network", "localnet", "Flow network to connect to (localnet, testnet, mainnet, emulator)")
		txID    = flag.String("tx", "", "Transaction ID to fetch callback for (required)")
		blockID = flag.String("block", "", "Block ID to fetch transaction from (required)")
		format  = flag.String("format", "table", "Output format (json, table)")
	)
	flag.Parse()

	// validate required flags
	if *txID == "" {
		log.Fatalf("Transaction ID is required. Use -tx flag to specify it.")
	}

	if *blockID == "" {
		log.Fatalf("Block ID is required. Use -block flag to specify it.")
	}

	// validate transaction ID format
	if !strings.HasPrefix(*txID, "0x") && len(*txID) != 64 {
		log.Fatalf("Invalid transaction ID format. Expected 64-character hex string or 0x-prefixed hex string.")
	}

	// validate block ID format
	if !strings.HasPrefix(*blockID, "0x") && len(*blockID) != 64 {
		log.Fatalf("Invalid block ID format. Expected 64-character hex string or 0x-prefixed hex string.")
	}

	fetcher, err := NewCallbackFetcher(*network)
	if err != nil {
		log.Fatalf("Failed to create callback fetcher: %v", err)
	}
	defer fetcher.Close()
	// fetch system transaction with both transaction ID and block ID
	result, err := fetcher.fetchCallbackByTxAndBlockID(*txID, *blockID)
	if err != nil {
		log.Fatalf("Failed to fetch callback: %v", err)
	}

	if err := fetcher.printResult(result, *format); err != nil {
		log.Fatalf("Failed to print result: %v", err)
	}
}
