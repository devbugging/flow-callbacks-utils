package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/onflow/flow/protobuf/go/flow/access"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

const (
	defaultPollingInterval = 10 * time.Second
	defaultPort            = "8080"
)

// Event represents a transaction scheduler event
type Event struct {
	ID              string  `json:"id"`
	Type            string  `json:"type"`
	TxID            uint64  `json:"tx_id"`
	Priority        uint8   `json:"priority"`
	Timestamp       float64 `json:"timestamp"`
	BlockHeight     uint64  `json:"block_height"`
	BlockTime       float64 `json:"block_time"`
	Data            string  `json:"data,omitempty"`
	Fees            float64 `json:"fees,omitempty"`
	Owner           string  `json:"owner,omitempty"`
	Handler         string  `json:"handler,omitempty"`
	ExecutionTxID   string  `json:"execution_tx_id,omitempty"`
	ExecutionStatus string  `json:"execution_status,omitempty"`
}

// TransactionQueryResult represents the detailed transaction information
type TransactionQueryResult struct {
	TransactionID   string                            `json:"transaction_id"`
	BlockID         string                            `json:"block_id"`
	BlockHeight     uint64                            `json:"block_height"`
	Status          string                            `json:"status"`
	ErrorMessage    string                            `json:"error_message,omitempty"`
	Events          []TransactionEvent                `json:"events"`
	ExecutionResult *access.TransactionResultResponse `json:"execution_result,omitempty"`
}

// TransactionEvent represents events emitted during transaction execution
type TransactionEvent struct {
	Type        string                 `json:"type"`
	Values      map[string]interface{} `json:"values"`
	BlockID     string                 `json:"block_id"`
	BlockHeight uint64                 `json:"block_height"`
	TxID        string                 `json:"transaction_id"`
	TxIndex     uint32                 `json:"transaction_index"`
	EventIndex  uint32                 `json:"event_index"`
}

// Database represents the local JSON database
type Database struct {
	LastBlock uint64  `json:"last_block"`
	Events    []Event `json:"events"`
}

// Monitor service configuration
type Monitor struct {
	network         string
	serviceAccount  string
	dbPath          string
	pollingInterval time.Duration
	port            string
	startHeight     uint64
	dbWasEmpty      bool // Track if database file existed when loaded
	db              *Database
	grpcClient      access.AccessAPIClient
	grpcConn        *grpc.ClientConn
}

// NewMonitor creates a new monitor instance
func NewMonitor(network, serviceAccount, dbPath string, pollingInterval time.Duration, port string, startHeight uint64) *Monitor {
	monitor := &Monitor{
		network:         network,
		serviceAccount:  serviceAccount,
		dbPath:          dbPath,
		pollingInterval: pollingInterval,
		port:            port,
		startHeight:     startHeight,
		db:              &Database{Events: []Event{}},
	}

	// initialize gRPC connection
	if err := monitor.initGRPCClient(); err != nil {
		log.Printf("Warning: Failed to initialize gRPC client: %v", err)
	}

	return monitor
}

// initGRPCClient initializes the gRPC client connection
func (m *Monitor) initGRPCClient() error {
	var accessNode string
	switch m.network {
	case "mainnet":
		accessNode = "access.mainnet.nodes.onflow.org:9000"
	case "migrationnet":
		accessNode = "access-001.migrationtestnet1.nodes.onflow.org:9000"
	case "testnet":
		accessNode = "access.devnet.nodes.onflow.org:9000"
	case "localnet", "emulator":
		accessNode = "localhost:3569"
	default:
		return fmt.Errorf("unsupported network: %s", m.network)
	}

	// establish gRPC connection
	conn, err := grpc.Dial(accessNode, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return fmt.Errorf("failed to connect to Flow Access API at %s: %v", accessNode, err)
	}

	m.grpcConn = conn
	m.grpcClient = access.NewAccessAPIClient(conn)
	return nil
}

// closeGRPCClient closes the gRPC connection
func (m *Monitor) closeGRPCClient() {
	if m.grpcConn != nil {
		m.grpcConn.Close()
	}
}

