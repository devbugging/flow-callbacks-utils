package main

import (
	"context"
	"fmt"
	cryptorand "crypto/rand"
	mathrand "math/rand"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onflow/cadence"
	"github.com/onflow/flow-go-sdk"
	"github.com/onflow/flow-go-sdk/crypto"
	"github.com/onflow/flowkit/v2"
	"github.com/onflow/flowkit/v2/accounts"
	"github.com/onflow/flowkit/v2/config"
	"github.com/onflow/flowkit/v2/gateway"
	"github.com/onflow/flowkit/v2/output"
	"github.com/onflow/flowkit/v2/transactions"
	"github.com/spf13/afero"
)

type StressTest struct {
	t                   *testing.T
	startBlockHeight    uint64
	executedCallbackIDs []uint64 // Track which callbacks were actually executed
	scheduledCallbacks  []ScheduledCallback
	mu                  sync.Mutex // Protect concurrent access

	// Flowkit components
	flowkit        *flowkit.Flowkit
	ctx            context.Context
	network        config.Network
	account        *accounts.Account
	serviceAccount *flow.Account
	scheduleScript string

	keyCount        int
	testAccount     *flow.Account
	testAccountKeys []crypto.PrivateKey
	keyIndex        *atomic.Uint32 // Track which key to use next
}

func NewStressTest(t *testing.T) *StressTest {
	st := &StressTest{
		t:                   t,
		executedCallbackIDs: make([]uint64, 0),
		scheduledCallbacks:  make([]ScheduledCallback, 0),
		keyIndex:            &atomic.Uint32{},
		ctx:                 context.Background(),
	}

	// Initialize flowkit
	err := st.initializeFlowkit()
	if err != nil {
		t.Fatalf("Failed to initialize flowkit: %v", err)
	}

	// Get the current block height
	startHeight, err := st.getCurrentBlockHeight()
	if err != nil {
		t.Fatalf("Failed to get current block height: %v", err)
	}
	st.startBlockHeight = startHeight

	st.keyCount = 20
	err = st.createTestAccountMultipleKeys()
	if err != nil {
		t.Fatalf("Failed to create test account with multiple keys: %v", err)
	}

	// Load the schedule script
	err = st.loadScheduleScript()
	if err != nil {
		t.Fatalf("Failed to load schedule script: %v", err)
	}

	t.Logf("Starting stress test from block height %d with %d keys available", startHeight, st.keyCount)

	return st
}

func (st *StressTest) initializeFlowkit() error {
	// Load flow.json configuration
	readerWriter := afero.Afero{Fs: afero.NewOsFs()}
	state, err := flowkit.Load([]string{"flow.json"}, readerWriter)
	if err != nil {
		return fmt.Errorf("failed to load flow.json: %w", err)
	}

	// Get testnet network
	network, err := state.Networks().ByName("testnet")
	if err != nil {
		return fmt.Errorf("failed to get testnet network: %w", err)
	}
	st.network = *network

	// Initialize gateway
	gw, err := gateway.NewGrpcGateway(config.Network{Host: network.Host})
	if err != nil {
		return fmt.Errorf("failed to create gateway: %w", err)
	}

	// Initialize logger - simplified for stress testing
	logger := output.NewStdoutLogger(output.NoneLog)

	// Create flowkit instance
	st.flowkit = flowkit.NewFlowkit(state, *network, gw, logger)

	// Get service account
	account, err := state.Accounts().ByName("test")
	if err != nil {
		return fmt.Errorf("failed to get test account: %w", err)
	}

	// Get the Flow account details
	flowAccount, err := st.flowkit.GetAccount(st.ctx, account.Address)
	if err != nil {
		return fmt.Errorf("failed to get Flow account: %w", err)
	}
	st.serviceAccount = flowAccount
	st.account = account

	return nil
}

func (st *StressTest) createTestAccountMultipleKeys() error {
	randSeed := make([]byte, 32)
	cryptorand.Read(randSeed)
	publicKeys := make([]accounts.PublicKey, st.keyCount)
	st.testAccountKeys = make([]crypto.PrivateKey, 0, st.keyCount)

	for i := 0; i < st.keyCount; i++ {
		pkey, err := st.flowkit.GenerateKey(st.ctx, crypto.ECDSA_P256, string(randSeed))
		if err != nil {
			return fmt.Errorf("failed to generate key: %w", err)
		}

		st.testAccountKeys = append(st.testAccountKeys, pkey)

		publicKeys[i] = accounts.PublicKey{
			Public:   pkey.PublicKey(),
			Weight:   flow.AccountKeyWeightThreshold,
			SigAlgo:  crypto.ECDSA_P256,
			HashAlgo: crypto.SHA2_256,
		}
	}

	testAccount, _, err := st.flowkit.CreateAccount(
		st.ctx,
		st.account,
		publicKeys,
	)
	if err != nil {
		return fmt.Errorf("failed to create test account: %w", err)
	}

	st.testAccount = testAccount
	st.t.Logf("Created test account with address %s and %d keys", testAccount.Address.Hex(), st.keyCount)

	// Fund the new account with 1 Flow token
	err = st.fundTestAccount()
	if err != nil {
		return fmt.Errorf("failed to fund test account: %w", err)
	}

	return nil
}

