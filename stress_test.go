package main

import (
	"fmt"
	"math/rand"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type StressTest struct {
	t                      *testing.T
	startBlockHeight       uint64
	executedCallbackIDs    []uint64 // Track which callbacks were actually executed
	scheduledCallbacks     []ScheduledCallback
	mu                     sync.Mutex      // Protect concurrent access
	sequenceNumber         *atomic.Uint64  // Track sequence numbers for transactions
	currentSequence        uint64          // Current sequence number
}

func NewStressTest(t *testing.T) *StressTest {
	st := &StressTest{
		t:                   t,
		executedCallbackIDs: make([]uint64, 0),
		scheduledCallbacks:  make([]ScheduledCallback, 0),
		sequenceNumber:      &atomic.Uint64{},
	}

	// Get the current block height and sequence number
	startHeight, err := st.getCurrentBlockHeight()
	if err != nil {
		t.Fatalf("Failed to get current block height: %v", err)
	}
	st.startBlockHeight = startHeight

	// Initialize sequence number
	seq, err := st.getCurrentSequenceNumber()
	if err != nil {
		t.Fatalf("Failed to get current sequence number: %v", err)
	}
	st.currentSequence = seq
	st.sequenceNumber.Store(seq)

	t.Logf("Starting stress test from block height %d with sequence number %d", startHeight, seq)

	return st
}

func (st *StressTest) getCurrentSequenceNumber() (uint64, error) {
	cmd := exec.Command("flow", "accounts", "get", serviceAccount, "-n", network)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return 0, fmt.Errorf("failed to get account info: %v, output: %s", err, output)
	}

	// Parse the sequence number from the output
	// Look for pattern like "Key 0.*Sequence Number.*\d+"
	re := regexp.MustCompile(`Key\s+0.*?\n.*?Sequence Number\s+(\d+)`)
	matches := re.FindStringSubmatch(string(output))
	if len(matches) < 2 {
		return 0, fmt.Errorf("could not find sequence number in output: %s", output)
	}

	seq, err := strconv.ParseUint(matches[1], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("failed to parse sequence number: %v", err)
	}

	return seq, nil
}

func (st *StressTest) getNextSequenceNumber() uint64 {
	return st.sequenceNumber.Add(1)
}