// loadDatabase loads the database from JSON file
func (m *Monitor) loadDatabase() error {
	if _, err := os.Stat(m.dbPath); os.IsNotExist(err) {
		// Database doesn't exist, start fresh
		m.dbWasEmpty = true
		if m.startHeight > 0 {
			log.Printf("Database file doesn't exist, will start from block height %d", m.startHeight)
		} else {
			log.Printf("Database file doesn't exist, will start from current block height")
		}
		return m.saveDatabase()
	}

	data, err := os.ReadFile(m.dbPath)
	if err != nil {
		return fmt.Errorf("failed to read database file: %v", err)
	}

	if err := json.Unmarshal(data, m.db); err != nil {
		return fmt.Errorf("failed to unmarshal database: %v", err)
	}

	// Database file exists - always use database values, ignore start height parameter
	m.dbWasEmpty = false
	if m.db.LastBlock == 0 {
		log.Printf("Database file exists but last_block is 0, will start from current block height")
	} else {
		log.Printf("Database file exists, will resume from block %d", m.db.LastBlock+1)
	}

	log.Printf("Loaded database with %d events, last block: %d", len(m.db.Events), m.db.LastBlock)
	return nil
}

// saveDatabase saves the database to JSON file
func (m *Monitor) saveDatabase() error {
	data, err := json.MarshalIndent(m.db, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal database: %v", err)
	}

	if err := os.WriteFile(m.dbPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write database file: %v", err)
	}

	return nil
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

// getBlockIDByHeight gets the block ID for a given block height
func (m *Monitor) getBlockIDByHeight(ctx context.Context, height uint64) (string, error) {
	if m.grpcClient == nil {
		return "", fmt.Errorf("gRPC client not initialized")
	}

	req := &access.GetBlockByHeightRequest{
		Height: height,
	}

	resp, err := m.grpcClient.GetBlockByHeight(ctx, req)
	if err != nil {
		return "", fmt.Errorf("failed to get block by height %d: %v", height, err)
	}

	return hex.EncodeToString(resp.Block.Id), nil
}

// queryTransactionDetails queries detailed transaction information using gRPC
func (m *Monitor) queryTransactionDetails(executionTxID string, blockHeight uint64) (*TransactionQueryResult, error) {
	if m.grpcClient == nil {
		return nil, fmt.Errorf("gRPC client not initialized")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// first get block ID by height
	blockID, err := m.getBlockIDByHeight(ctx, blockHeight)
	if err != nil {
		return nil, fmt.Errorf("failed to get block ID for height %d: %v", blockHeight, err)
	}

	// convert hex strings to bytes
	txIDBytes, err := hexToBytes(executionTxID)
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
	_, err = m.grpcClient.GetSystemTransaction(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to get system transaction: %v", err)
	}

	// get transaction result for the system transaction
	resultReq := &access.GetSystemTransactionResultRequest{
		Id:      txIDBytes,
		BlockId: blockIDBytes,
	}

	resultResp, err := m.grpcClient.GetSystemTransactionResult(ctx, resultReq)
	if err != nil {
		return nil, fmt.Errorf("failed to get system transaction result: %v", err)
	}

	result := &TransactionQueryResult{
		TransactionID:   executionTxID,
		BlockID:         blockID,
		BlockHeight:     blockHeight,
		Status:          resultResp.Status.String(),
		ErrorMessage:    resultResp.ErrorMessage,
		Events:          []TransactionEvent{},
		ExecutionResult: resultResp,
	}

	// parse events from transaction result
	for _, event := range resultResp.Events {
		txEvent := TransactionEvent{
			Type:        event.Type,
			Values:      m.parseEventPayload(event.Payload),
			BlockID:     blockID,
			BlockHeight: blockHeight,
			TxID:        executionTxID,
			TxIndex:     event.TransactionIndex,
			EventIndex:  event.EventIndex,
		}

		result.Events = append(result.Events, txEvent)
	}

	return result, nil
}

// parseEventPayload parses the event payload (simplified implementation)
func (m *Monitor) parseEventPayload(payload []byte) map[string]interface{} {
	result := make(map[string]interface{})

	// for now, just include the raw payload as a hex string
	// in a full implementation, you would decode the Cadence event payload
	if len(payload) > 0 {
		result["payload_hex"] = hex.EncodeToString(payload)
		result["payload_raw"] = string(payload)
	}

	return result
}

// getCurrentBlockHeight gets the current block height from Flow
func (m *Monitor) getCurrentBlockHeight() (uint64, error) {
	cmd := exec.Command("flow", "blocks", "get", "latest", "-n", m.network)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return 0, fmt.Errorf("failed to get latest block: %v, output: %s", err, output)
	}

	re := regexp.MustCompile(`Height\s+(\d+)`)
	matches := re.FindStringSubmatch(string(output))
	if len(matches) < 2 {
		return 0, fmt.Errorf("could not find block height in output: %s", output)
	}

	height, err := strconv.ParseUint(matches[1], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("failed to parse block height: %v", err)
	}

	return height, nil
}

// getBlockTimestamp gets the timestamp for a specific block
func (m *Monitor) getBlockTimestamp(blockNumber uint64) (float64, error) {
	cmd := exec.Command("flow", "blocks", "get", fmt.Sprintf("%d", blockNumber), "-n", m.network)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return 0, fmt.Errorf("failed to get block %d: %v", blockNumber, err)
	}

	re := regexp.MustCompile(`Proposal Timestamp Unix\s+([0-9.]+)`)
	matches := re.FindStringSubmatch(string(output))
	if len(matches) < 2 {
		return 0, fmt.Errorf("could not find proposal timestamp in block %d output", blockNumber)
	}

	timestamp, err := strconv.ParseFloat(matches[1], 64)
	if err != nil {
		return 0, fmt.Errorf("failed to parse timestamp: %v", err)
	}

	return timestamp, nil
}