func (st *StressTest) fundTestAccount() error {
	st.t.Logf("Funding test account %s with 1.0 Flow tokens", st.testAccount.Address.Hex())

	// Create Flow token transfer script (Cadence 1.0 syntax)
	fundScript := `
import FungibleToken from 0x9a0766d93b6608b7
import FlowToken from 0x7e60df042a9c0868

transaction(amount: UFix64, to: Address) {
    let vault: @{FungibleToken.Vault}

    prepare(signer: auth(BorrowValue) &Account) {
        self.vault <- signer.storage.borrow<auth(FungibleToken.Withdraw) &FlowToken.Vault>(
            from: /storage/flowTokenVault
        )!.withdraw(amount: amount)
    }

    execute {
        let recipient = getAccount(to)
        let receiverRef = recipient.capabilities.get<&{FungibleToken.Receiver}>(
            /public/flowTokenReceiver
        ).borrow() ?? panic("Unable to borrow receiver reference")

        receiverRef.deposit(from: <-self.vault)
    }
}
`

	// Prepare transfer arguments (1.0 Flow = 100000000 in Flow's 8-decimal precision)
	args := []cadence.Value{
		cadence.UFix64(100000000), // 1.0 Flow
		cadence.NewAddress(st.testAccount.Address),
	}

	// Create script
	script := flowkit.Script{
		Code: []byte(fundScript),
		Args: args,
	}

	// Create account roles for transaction (use service account as payer)
	acctRoles := transactions.AccountRoles{
		Proposer:    *st.account,
		Authorizers: []accounts.Account{*st.account},
		Payer:       *st.account,
	}

	// Send funding transaction
	_, result, err := st.flowkit.SendTransaction(
		st.ctx,
		acctRoles,
		script,
		9999, // gas limit
	)

	if err != nil {
		st.t.Logf("Fund transaction failed: %v", err)
		return fmt.Errorf("failed to send fund transaction: %w", err)
	}

	// Check for transaction errors
	if result.Error != nil {
		st.t.Logf("Fund transaction had execution error: %v", result.Error)
		return fmt.Errorf("fund transaction failed with execution error: %w", result.Error)
	}

	st.t.Logf("Successfully funded test account with 1.0 Flow tokens")
	return nil
}

func (st *StressTest) loadScheduleScript() error {
	scriptBytes, err := os.ReadFile("schedule_with_storage_testnet.cdc")
	if err != nil {
		return fmt.Errorf("failed to read schedule script: %w", err)
	}
	st.scheduleScript = string(scriptBytes)
	return nil
}

// getNextKeyIndex returns the next key index to use and rotates through available keys
func (st *StressTest) getNextKeyIndex() int {
	currentIndex := st.keyIndex.Add(1) - 1
	return int(currentIndex) % st.keyCount
}

func (st *StressTest) scheduleCallbackWithNextKey(data string, priority uint8, futureSeconds int, effort string) (ScheduledCallback, error) {
	// Get next available key index
	keyIndex := st.getNextKeyIndex()
	st.t.Logf("Using key index %d for transaction", keyIndex)
	return st.scheduleCallbackWithKey(data, priority, futureSeconds, effort, keyIndex)
}