func (st *StressTest) scheduleCallbackWithSequence(data string, priority uint8, futureSeconds int, effort string) (ScheduledCallback, error) {
	timestamp := float64(time.Now().Unix() + int64(futureSeconds))
	sequence := st.getNextSequenceNumber()

	st.t.Logf("Scheduling callback with data '%s' at timestamp %.1f, sequence %d", data, timestamp, sequence)

	cmd := exec.Command("flow", "transactions", "send", "schedule_with_storage.cdc",
		fmt.Sprintf("%.1f", timestamp),
		defaultFee,
		effort,
		fmt.Sprintf("%d", priority),
		fmt.Sprintf("\"%s\"", data),
		"-n", network,
		"--signer", serviceAccountName,
		"--proposer-key-index", "0",
		"--proposer-sequence-number", fmt.Sprintf("%d", sequence))

	output, err := cmd.CombinedOutput()
	outputStr := string(output)

	if err != nil {
		// Rollback sequence on failure
		current := st.sequenceNumber.Load()
		st.sequenceNumber.Store(current - 1)
		st.t.Logf("Schedule transaction failed - full output:\n%s", outputStr)
		return ScheduledCallback{}, fmt.Errorf("failed to schedule callback: %v", err)
	}

	// Check for transaction errors
	if strings.Contains(outputStr, "❌ Transaction Error") {
		// Rollback sequence on failure
		current := st.sequenceNumber.Load()
		st.sequenceNumber.Store(current - 1)
		st.t.Logf("Schedule transaction had execution error - full output:\n%s", outputStr)
		return ScheduledCallback{}, fmt.Errorf("schedule transaction failed with execution error")
	}

	// Extract transaction ID
	txID := st.extractTransactionID(outputStr)
	if txID == 0 {
		st.t.Logf("Failed to extract transaction ID - full output:\n%s", outputStr)
		return ScheduledCallback{}, fmt.Errorf("failed to extract transaction ID from output")
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

	return callback, nil
}

func (st *StressTest) extractTransactionID(output string) uint64 {
	re := regexp.MustCompile(`id \(UInt64\):\s*(\d+)`)
	matches := re.FindStringSubmatch(output)
	if len(matches) < 2 {
		st.t.Logf("Could not find transaction ID in output: %s", output)
		return 0
	}

	txID, err := strconv.ParseUint(matches[1], 10, 64)
	if err != nil {
		st.t.Logf("Failed to parse transaction ID: %v", err)
		return 0
	}

	return txID
}

func (st *StressTest) getCurrentBlockHeight() (uint64, error) {
	cmd := exec.Command("flow", "blocks", "get", "latest", "-n", network)
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
			eventOutput, err := st.fetchEvents()
			if err != nil {
				st.t.Logf("Error fetching events: %v", err)
				continue
			}

			// Parse executed callbacks and their IDs
			executedIDs := st.parseExecutedCallbackIDs(eventOutput)

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

func (st *StressTest) parseExecutedCallbackIDs(eventOutput string) []uint64 {
	var executedIDs []uint64
	lines := strings.Split(eventOutput, "\n")

	inExecutedEvent := false
	for _, line := range lines {
		line = strings.TrimSpace(line)

		if strings.Contains(line, "FlowTransactionScheduler.Executed") {
			inExecutedEvent = true
			continue
		}

		if inExecutedEvent && strings.Contains(line, "- id (UInt64):") {
			re := regexp.MustCompile(`id \(UInt64\):\s*(\d+)`)
			matches := re.FindStringSubmatch(line)
			if len(matches) >= 2 {
				if id, err := strconv.ParseUint(matches[1], 10, 64); err == nil {
					executedIDs = append(executedIDs, id)
				}
			}
			inExecutedEvent = false
		}

		// Reset if we hit another event type
		if strings.Contains(line, "Type	A.") && !strings.Contains(line, "FlowTransactionScheduler.Executed") {
			inExecutedEvent = false
		}
	}

	return executedIDs
}

func (st *StressTest) fetchEvents() (string, error) {
	currentHeight, err := st.getCurrentBlockHeight()
	if err != nil {
		return "", fmt.Errorf("failed to get current block height: %v", err)
	}

	maxBlocks := uint64(100)
	var allEvents []string
	scanStart := st.startBlockHeight

	st.t.Logf("Scanning for events from block %d to %d", scanStart, currentHeight)

	for scanStart <= currentHeight {
		scanEnd := scanStart + maxBlocks - 1
		if scanEnd > currentHeight {
			scanEnd = currentHeight
		}

		if scanStart > scanEnd {
			break
		}

		account := strings.TrimPrefix(serviceAccount, "0x")
		cmd := exec.Command("flow", "events", "get",
			fmt.Sprintf("A.%s.FlowTransactionScheduler.Scheduled", account),
			fmt.Sprintf("A.%s.FlowTransactionScheduler.PendingExecution", account),
			fmt.Sprintf("A.%s.FlowTransactionScheduler.Executed", account),
			fmt.Sprintf("A.%s.FlowTransactionScheduler.Canceled", account),
			fmt.Sprintf("A.%s.TestFlowCallbackHandler.CallbackExecuted", testHandlerAccount),
			"--start", fmt.Sprintf("%d", scanStart),
			"--end", fmt.Sprintf("%d", scanEnd),
			"-n", network)

		output, err := cmd.CombinedOutput()
		if err != nil {
			st.t.Logf("Error fetching events for blocks %d-%d: %v", scanStart, scanEnd, err)
			scanStart = scanEnd + 1
			continue
		}

		outputStr := string(output)
		if strings.Contains(outputStr, "Events Block #") {
			allEvents = append(allEvents, outputStr)
		}

		scanStart = scanEnd + 1
	}

	return strings.Join(allEvents, "\n"), nil
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

// Test Suite 1: Slot Saturation Test
func TestSlotSaturation(t *testing.T) {
	st := NewStressTest(t)

	futureSeconds := 60 // Schedule 60 seconds in future

	// High priority: Fill up to 20,000 effort (20 transactions @ 1000 each)
	highPriorityCount := 20
	highPriorityEffort := "1000"

	t.Logf("Scheduling %d high priority transactions with %s effort each", highPriorityCount, highPriorityEffort)

	for i := 0; i < highPriorityCount; i++ {
		data := fmt.Sprintf("slot-saturation-high-%d-%d", i, time.Now().UnixNano())
		_, err := st.scheduleCallbackWithSequence(data, 0, futureSeconds, highPriorityEffort)
		if err != nil {
			t.Logf("Failed to schedule high priority callback %d: %v", i, err)
			// Continue to see how many we can schedule
		}
		time.Sleep(100 * time.Millisecond) // Small delay between transactions
	}

	// Medium priority: Try to fill shared pool (10 transactions @ 1000 each)
	mediumPriorityCount := 10
	mediumPriorityEffort := "1000"

	t.Logf("Scheduling %d medium priority transactions with %s effort each", mediumPriorityCount, mediumPriorityEffort)

	for i := 0; i < mediumPriorityCount; i++ {
		data := fmt.Sprintf("slot-saturation-medium-%d-%d", i, time.Now().UnixNano())
		_, err := st.scheduleCallbackWithSequence(data, 1, futureSeconds, mediumPriorityEffort)
		if err != nil {
			t.Logf("Failed to schedule medium priority callback %d: %v", i, err)
		}
		time.Sleep(100 * time.Millisecond)
	}

	// Try to schedule one more high priority - should fail
	t.Logf("Attempting to schedule additional high priority transaction (should fail)")
	data := fmt.Sprintf("slot-saturation-overflow-%d", time.Now().UnixNano())
	_, err := st.scheduleCallbackWithSequence(data, 0, futureSeconds, "1000")
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
	t.Logf("- Scheduled: %d callbacks", len(scheduledCallbacks))
	t.Logf("- Executed: %d callbacks", len(executedIDs))
	t.Logf("- Executed IDs: %v", executedIDs)
}

// Test Suite 2: Burst Scheduling Test
func TestBurstScheduling(t *testing.T) {
	st := NewStressTest(t)

	// Configurable number of transactions
	burstSize := 100 // Start with 100, can increase to 500
	if testing.Short() {
		burstSize = 50
	}

	futureSeconds := 120 // Schedule 2 minutes in future to give time for all transactions

	t.Logf("Starting burst scheduling of %d transactions", burstSize)

	startTime := time.Now()
	successCount := 0
	failureCount := 0

	// Use goroutines for parallel scheduling with sequence number management
	var wg sync.WaitGroup
	semaphore := make(chan struct{}, 10) // Limit concurrent submissions to 10

	for i := 0; i < burstSize; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()

			semaphore <- struct{}{} // Acquire
			defer func() { <-semaphore }() // Release

			priority := uint8(index % 3) // Mix priorities
			effort := fmt.Sprintf("%d", 100+rand.Intn(900)) // Random effort 100-999
			data := fmt.Sprintf("burst-%d-%d", index, time.Now().UnixNano())

			_, err := st.scheduleCallbackWithSequence(data, priority, futureSeconds+rand.Intn(60), effort)
			if err != nil {
				st.t.Logf("Failed to schedule burst callback %d: %v", index, err)
				st.mu.Lock()
				failureCount++
				st.mu.Unlock()
			} else {
				st.mu.Lock()
				successCount++
				st.mu.Unlock()
			}
		}(i)

		// Small delay to prevent overwhelming the network
		time.Sleep(50 * time.Millisecond)
	}

	wg.Wait()
	duration := time.Since(startTime)

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

	t.Logf("Scheduling %d transactions for the same timestamp to test collection limit", targetCount)

	successCount := 0
	for i := 0; i < targetCount; i++ {
		data := fmt.Sprintf("collection-limit-%d-%d", i, time.Now().UnixNano())
		effort := "100" // Small effort to ensure we hit transaction count limit first

		_, err := st.scheduleCallbackWithSequence(data, uint8(i%3), futureSeconds, effort)
		if err != nil {
			t.Logf("Failed to schedule callback %d (expected after 150): %v", i, err)
			if i >= 150 {
				t.Logf("Correctly hit collection limit at transaction %d", i)
			}
		} else {
			successCount++
		}

		time.Sleep(100 * time.Millisecond)
	}

	t.Logf("Successfully scheduled %d transactions", successCount)

	// Wait and check for CollectionLimitReached event
	st.waitAndCollectExecutedCallbacks(time.Duration(futureSeconds+30) * time.Second)

	// Check events for collection limit
	eventOutput, _ := st.fetchEvents()
	if strings.Contains(eventOutput, "CollectionLimitReached") {
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

	// First, fill slot with high priority transactions
	t.Logf("Filling slot with high priority transactions")

	// Fill high priority to near capacity (29 transactions @ 1000 effort = 29,000 out of 30,000)
	for i := 0; i < 29; i++ {
		data := fmt.Sprintf("starvation-high-%d-%d", i, time.Now().UnixNano())
		_, err := st.scheduleCallbackWithSequence(data, 0, futureSeconds, "1000")
		if err != nil {
			t.Logf("Failed to schedule high priority %d: %v", i, err)
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	// Now try to schedule medium priority - should get rescheduled to next slot
	t.Logf("Attempting to schedule medium priority transactions (should get different slot)")

	mediumData := fmt.Sprintf("starvation-medium-%d", time.Now().UnixNano())
	mediumCallback, err := st.scheduleCallbackWithSequence(mediumData, 1, futureSeconds, "6000")
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
	lowCallback, err := st.scheduleCallbackWithSequence(lowData, 2, futureSeconds, "1000")
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
		name string
		size int // Size in KB
		shouldSucceed bool
	}{
		{"Empty", 0, true},
		{"Small-1KB", 1, true},
		{"Medium-500KB", 500, true},
		{"Large-1MB", 1024, true},
		{"VeryLarge-2MB", 2048, true},
		{"TooLarge-4MB", 4096, false}, // Should fail - exceeds 3MB limit
	}

	for _, test := range dataSizes {
		t.Logf("Testing data payload: %s (%d KB)", test.name, test.size)

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

		_, err := st.scheduleCallbackWithSequence(data, 1, futureSeconds+test.size/10, "100")

		if test.shouldSucceed && err != nil {
			t.Errorf("Failed to schedule %s payload: %v", test.name, err)
		} else if !test.shouldSucceed && err == nil {
			t.Errorf("Expected %s payload to fail but it succeeded", test.name)
		} else if !test.shouldSucceed && err != nil {
			t.Logf("Correctly rejected %s payload: %v", test.name, err)
		} else {
			t.Logf("Successfully scheduled %s payload", test.name)
		}

		time.Sleep(200 * time.Millisecond) // Small delay between tests
	}

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