// updateBlockTimestamps updates block timestamps for events efficiently
func (m *Monitor) updateBlockTimestamps(events []Event) {
	// Group events by block height to avoid duplicate requests
	blockMap := make(map[uint64][]int)
	for i, event := range events {
		if event.BlockTime == 0 { // Only update if not already set
			blockMap[event.BlockHeight] = append(blockMap[event.BlockHeight], i)
		}
	}

	// Fetch timestamps for each unique block
	for blockHeight, indices := range blockMap {
		if timestamp, err := m.getBlockTimestamp(blockHeight); err == nil {
			// Update all events for this block
			for _, idx := range indices {
				events[idx].BlockTime = timestamp
			}
		} else {
			log.Printf("Warning: Could not get timestamp for block %d: %v", blockHeight, err)
		}
	}
}

// getTransactionStatus gets the status of a transaction
func (m *Monitor) getTransactionStatus(txID string) string {
	cmd := exec.Command("flow", "transactions", "get", txID, "-n", m.network)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "UNKNOWN"
	}

	outputStr := string(output)
	if strings.Contains(outputStr, "Status") {
		if strings.Contains(outputStr, "SEALED") {
			return "SEALED"
		} else if strings.Contains(outputStr, "FAILED") || strings.Contains(outputStr, "ERROR") {
			return "FAILED"
		} else if strings.Contains(outputStr, "PENDING") {
			return "PENDING"
		}
	}

	return "UNKNOWN"
}

// fetchEvents fetches events from Flow blockchain
func (m *Monitor) fetchEvents(startBlock, endBlock uint64) (string, error) {
	// Remove 0x prefix from service account for event names
	account := strings.TrimPrefix(m.serviceAccount, "0x")

	cmd := exec.Command("flow", "events", "get",
		fmt.Sprintf("A.%s.FlowTransactionScheduler.Scheduled", account),
		fmt.Sprintf("A.%s.FlowTransactionScheduler.PendingExecution", account),
		fmt.Sprintf("A.%s.FlowTransactionScheduler.Executed", account),
		fmt.Sprintf("A.%s.FlowTransactionScheduler.Canceled", account),
		fmt.Sprintf("A.%s.FlowTransactionScheduler.CollectionLimitReached", account),
		fmt.Sprintf("A.%s.FlowTransactionScheduler.ConfigUpdated", account),
		"--start", fmt.Sprintf("%d", startBlock),
		"--end", fmt.Sprintf("%d", endBlock),
		"-n", m.network)

	// Use separate stdout and stderr to avoid potential issues with CombinedOutput
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if err != nil {
		return "", fmt.Errorf("failed to fetch events: %v, stderr: %s, stdout: %s", err, stderr.String(), stdout.String())
	}

	// Combine stdout and stderr manually, but prioritize stdout
	output := stdout.String()
	if stderr.String() != "" {
		output = stderr.String() + "\n" + output
	}

	return output, nil
}