func (st *StressTest) scheduleCallbackWithKey(data string, priority uint8, futureSeconds int, effort string, keyIndex int) (ScheduledCallback, error) {
	timestamp := float64(time.Now().Unix() + int64(futureSeconds))

	st.t.Logf("Scheduling callback with data '%s' at timestamp %.1f, key index %d", data, timestamp, keyIndex)

	// Prepare transaction arguments
	effortUint, _ := strconv.ParseUint(effort, 10, 64)
	feeAmount, _ := strconv.ParseFloat("0.1", 64)

	arguments := []cadence.Value{
		cadence.UFix64(uint64(timestamp * 100000000)), // UFix64 (convert to Flow's 8-decimal precision)
		cadence.UFix64(uint64(feeAmount * 100000000)), // UFix64 (convert to Flow units)
		cadence.UInt64(effortUint),                    // UInt64
		cadence.UInt8(priority),                       // UInt8
		cadence.String(data),                          // String
	}

	// Create a dynamic account for this specific key
	hexKey := accounts.NewHexKeyFromPrivateKey(uint32(keyIndex), crypto.SHA2_256, st.testAccountKeys[keyIndex])
	testAccount := &accounts.Account{
		Name:    fmt.Sprintf("dynamic-key-%d", keyIndex),
		Address: st.testAccount.Address,
		Key:     hexKey,
	}

	// Create script
	script := flowkit.Script{
		Code: []byte(st.scheduleScript),
		Args: arguments,
	}

	// Create account roles for transaction using the specific key
	acctRoles := transactions.AccountRoles{
		Proposer:    *testAccount,
		Authorizers: []accounts.Account{*testAccount},
		Payer:       *testAccount,
	}

	// Send transaction using flowkit
	_, result, err := st.flowkit.SendTransaction(
		st.ctx,
		acctRoles,
		script,
		9999, // gas limit
	)

	if err != nil {
		st.t.Logf("Schedule transaction failed: %v", err)
		return ScheduledCallback{}, fmt.Errorf("failed to schedule callback: %w", err)
	}

	// Check for transaction errors
	if result.Error != nil {
		st.t.Logf("Schedule transaction had execution error: %v", result.Error)
		return ScheduledCallback{}, fmt.Errorf("schedule transaction failed with execution error: %w", result.Error)
	}

	// Extract transaction ID from events
	txID := st.extractTransactionIDFromEvents(result.Events)
	if txID == 0 {
		st.t.Logf("Failed to extract transaction ID from events: %+v", result.Events)
		return ScheduledCallback{}, fmt.Errorf("failed to extract transaction ID from events")
	}

	callback := ScheduledCallback{
		Data:      data,
		Priority:  priority,
		Timestamp: timestamp,
		TxID:      txID,
	}

	st.mu.Lock()
	st.scheduledCallbacks = append(st.scheduledCallbacks, callback)
	st.mu.Unlock()

	st.t.Logf("Successfully scheduled callback with ID %d", txID)

	return callback, nil
}

func (st *StressTest) extractTransactionID(output string) uint64 {
	// Try the new Cadence event format first: "id: 1234"
	re := regexp.MustCompile(`id:\s*(\d+)`)
	matches := re.FindStringSubmatch(output)
	if len(matches) >= 2 {
		if txID, err := strconv.ParseUint(matches[1], 10, 64); err == nil {
			return txID
		}
	}

	// Fall back to old format: "id (UInt64): 1234"
	re = regexp.MustCompile(`id \(UInt64\):\s*(\d+)`)
	matches = re.FindStringSubmatch(output)
	if len(matches) >= 2 {
		if txID, err := strconv.ParseUint(matches[1], 10, 64); err == nil {
			return txID
		}
	}

	st.t.Logf("Could not find transaction ID in output: %s", output)
	return 0
}

func (st *StressTest) extractTransactionIDFromEvents(events []flow.Event) uint64 {
	for _, event := range events {
		// Look for FlowTransactionScheduler.Scheduled event
		if strings.Contains(string(event.Type), "FlowTransactionScheduler.Scheduled") {
			// Convert the event value to string and parse the ID
			eventStr := event.Value.String()
			st.t.Logf("Event string: %s", eventStr)

			// Extract transaction ID from the string representation
			id := st.extractTransactionID(eventStr)
			if id != 0 {
				return id
			}
		}
	}
	return 0
}

func (st *StressTest) getCurrentBlockHeight() (uint64, error) {
	latestBlock, err := st.flowkit.GetBlock(st.ctx, flowkit.LatestBlockQuery)
	if err != nil {
		return 0, fmt.Errorf("failed to get latest block: %w", err)
	}

	return latestBlock.Height, nil
}

func (st *StressTest) waitAndCollectExecutedCallbacks(maxWaitTime time.Duration) error {
	st.t.Logf("Waiting up to %v for callbacks to execute...", maxWaitTime)

	time.Sleep(maxWaitTime)

	// Poll for events
	timeout := time.After(120 * time.Second)
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-timeout:
			return fmt.Errorf("timeout waiting for events after 120 seconds")
		case <-ticker.C:
			events, err := st.fetchEvents()
			if err != nil {
				st.t.Logf("Error fetching events: %v", err)
				continue
			}

			// Parse executed callbacks and their IDs
			executedIDs := st.parseExecutedCallbackIDsFromEvents(events)

			st.mu.Lock()
			st.executedCallbackIDs = executedIDs
			st.mu.Unlock()

			// Check if we have enough executed callbacks
			if len(executedIDs) >= len(st.scheduledCallbacks)/2 { // Adjust threshold as needed
				st.t.Logf("Found %d executed callbacks", len(executedIDs))
				return nil
			}

			st.t.Logf("Found %d executed callbacks so far, waiting...", len(executedIDs))
		}
	}
}

func (st *StressTest) parseExecutedCallbackIDsFromEvents(events []flow.Event) []uint64 {
	var executedIDs []uint64

	for _, event := range events {
		// Look for FlowTransactionScheduler.Executed events
		if strings.Contains(string(event.Type), "FlowTransactionScheduler.Executed") {
			// Convert the event value to string and parse the ID
			eventStr := event.Value.String()
			st.t.Logf("Executed event string: %s", eventStr)

			// Extract transaction ID from the string representation
			id := st.extractTransactionID(eventStr)
			if id != 0 {
				executedIDs = append(executedIDs, id)
			}
		}
	}

	return executedIDs
}

func (st *StressTest) fetchEvents() ([]flow.Event, error) {
	currentHeight, err := st.getCurrentBlockHeight()
	if err != nil {
		return nil, fmt.Errorf("failed to get current block height: %w", err)
	}

	st.t.Logf("Scanning for events from block %d to %d", st.startBlockHeight, currentHeight)

	// Get contract addresses from flow.json
	state, err := st.flowkit.State()
	if err != nil {
		return nil, fmt.Errorf("failed to get state: %w", err)
	}

	schedulerContract, err := state.Contracts().ByName("FlowTransactionScheduler")
	if err != nil {
		return nil, fmt.Errorf("failed to get FlowTransactionScheduler contract: %w", err)
	}

	handlerContract, err := state.Contracts().ByName("TestFlowCallbackHandler")
	if err != nil {
		return nil, fmt.Errorf("failed to get TestFlowCallbackHandler contract: %w", err)
	}

	// Get contract addresses for the testnet
	schedulerAlias := schedulerContract.Aliases.ByNetwork("testnet")
	if schedulerAlias == nil {
		return nil, fmt.Errorf("FlowTransactionScheduler contract has no testnet alias")
	}
	schedulerAddr := schedulerAlias.Address

	handlerAlias := handlerContract.Aliases.ByNetwork("testnet")
	if handlerAlias == nil {
		// Use migrationnet address as fallback for TestFlowCallbackHandler
		handlerAlias = handlerContract.Aliases.ByNetwork("migrationnet")
		if handlerAlias == nil {
			return nil, fmt.Errorf("TestFlowCallbackHandler contract has no testnet or migrationnet alias")
		}
	}
	handlerAddr := handlerAlias.Address

	// Define event types to query
	eventTypes := []string{
		fmt.Sprintf("A.%s.FlowTransactionScheduler.Scheduled", schedulerAddr.Hex()),
		fmt.Sprintf("A.%s.FlowTransactionScheduler.PendingExecution", schedulerAddr.Hex()),
		fmt.Sprintf("A.%s.FlowTransactionScheduler.Executed", schedulerAddr.Hex()),
		fmt.Sprintf("A.%s.FlowTransactionScheduler.Canceled", schedulerAddr.Hex()),
		fmt.Sprintf("A.%s.TestFlowCallbackHandler.CallbackExecuted", handlerAddr.Hex()),
	}

	// Create event worker
	worker := &flowkit.EventWorker{
		Count:           1,
		BlocksPerWorker: 100,
	}

	// Query events using flowkit
	events, err := st.flowkit.GetEvents(
		st.ctx,
		eventTypes,
		st.startBlockHeight,
		currentHeight,
		worker,
	)

	if err != nil {
		return nil, fmt.Errorf("failed to get events: %w", err)
	}

	// Flatten all events into a single slice
	var allEvents []flow.Event
	for _, blockEvents := range events {
		allEvents = append(allEvents, blockEvents.Events...)
	}

	return allEvents, nil
}

func (st *StressTest) getExecutedCallbackIDs() []uint64 {
	st.mu.Lock()
	defer st.mu.Unlock()

	// Return a copy to avoid data races
	result := make([]uint64, len(st.executedCallbackIDs))
	copy(result, st.executedCallbackIDs)
	return result
}

func (st *StressTest) getScheduledCallbacks() []ScheduledCallback {
	st.mu.Lock()
	defer st.mu.Unlock()

	// Return a copy to avoid data races
	result := make([]ScheduledCallback, len(st.scheduledCallbacks))
	copy(result, st.scheduledCallbacks)
	return result
}

// ScheduleResult represents the result of a concurrent scheduling operation
type ScheduleResult struct {
	Callback ScheduledCallback
	Error    error
	Index    int // Original index for tracking
}