// parseEvents parses events from Flow CLI output
func (m *Monitor) parseEvents(eventOutput string) ([]Event, error) {
	var events []Event
	lines := strings.Split(eventOutput, "\n")

	var currentBlockHeight uint64
	var currentBlockTime float64
	var currentEvent *Event

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		// Extract block information
		if strings.Contains(line, "Events Block #") {
			re := regexp.MustCompile(`Events Block #(\d+):`)
			matches := re.FindStringSubmatch(line)
			if len(matches) >= 2 {
				if blockHeight, err := strconv.ParseUint(matches[1], 10, 64); err == nil {
					currentBlockHeight = blockHeight
					// Get block timestamp
					if timestamp, err := m.getBlockTimestamp(blockHeight); err == nil {
						currentBlockTime = timestamp
					} else {
						// If we can't get the timestamp, log the error but continue
						log.Printf("Warning: Could not get timestamp for block %d: %v", blockHeight, err)
						currentBlockTime = 0
					}
				}
			}
			continue
		}

		// Events are now saved when we encounter a new event type

		// Parse the Tx ID line - this is the execution transaction ID for all events
		if strings.Contains(line, "Tx ID") && currentEvent != nil {
			re := regexp.MustCompile(`Tx ID\s+(.+)`)
			matches := re.FindStringSubmatch(line)
			if len(matches) >= 2 {
				executionTxID := strings.TrimSpace(matches[1])
				currentEvent.ExecutionTxID = executionTxID
				// Only get execution status for executed events (since others aren't executed yet)
				if currentEvent.Type == "executed" {
					currentEvent.ExecutionStatus = m.getTransactionStatus(executionTxID)
				}
			}
		}

		// Check for event types (look for Type line with the event)
		if strings.Contains(line, "Type") && strings.Contains(line, "FlowTransactionScheduler.") {
			// Save the current event before starting a new one
			if currentEvent != nil && currentEvent.ID != "" {
				events = append(events, *currentEvent)
			}

			// Create new event based on type
			if strings.Contains(line, "FlowTransactionScheduler.Scheduled") {
				currentEvent = &Event{
					Type:        "scheduled",
					BlockHeight: currentBlockHeight,
					BlockTime:   currentBlockTime,
				}
			} else if strings.Contains(line, "FlowTransactionScheduler.PendingExecution") {
				currentEvent = &Event{
					Type:        "pending",
					BlockHeight: currentBlockHeight,
					BlockTime:   currentBlockTime,
				}
			} else if strings.Contains(line, "FlowTransactionScheduler.Executed") {
				currentEvent = &Event{
					Type:        "executed",
					BlockHeight: currentBlockHeight,
					BlockTime:   currentBlockTime,
				}
			} else if strings.Contains(line, "FlowTransactionScheduler.Canceled") {
				currentEvent = &Event{
					Type:        "canceled",
					BlockHeight: currentBlockHeight,
					BlockTime:   currentBlockTime,
				}
			} else if strings.Contains(line, "FlowTransactionScheduler.CollectionLimitReached") {
				currentEvent = &Event{
					Type:        "collection_limit",
					BlockHeight: currentBlockHeight,
					BlockTime:   currentBlockTime,
				}
			} else if strings.Contains(line, "FlowTransactionScheduler.ConfigUpdated") {
				currentEvent = &Event{
					Type:        "config_updated",
					BlockHeight: currentBlockHeight,
					BlockTime:   currentBlockTime,
				}
			}
		}

		// Parse event fields
		if currentEvent != nil {
			if strings.Contains(line, "id (UInt64):") {
				re := regexp.MustCompile(`id \(UInt64\):\s*(\d+)`)
				matches := re.FindStringSubmatch(line)
				if len(matches) >= 2 {
					if txID, err := strconv.ParseUint(matches[1], 10, 64); err == nil {
						currentEvent.TxID = txID
						currentEvent.ID = fmt.Sprintf("%s-%d-%d", currentEvent.Type, txID, currentBlockHeight)
					}
				}
			} else if strings.Contains(line, "priority (UInt8):") {
				re := regexp.MustCompile(`priority \(UInt8\):\s*(\d+)`)
				matches := re.FindStringSubmatch(line)
				if len(matches) >= 2 {
					if priority, err := strconv.ParseUint(matches[1], 10, 8); err == nil {
						currentEvent.Priority = uint8(priority)
					}
				}
			} else if strings.Contains(line, "timestamp (UFix64):") {
				re := regexp.MustCompile(`timestamp \(UFix64\):\s*([0-9.]+)`)
				matches := re.FindStringSubmatch(line)
				if len(matches) >= 2 {
					if timestamp, err := strconv.ParseFloat(matches[1], 64); err == nil {
						currentEvent.Timestamp = timestamp
					}
				}
			} else if strings.Contains(line, "data (String):") {
				re := regexp.MustCompile(`data \(String\):\s*(.*)`)
				matches := re.FindStringSubmatch(line)
				if len(matches) >= 2 {
					data := strings.TrimSpace(matches[1])
					currentEvent.Data = strings.Trim(data, "\"")
				}
			} else if strings.Contains(line, "fees (UFix64):") || strings.Contains(line, "feesReturned (UFix64):") {
				re := regexp.MustCompile(`fees.*\(UFix64\):\s*([0-9.]+)`)
				matches := re.FindStringSubmatch(line)
				if len(matches) >= 2 {
					if fees, err := strconv.ParseFloat(matches[1], 64); err == nil {
						currentEvent.Fees = fees
					}
				}
			} else if strings.Contains(line, "transactionHandlerOwner (Address):") {
				re := regexp.MustCompile(`transactionHandlerOwner \(Address\):\s*(.*)`)
				matches := re.FindStringSubmatch(line)
				if len(matches) >= 2 {
					currentEvent.Owner = strings.TrimSpace(matches[1])
				}
			} else if strings.Contains(line, "transactionHandlerTypeIdentifier (String):") {
				re := regexp.MustCompile(`transactionHandlerTypeIdentifier \(String\):\s*(.*)`)
				matches := re.FindStringSubmatch(line)
				if len(matches) >= 2 {
					currentEvent.Handler = strings.Trim(strings.TrimSpace(matches[1]), "\"")
				}
			}

		}
	}

	// Add the last event if it exists
	if currentEvent != nil && currentEvent.ID != "" {
		events = append(events, *currentEvent)
	}
	return events, nil
}