// scheduleCallbacksConcurrently schedules multiple callbacks truly concurrently
// Uses goroutines with semaphore to limit concurrency to available keys
func (st *StressTest) scheduleCallbacksConcurrently(requests []ScheduleRequest, maxConcurrency int) []ScheduleResult {
	results := make([]ScheduleResult, len(requests))

	// Use keyCount as max concurrency if maxConcurrency is higher or not specified
	if maxConcurrency <= 0 || maxConcurrency > st.keyCount {
		maxConcurrency = st.keyCount
	}

	st.t.Logf("Starting concurrent submission of %d transactions with max concurrency %d", len(requests), maxConcurrency)

	// Create semaphore to limit concurrent goroutines to available keys
	semaphore := make(chan struct{}, maxConcurrency)
	var wg sync.WaitGroup

	// Launch goroutines for each request
	for i, req := range requests {
		wg.Add(1)
		go func(index int, request ScheduleRequest) {
			defer wg.Done()

			// Acquire semaphore slot
			semaphore <- struct{}{}
			defer func() { <-semaphore }()

			st.t.Logf("Submitting concurrent transaction %d/%d", index+1, len(requests))

			// Submit transaction using next available key
			callback, err := st.scheduleCallbackWithNextKey(
				request.Data,
				request.Priority,
				request.FutureSeconds,
				request.Effort,
			)

			results[index] = ScheduleResult{
				Callback: callback,
				Error:    err,
				Index:    index,
			}

			// Log progress
			if err != nil {
				st.t.Logf("Concurrent transaction %d failed: %v", index+1, err)
			} else {
				st.t.Logf("Concurrent transaction %d submitted successfully (ID: %d)", index+1, callback.TxID)
			}
		}(i, req)
	}

	// Wait for all goroutines to complete
	wg.Wait()

	st.t.Logf("All %d concurrent transactions completed", len(requests))
	return results
}

// ScheduleRequest represents a request to schedule a callback
type ScheduleRequest struct {
	Data          string
	Priority      uint8
	FutureSeconds int
	Effort        string
}

// Test Suite 0: Sanity Test - Single Transaction
func TestSingleTransactionSanity(t *testing.T) {
	st := NewStressTest(t)

	futureSeconds := 30 // Schedule 30 seconds in future
	testData := fmt.Sprintf("sanity-test-%d", time.Now().UnixNano())

	t.Logf("Running sanity test: scheduling single callback with data '%s'", testData)

	// Schedule a single callback
	startTime := time.Now()
	callback, err := st.scheduleCallbackWithNextKey(testData, 1, futureSeconds, "100")
	scheduleDuration := time.Since(startTime)

	if err != nil {
		t.Fatalf("Failed to schedule sanity test callback: %v", err)
	}

	t.Logf("Successfully scheduled callback with ID %d in %v", callback.TxID, scheduleDuration)
	t.Logf("Callback scheduled for timestamp: %.0f", callback.Timestamp)

	// Wait for execution
	t.Logf("Waiting for callback execution...")
	err = st.waitAndCollectExecutedCallbacks(time.Duration(futureSeconds+15) * time.Second)
	if err != nil {
		t.Errorf("Failed to collect executed callbacks: %v", err)
	}

	// Verify execution
	executedIDs := st.getExecutedCallbackIDs()
	scheduledCallbacks := st.getScheduledCallbacks()

	t.Logf("Sanity Test Results:")
	t.Logf("- Scheduled: %d callbacks", len(scheduledCallbacks))
	t.Logf("- Executed: %d callbacks", len(executedIDs))

	if len(scheduledCallbacks) != 1 {
		t.Errorf("Expected 1 scheduled callback, got %d", len(scheduledCallbacks))
	}

	if len(executedIDs) != 1 {
		t.Errorf("Expected 1 executed callback, got %d", len(executedIDs))
	} else {
		if executedIDs[0] == callback.TxID {
			t.Logf("✅ Sanity test PASSED: Callback %d executed successfully", callback.TxID)
		} else {
			t.Errorf("❌ Sanity test FAILED: Expected callback %d to execute, but got %d", callback.TxID, executedIDs[0])
		}
	}

	// Additional validation: Check if our specific callback was executed
	found := false
	for _, scheduled := range scheduledCallbacks {
		if scheduled.TxID == callback.TxID && scheduled.Data == testData {
			found = true
			t.Logf("✅ Callback data verification PASSED: Found callback with correct data '%s'", testData)
			break
		}
	}

	if !found {
		t.Errorf("❌ Callback data verification FAILED: Could not find callback with data '%s'", testData)
	}

	t.Logf("Sanity test completed - This validates the stress test suite is working correctly")
}