// Start begins monitoring for events
func (m *Monitor) Start() error {
	// Load existing database
	if err := m.loadDatabase(); err != nil {
		return fmt.Errorf("failed to load database: %v", err)
	}

	// Start web server in a goroutine
	go m.startWebServer()

	log.Printf("Starting Flow Transaction Scheduler Monitor on %s network", m.network)
	log.Printf("Web interface available at http://localhost:%s", m.port)

	// Main monitoring loop
	for {
		if err := m.pollForEvents(); err != nil {
			log.Printf("Error polling for events: %v", err)
		}
		time.Sleep(m.pollingInterval)
	}
}

// pollForEvents polls for new events with pagination
func (m *Monitor) pollForEvents() error {
	currentHeight, err := m.getCurrentBlockHeight()
	if err != nil {
		return fmt.Errorf("failed to get current block height: %v", err)
	}

	// Start from last processed block + 1, or from start height only if database file didn't exist
	startBlock := m.db.LastBlock + 1
	if m.db.LastBlock == 0 {
		if m.dbWasEmpty && m.startHeight > 0 {
			startBlock = m.startHeight
			log.Printf("First run (no db file): starting from configured height %d", m.startHeight)
		} else {
			startBlock = currentHeight
			if m.dbWasEmpty {
				log.Printf("First run (no db file): starting from current height %d", currentHeight)
			} else {
				log.Printf("Database exists but last_block=0: starting from current height %d", currentHeight)
			}
		}
	}

	// Only fetch if there are new blocks
	if startBlock > currentHeight {
		return nil
	}

	// Process events in chunks of 100 blocks to avoid Flow CLI limits
	const maxBlocksPerQuery = 100
	totalNewEvents := 0

	log.Printf("Scanning blocks %d to %d for events...", startBlock, currentHeight)

	for start := startBlock; start <= currentHeight; {
		end := start + maxBlocksPerQuery - 1
		if end > currentHeight {
			end = currentHeight
		}

		eventOutput, err := m.fetchEvents(start, end)
		if err != nil {
			return fmt.Errorf("failed to fetch events for blocks %d-%d: %v", start, end, err)
		}

		newEvents, err := m.parseEvents(eventOutput)
		if err != nil {
			return fmt.Errorf("failed to parse events for blocks %d-%d: %v", start, end, err)
		}

		if len(newEvents) > 0 {
			log.Printf("Found %d new events in blocks %d-%d", len(newEvents), start, end)

			// Update block timestamps for new events
			m.updateBlockTimestamps(newEvents)

			// Execution transaction IDs are now parsed directly from events

			m.db.Events = append(m.db.Events, newEvents...)
			totalNewEvents += len(newEvents)
		}

		// Update last processed block after each chunk
		m.db.LastBlock = end
		if err := m.saveDatabase(); err != nil {
			return fmt.Errorf("failed to save database after processing blocks %d-%d: %v", start, end, err)
		}

		// Move to next chunk
		start = end + 1
	}

	if totalNewEvents > 0 {
		log.Printf("Total events found in this poll: %d", totalNewEvents)
	}

	return nil
}