// Test Suite 1: Slot Saturation Test
func TestSlotSaturation(t *testing.T) {
	st := NewStressTest(t)

	futureSeconds := 60 // Schedule 60 seconds in future

	// High priority: Fill up to 20,000 effort (20 transactions @ 1000 each)
	highPriorityCount := 20
	highPriorityEffort := "1000"

	t.Logf("Scheduling %d high priority transactions with %s effort each concurrently", highPriorityCount, highPriorityEffort)

	// Create high priority requests
	var highRequests []ScheduleRequest
	for i := 0; i < highPriorityCount; i++ {
		data := fmt.Sprintf("slot-saturation-high-%d-%d", i, time.Now().UnixNano())
		highRequests = append(highRequests, ScheduleRequest{
			Data:          data,
			Priority:      0,
			FutureSeconds: futureSeconds,
			Effort:        highPriorityEffort,
		})
	}

	// Schedule high priority callbacks concurrently (use all available keys)
	startTime := time.Now()
	highResults := st.scheduleCallbacksConcurrently(highRequests, 0)
	highDuration := time.Since(startTime)

	highSuccessCount := 0
	for _, result := range highResults {
		if result.Error != nil {
			t.Logf("Failed to schedule high priority callback: %v", result.Error)
		} else {
			highSuccessCount++
		}
	}

	t.Logf("High priority scheduling completed: %d successful in %v", highSuccessCount, highDuration)

	// Medium priority: Try to fill shared pool (10 transactions @ 1000 each)
	mediumPriorityCount := 10
	mediumPriorityEffort := "1000"

	t.Logf("Scheduling %d medium priority transactions with %s effort each concurrently", mediumPriorityCount, mediumPriorityEffort)

	// Create medium priority requests
	var mediumRequests []ScheduleRequest
	for i := 0; i < mediumPriorityCount; i++ {
		data := fmt.Sprintf("slot-saturation-medium-%d-%d", i, time.Now().UnixNano())
		mediumRequests = append(mediumRequests, ScheduleRequest{
			Data:          data,
			Priority:      1,
			FutureSeconds: futureSeconds,
			Effort:        mediumPriorityEffort,
		})
	}

	// Schedule medium priority callbacks concurrently
	startTime = time.Now()
	mediumResults := st.scheduleCallbacksConcurrently(mediumRequests, 0)
	mediumDuration := time.Since(startTime)

	mediumSuccessCount := 0
	for _, result := range mediumResults {
		if result.Error != nil {
			t.Logf("Failed to schedule medium priority callback: %v", result.Error)
		} else {
			mediumSuccessCount++
		}
	}

	t.Logf("Medium priority scheduling completed: %d successful in %v", mediumSuccessCount, mediumDuration)

	// Try to schedule one more high priority - should fail
	t.Logf("Attempting to schedule additional high priority transaction (should fail)")
	data := fmt.Sprintf("slot-saturation-overflow-%d", time.Now().UnixNano())
	_, err := st.scheduleCallbackWithNextKey(data, 0, futureSeconds, "1000")
	if err == nil {
		t.Errorf("Expected slot saturation but was able to schedule additional transaction")
	} else {
		t.Logf("Correctly rejected overflow transaction: %v", err)
	}

	// Wait for execution and collect results
	st.waitAndCollectExecutedCallbacks(time.Duration(futureSeconds+30) * time.Second)

	executedIDs := st.getExecutedCallbackIDs()
	scheduledCallbacks := st.getScheduledCallbacks()

	t.Logf("Slot Saturation Test Results:")
	t.Logf("- High Priority Scheduled: %d", highSuccessCount)
	t.Logf("- Medium Priority Scheduled: %d", mediumSuccessCount)
	t.Logf("- Total Scheduled: %d callbacks", len(scheduledCallbacks))
	t.Logf("- Executed: %d callbacks", len(executedIDs))
	t.Logf("- High Priority Scheduling Time: %v", highDuration)
	t.Logf("- Medium Priority Scheduling Time: %v", mediumDuration)
}

// Test Suite 2: Burst Scheduling Test
func TestBurstScheduling(t *testing.T) {
	st := NewStressTest(t)

	// Configurable number of transactions
	burstSize := 10 // Start with 100, can increase to 500
	if testing.Short() {
		burstSize = 5
	}

	futureSeconds := 120 // Schedule 2 minutes in future to give time for all transactions

	t.Logf("Starting burst scheduling of %d transactions concurrently", burstSize)

	// Create burst requests with mixed priorities and random parameters
	var burstRequests []ScheduleRequest
	for i := 0; i < burstSize; i++ {
		priority := uint8(i % 3)                            // Mix priorities (0, 1, 2)
		effort := fmt.Sprintf("%d", 100+mathrand.Intn(900)) // Random effort 100-999
		data := fmt.Sprintf("burst-%d-%d", i, time.Now().UnixNano())
		timeVariation := mathrand.Intn(5) // Spread execution times over 5 seconds

		burstRequests = append(burstRequests, ScheduleRequest{
			Data:          data,
			Priority:      priority,
			FutureSeconds: futureSeconds + timeVariation,
			Effort:        effort,
		})
	}

	// Schedule all transactions concurrently with maximum available keys
	maxConcurrency := 0 // Use all available keys for maximum burst testing
	startTime := time.Now()
	results := st.scheduleCallbacksConcurrently(burstRequests, maxConcurrency)
	duration := time.Since(startTime)

	// Analyze results
	successCount := 0
	failureCount := 0
	for _, result := range results {
		if result.Error != nil {
			t.Logf("Failed to schedule burst callback: %v", result.Error)
			failureCount++
		} else {
			successCount++
		}
	}

	t.Logf("Burst scheduling completed in %v", duration)
	t.Logf("- Success: %d", successCount)
	t.Logf("- Failures: %d", failureCount)
	t.Logf("- Throughput: %.2f tx/sec", float64(successCount)/duration.Seconds())

	// Wait for execution
	st.waitAndCollectExecutedCallbacks(time.Duration(futureSeconds+90) * time.Second)

	executedIDs := st.getExecutedCallbackIDs()
	t.Logf("Burst Test Results:")
	t.Logf("- Executed: %d callbacks", len(executedIDs))
	t.Logf("- Execution rate: %.2f%%", float64(len(executedIDs))/float64(successCount)*100)
}

// Test Suite 4: Collection Limit Test
func TestCollectionLimits(t *testing.T) {
	st := NewStressTest(t)

	// Schedule exactly 150 transactions for the same timestamp
	targetCount := 155 // Try to exceed the 150 limit
	futureSeconds := 90

	t.Logf("Scheduling %d transactions for the same timestamp to test collection limit concurrently", targetCount)

	// Create requests for all transactions with the same timestamp
	var collectionRequests []ScheduleRequest
	for i := 0; i < targetCount; i++ {
		data := fmt.Sprintf("collection-limit-%d-%d", i, time.Now().UnixNano())

		collectionRequests = append(collectionRequests, ScheduleRequest{
			Data:          data,
			Priority:      uint8(i % 3),  // Mix priorities
			FutureSeconds: futureSeconds, // Same timestamp for all
			Effort:        "100",         // Small effort to ensure we hit transaction count limit first
		})
	}

	// Schedule all transactions concurrently - use all available keys to stress test
	startTime := time.Now()
	results := st.scheduleCallbacksConcurrently(collectionRequests, 0)
	duration := time.Since(startTime)

	successCount := 0
	failureCount := 0
	for i, result := range results {
		if result.Error != nil {
			t.Logf("Failed to schedule callback %d: %v", i, result.Error)
			if i >= 150 {
				t.Logf("Expected failure after 150: transaction %d correctly rejected", i)
			}
			failureCount++
		} else {
			successCount++
		}
	}

	t.Logf("Collection limit scheduling completed in %v", duration)
	t.Logf("Successfully scheduled %d transactions (failures: %d)", successCount, failureCount)

	// Wait and check for CollectionLimitReached event
	st.waitAndCollectExecutedCallbacks(time.Duration(futureSeconds+30) * time.Second)

	// Check events for collection limit
	events, _ := st.fetchEvents()
	hasCollectionLimitEvent := false
	for _, event := range events {
		if strings.Contains(string(event.Type), "CollectionLimitReached") {
			hasCollectionLimitEvent = true
			break
		}
	}
	if hasCollectionLimitEvent {
		t.Logf("Found CollectionLimitReached event as expected")
	}

	executedIDs := st.getExecutedCallbackIDs()
	t.Logf("Collection Limit Test Results:")
	t.Logf("- Scheduled: %d", successCount)
	t.Logf("- Executed: %d", len(executedIDs))

	if len(executedIDs) <= 150 {
		t.Logf("Collection limit properly enforced (executed <= 150)")
	} else {
		t.Errorf("Collection limit exceeded: %d transactions executed", len(executedIDs))
	}
}