// startWebServer starts the HTTP server for the web interface
func (m *Monitor) startWebServer() {
	http.HandleFunc("/", m.handleIndex)
	http.HandleFunc("/api/events", m.handleEvents)
	http.HandleFunc("/api/stats", m.handleStats)
	http.HandleFunc("/api/transaction", m.handleTransactionQuery)

	log.Printf("Web server starting on port %s", m.port)
	if err := http.ListenAndServe(":"+m.port, nil); err != nil {
		log.Fatalf("Failed to start web server: %v", err)
	}
}

// handleIndex serves the main HTML page
func (m *Monitor) handleIndex(w http.ResponseWriter, r *http.Request) {
	tmpl, err := template.ParseFiles("index.html")
	if err != nil {
		http.Error(w, fmt.Sprintf("Error loading template: %v", err), http.StatusInternalServerError)
		return
	}

	data := struct {
		Network        string
		ServiceAccount string
	}{
		Network:        m.network,
		ServiceAccount: m.serviceAccount,
	}

	w.Header().Set("Content-Type", "text/html")
	if err := tmpl.Execute(w, data); err != nil {
		http.Error(w, fmt.Sprintf("Error executing template: %v", err), http.StatusInternalServerError)
	}
	return
}

// handleEvents serves the events API endpoint
func (m *Monitor) handleEvents(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	if err := json.NewEncoder(w).Encode(m.db.Events); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// handleStats serves the statistics API endpoint
func (m *Monitor) handleStats(w http.ResponseWriter, r *http.Request) {
	stats := make(map[string]interface{})
	stats["lastBlock"] = m.db.LastBlock

	// Count events by type and execution status
	typeCounts := make(map[string]int)
	successfulExecutions := 0
	failedExecutions := 0

	for _, event := range m.db.Events {
		typeCounts[event.Type]++

		// Count execution status for executed events
		if event.Type == "executed" && event.ExecutionStatus != "" {
			if event.ExecutionStatus == "SEALED" {
				successfulExecutions++
			} else if event.ExecutionStatus == "FAILED" {
				failedExecutions++
			}
		}
	}

	stats["scheduled"] = typeCounts["scheduled"]
	stats["pending"] = typeCounts["pending"]
	stats["executed"] = typeCounts["executed"]
	stats["canceled"] = typeCounts["canceled"]
	stats["collection_limit"] = typeCounts["collection_limit"]
	stats["config_updated"] = typeCounts["config_updated"]
	stats["total"] = len(m.db.Events)
	stats["successful_executions"] = successfulExecutions
	stats["failed_executions"] = failedExecutions

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	if err := json.NewEncoder(w).Encode(stats); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// handleTransactionQuery serves the transaction query API endpoint
func (m *Monitor) handleTransactionQuery(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		ExecutionTxID string `json:"execution_tx_id"`
		BlockHeight   uint64 `json:"block_height"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf("Invalid request body: %v", err), http.StatusBadRequest)
		return
	}

	if req.ExecutionTxID == "" {
		http.Error(w, "execution_tx_id is required", http.StatusBadRequest)
		return
	}

	if req.BlockHeight == 0 {
		http.Error(w, "block_height is required", http.StatusBadRequest)
		return
	}

	result, err := m.queryTransactionDetails(req.ExecutionTxID, req.BlockHeight)
	if err != nil {
		log.Printf("Error querying transaction details: %v", err)
		http.Error(w, fmt.Sprintf("Failed to query transaction: %v", err), http.StatusInternalServerError)
		return
	}

	if err := json.NewEncoder(w).Encode(result); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func main() {
	var (
		network         = flag.String("network", "localnet", "Flow network to connect to (localnet, testnet, mainnet, emulator)")
		serviceAccount  = flag.String("account", "", "Service account address (required)")
		dbPath          = flag.String("db", "db.json", "Path to local database file")
		pollingInterval = flag.Duration("interval", defaultPollingInterval, "Polling interval for new events")
		port            = flag.String("port", defaultPort, "Web server port")
		startHeight     = flag.Uint64("start", 0, "Starting block height to monitor from (only used when database is empty)")
	)
	flag.Parse()

	// Validate required flags
	if *serviceAccount == "" {
		log.Fatalf("Service account address is required. Use -account flag to specify it.")
	}

	monitor := NewMonitor(*network, *serviceAccount, *dbPath, *pollingInterval, *port, *startHeight)

	// ensure gRPC connection is closed on exit
	defer monitor.closeGRPCClient()

	if err := monitor.Start(); err != nil {
		log.Fatalf("Failed to start monitor: %v", err)
	}
}