// Test Suite 5: Priority Starvation Test
func TestPriorityStarvation(t *testing.T) {
	st := NewStressTest(t)

	futureSeconds := 60

	// First, fill slot with high priority transactions concurrently
	t.Logf("Filling slot with high priority transactions concurrently")

	// Create high priority requests to near capacity (29 transactions @ 1000 effort = 29,000 out of 30,000)
	var highRequests []ScheduleRequest
	for i := 0; i < 29; i++ {
		data := fmt.Sprintf("starvation-high-%d-%d", i, time.Now().UnixNano())
		highRequests = append(highRequests, ScheduleRequest{
			Data:          data,
			Priority:      0,
			FutureSeconds: futureSeconds,
			Effort:        "1000",
		})
	}

	// Schedule high priority transactions concurrently
	startTime := time.Now()
	highResults := st.scheduleCallbacksConcurrently(highRequests, 0)
	highDuration := time.Since(startTime)

	highSuccessCount := 0
	for _, result := range highResults {
		if result.Error != nil {
			t.Logf("Failed to schedule high priority: %v", result.Error)
		} else {
			highSuccessCount++
		}
	}

	t.Logf("High priority scheduling completed: %d successful in %v", highSuccessCount, highDuration)

	// Now try to schedule medium priority - should get rescheduled to next slot
	t.Logf("Attempting to schedule medium priority transactions (should get different slot)")

	mediumData := fmt.Sprintf("starvation-medium-%d", time.Now().UnixNano())
	mediumCallback, err := st.scheduleCallbackWithNextKey(mediumData, 1, futureSeconds, "6000")
	if err != nil {
		t.Logf("Failed to schedule medium priority: %v", err)
	} else {
		expectedTime := float64(time.Now().Unix() + int64(futureSeconds))
		if mediumCallback.Timestamp > expectedTime {
			t.Logf("Medium priority correctly rescheduled to future slot: %.0f (expected: %.0f)",
				mediumCallback.Timestamp, expectedTime)
		} else {
			t.Errorf("Medium priority was not rescheduled: %.0f", mediumCallback.Timestamp)
		}
	}

	// Try low priority - should definitely be pushed out
	t.Logf("Attempting to schedule low priority transactions (should get rescheduled)")

	lowData := fmt.Sprintf("starvation-low-%d", time.Now().UnixNano())
	lowCallback, err := st.scheduleCallbackWithNextKey(lowData, 2, futureSeconds, "1000")
	if err != nil {
		t.Logf("Failed to schedule low priority: %v", err)
	} else {
		t.Logf("Low priority scheduled at: %.0f", lowCallback.Timestamp)
	}

	// Wait for execution
	st.waitAndCollectExecutedCallbacks(time.Duration(futureSeconds+60) * time.Second)

	executedIDs := st.getExecutedCallbackIDs()
	scheduledCallbacks := st.getScheduledCallbacks()

	t.Logf("Priority Starvation Test Results:")
	t.Logf("- Total scheduled: %d", len(scheduledCallbacks))
	t.Logf("- Total executed: %d", len(executedIDs))

	// Analyze which priorities got executed
	highExecuted := 0
	mediumExecuted := 0
	lowExecuted := 0

	for _, callback := range scheduledCallbacks {
		for _, execID := range executedIDs {
			if callback.TxID == execID {
				switch callback.Priority {
				case 0:
					highExecuted++
				case 1:
					mediumExecuted++
				case 2:
					lowExecuted++
				}
				break
			}
		}
	}

	t.Logf("- High priority executed: %d", highExecuted)
	t.Logf("- Medium priority executed: %d", mediumExecuted)
	t.Logf("- Low priority executed: %d", lowExecuted)
}

// Test Suite 7: Data Payload Stress Test
func TestDataPayloadStress(t *testing.T) {
	st := NewStressTest(t)

	futureSeconds := 60

	// Test various data sizes
	dataSizes := []struct {
		name          string
		size          int // Size in KB
		shouldSucceed bool
	}{
		{"Empty", 0, true},
		{"Small-1KB", 1, true},
		{"Medium-500KB", 500, true},
		{"Large-1MB", 1024, true},
		{"VeryLarge-2MB", 2048, true},
		{"TooLarge-4MB", 4096, false}, // Should fail - exceeds 3MB limit
	}

	// Create payload requests
	var payloadRequests []ScheduleRequest
	for _, test := range dataSizes {
		t.Logf("Preparing data payload: %s (%d KB)", test.name, test.size)

		// Generate data of specified size
		var data string
		if test.size == 0 {
			data = "empty-payload"
		} else {
			// Generate string of approximately the right size
			chunk := "X"
			chunkSize := len(chunk)
			iterations := (test.size * 1024) / chunkSize
			dataBuilder := strings.Builder{}
			dataBuilder.WriteString("payload-")
			for i := 0; i < iterations; i++ {
				dataBuilder.WriteString(chunk)
			}
			data = dataBuilder.String()
		}

		payloadRequests = append(payloadRequests, ScheduleRequest{
			Data:          data,
			Priority:      1,
			FutureSeconds: futureSeconds + test.size/10, // Spread execution times
			Effort:        "100",
		})
	}

	// Schedule all payload tests concurrently
	t.Logf("Scheduling all payload tests concurrently")
	startTime := time.Now()
	results := st.scheduleCallbacksConcurrently(payloadRequests, 6) // Limited concurrency for large payloads
	duration := time.Since(startTime)

	// Analyze results
	for i, result := range results {
		test := dataSizes[i]

		if test.shouldSucceed && result.Error != nil {
			t.Errorf("Failed to schedule %s payload: %v", test.name, result.Error)
		} else if !test.shouldSucceed && result.Error == nil {
			t.Errorf("Expected %s payload to fail but it succeeded", test.name)
		} else if !test.shouldSucceed && result.Error != nil {
			t.Logf("Correctly rejected %s payload: %v", test.name, result.Error)
		} else {
			t.Logf("Successfully scheduled %s payload", test.name)
		}
	}

	t.Logf("Data payload scheduling completed in %v", duration)

	// Test fee calculation for different data sizes
	t.Logf("\nTesting storage fee impact on different data sizes...")

	// Wait for execution of successful payloads
	st.waitAndCollectExecutedCallbacks(time.Duration(futureSeconds+30) * time.Second)

	executedIDs := st.getExecutedCallbackIDs()
	scheduledCallbacks := st.getScheduledCallbacks()

	t.Logf("Data Payload Test Results:")
	t.Logf("- Scheduled: %d", len(scheduledCallbacks))
	t.Logf("- Executed: %d", len(executedIDs))
}
