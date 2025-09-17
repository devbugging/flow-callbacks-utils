package main

import (
	"fmt"
	"math/rand"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

const (
	serviceAccount     = "8c5303eaa26202d6"
	testHandlerAccount = "373ce83aef691d2d"
	serviceAccountName = "migration"
	network            = "migrationnet"
	defaultFee         = "0.1"
	defaultEffort      = "100"
)

type CallbackTest struct {
	NumCallbacks           int
	t                      *testing.T
	startBlockHeight       uint64
	lastScannedBlockHeight uint64
}

type ScheduledCallback struct {
	Data      string
	Priority  uint8
	Timestamp float64
	TxID      uint64
}

func NewCallbackTest(t *testing.T, numCallbacks int) *CallbackTest {
	ct := &CallbackTest{
		NumCallbacks: numCallbacks,
		t:            t,
	}

	// Get the current block height to use as starting point for event fetching
	startHeight, err := ct.getCurrentBlockHeight()
	if err != nil {
		t.Fatalf("Failed to get current block height: %v", err)
	}
	ct.startBlockHeight = startHeight
	ct.lastScannedBlockHeight = startHeight
	t.Logf("Starting test from block height %d", startHeight)

	return ct
}

func (ct *CallbackTest) scheduleCallback(data string, priority uint8, futureSeconds int) (float64, error) {
	timestamp := float64(time.Now().Unix() + int64(futureSeconds))
	ct.t.Logf("Scheduled callback with data '%s' at timestamp %.1f", data, timestamp)

	cmd := exec.Command("flow", "transactions", "send", "schedule.cdc",
		fmt.Sprintf("%.1f", timestamp),
		defaultFee,
		defaultEffort,
		fmt.Sprintf("%d", priority),
		fmt.Sprintf("\"%s\"", data),
		"-n", network,
		"--signer", serviceAccountName)

	output, err := cmd.CombinedOutput()
	outputStr := string(output)

	if err != nil {
		ct.t.Logf("Schedule transaction failed - full output:\n%s", outputStr)
		return 0, fmt.Errorf("failed to schedule callback: %v", err)
	}

	// Check for transaction errors
	if strings.Contains(outputStr, "❌ Transaction Error") {
		ct.t.Logf("Schedule transaction had execution error - full output:\n%s", outputStr)
		return 0, fmt.Errorf("schedule transaction failed with execution error")
	}

	return timestamp, nil
}

func (ct *CallbackTest) scheduleCallbackWithStorage(data string, priority uint8, futureSeconds int) (ScheduledCallback, error) {
	timestamp := float64(time.Now().Unix() + int64(futureSeconds))
	ct.t.Logf("Scheduled callback with storage - data '%s' at timestamp %.1f", data, timestamp)

	cmd := exec.Command("flow", "transactions", "send", "schedule_with_storage.cdc",
		fmt.Sprintf("%.1f", timestamp),
		defaultFee,
		defaultEffort,
		fmt.Sprintf("%d", priority),
		fmt.Sprintf("\"%s\"", data),
		"-n", network,
		"--signer", serviceAccountName)

	output, err := cmd.CombinedOutput()
	outputStr := string(output)

	if err != nil {
		ct.t.Logf("Schedule transaction failed - full output:\n%s", outputStr)
		return ScheduledCallback{}, fmt.Errorf("failed to schedule callback: %v", err)
	}

	// Check for transaction errors
	if strings.Contains(outputStr, "❌ Transaction Error") {
		ct.t.Logf("Schedule transaction had execution error - full output:\n%s", outputStr)
		return ScheduledCallback{}, fmt.Errorf("schedule transaction failed with execution error")
	}

	// extract transaction ID from output
	txID := ct.extractTransactionID(outputStr)
	if txID == 0 {
		ct.t.Logf("Failed to extract transaction ID - full output:\n%s", outputStr)
		return ScheduledCallback{}, fmt.Errorf("failed to extract transaction ID from output")
	}

	return ScheduledCallback{
		Data:      data,
		Priority:  priority,
		Timestamp: timestamp,
		TxID:      txID,
	}, nil
}

func (ct *CallbackTest) cancelCallback(txID uint64) error {
	ct.t.Logf("Canceling callback with transaction ID %d", txID)

	cmd := exec.Command("flow", "transactions", "send", "cancel.cdc",
		fmt.Sprintf("%d", txID),
		"-n", network,
		"--signer", serviceAccountName)

	output, err := cmd.CombinedOutput()
	outputStr := string(output)

	if err != nil {
		ct.t.Logf("Cancel transaction failed - full output:\n%s", outputStr)
		return fmt.Errorf("failed to cancel callback: %v", err)
	}

	// Check if the cancel transaction was successful by looking for SEALED status and no errors
	if !strings.Contains(outputStr, "✅ SEALED") {
		ct.t.Logf("Cancel transaction was not sealed - full output:\n%s", outputStr)
		return fmt.Errorf("cancel transaction was not sealed successfully")
	}

	// Also check for transaction errors
	if strings.Contains(outputStr, "❌ Transaction Error") {
		ct.t.Logf("Cancel transaction had execution error - full output:\n%s", outputStr)
		return fmt.Errorf("cancel transaction failed with error")
	}

	ct.t.Logf("Successfully canceled transaction ID %d", txID)
	return nil
}

func (ct *CallbackTest) extractTransactionID(output string) uint64 {
	// look for the FlowTransactionScheduler.Scheduled event and extract the id field
	// Pattern: id (UInt64): followed by the number
	re := regexp.MustCompile(`id \(UInt64\):\s*(\d+)`)
	matches := re.FindStringSubmatch(output)
	if len(matches) < 2 {
		ct.t.Logf("Could not find transaction ID in output: %s", output)
		return 0
	}

	txID, err := strconv.ParseUint(matches[1], 10, 64)
	if err != nil {
		ct.t.Logf("Failed to parse transaction ID: %v", err)
		return 0
	}

	return txID
}

func (ct *CallbackTest) getCurrentBlockHeight() (uint64, error) {
	cmd := exec.Command("flow", "blocks", "get", "latest", "-n", network)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return 0, fmt.Errorf("failed to get latest block: %v, output: %s", err, output)
	}

	// Parse the block height from the output
	// Look for pattern like "Height			6671"
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

func (ct *CallbackTest) checkForCanceledEvent(eventOutput string, txID uint64) bool {
	// Look for a Canceled event with the specific transaction ID
	lines := strings.Split(eventOutput, "\n")
	inCanceledEvent := false

	for _, line := range lines {
		line = strings.TrimSpace(line)

		// Check if we're in a Canceled event
		if strings.Contains(line, "FlowTransactionScheduler.Canceled") {
			inCanceledEvent = true
			continue
		}

		// If we're in a canceled event, look for the id field
		if inCanceledEvent && strings.Contains(line, "- id (UInt64):") {
			re := regexp.MustCompile(`id \(UInt64\):\s*(\d+)`)
			matches := re.FindStringSubmatch(line)
			if len(matches) >= 2 {
				eventTxID, err := strconv.ParseUint(matches[1], 10, 64)
				if err == nil && eventTxID == txID {
					return true
				}
			}
			inCanceledEvent = false
		}

		// Reset if we hit another event type
		if strings.Contains(line, "Type	A.") && !strings.Contains(line, "FlowTransactionScheduler.Canceled") {
			inCanceledEvent = false
		}
	}

	return false
}

func (ct *CallbackTest) waitForEventsWithRetry(maxWaitTime time.Duration) (string, error) {
	ct.t.Logf("Waiting up to %v for callbacks to execute...", maxWaitTime)

	// Wait for the scheduled time first
	time.Sleep(maxWaitTime)

	// Then poll for events for up to 2 minutes
	timeout := time.After(120 * time.Second)
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-timeout:
			return "", fmt.Errorf("timeout waiting for events after 120 seconds")
		case <-ticker.C:
			eventOutput, err := ct.fetchEvents()
			if err != nil {
				ct.t.Logf("Error fetching events: %v", err)
				continue
			}

			// Check if we have the expected events
			var hasEvents bool
			if ct.NumCallbacks > 1 {
				hasEvents = ct.hasExpectedEventsForCallbacks(eventOutput)
			} else if ct.NumCallbacks == 0 {
				// This is a failed callback test expecting 0 successful callbacks
				hasEvents = ct.hasExpectedEventsForFailedCallback(eventOutput)
			} else {
				hasEvents = ct.hasExpectedEvents(eventOutput)
			}

			if hasEvents {
				return eventOutput, nil
			}
			// Log progress for debugging
			executedCount := len(ct.parseExecutedData(eventOutput))
			ct.t.Logf("Events not ready yet (%d/%d callbacks executed), waiting 5 more seconds...", executedCount, ct.NumCallbacks)
		}
	}
}

func (ct *CallbackTest) fetchEvents() (string, error) {
	// Get current block height to use as end block
	currentHeight, err := ct.getCurrentBlockHeight()
	if err != nil {
		return "", fmt.Errorf("failed to get current block height for event fetching: %v", err)
	}

	maxBlocks := uint64(100)
	var allEvents []string

	// Scan from the original start block to current block in chunks
	scanStart := ct.startBlockHeight

	ct.t.Logf("Scanning for events from block %d to %d (%d total blocks)",
		scanStart, currentHeight, currentHeight-scanStart)

	for scanStart <= currentHeight {
		// Calculate the end of this scan chunk
		scanEnd := scanStart + maxBlocks - 1 // -1 because range is inclusive
		if scanEnd > currentHeight {
			scanEnd = currentHeight
		}

		// Skip if this is an empty range
		if scanStart > scanEnd {
			break
		}

		// Remove 0x prefix from service account for event names
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
			ct.t.Logf("Error fetching events for blocks %d-%d: %v", scanStart, scanEnd, err)
			// Continue with next chunk instead of failing completely
			scanStart = scanEnd + 1
			continue
		}

		outputStr := string(output)

		// Collect events if found
		if strings.Contains(outputStr, "Events Block #") {
			allEvents = append(allEvents, outputStr)
		}

		// Move to next chunk
		scanStart = scanEnd + 1
	}

	// Combine all event outputs
	combinedOutput := strings.Join(allEvents, "\n")

	return combinedOutput, nil
}

func (ct *CallbackTest) parseExecutedData(eventOutput string) []string {
	var executedData []string
	lines := strings.Split(eventOutput, "\n")

	inHandlerEvent := false
	for _, line := range lines {
		line = strings.TrimSpace(line)

		if strings.Contains(line, "TestFlowCallbackHandler.CallbackExecuted") {
			inHandlerEvent = true
			continue
		}

		if inHandlerEvent && strings.Contains(line, "data (String):") {
			parts := strings.Split(line, "data (String):")
			if len(parts) > 1 {
				data := strings.TrimSpace(parts[1])
				data = strings.Trim(data, "\"")
				executedData = append(executedData, data)
			}
			inHandlerEvent = false
		}
	}

	return executedData
}

func (ct *CallbackTest) extractBlockNumbers(eventOutput string) []string {
	re := regexp.MustCompile(`Events Block #(\d+):`)
	matches := re.FindAllStringSubmatch(eventOutput, -1)
	var blocks []string
	for _, match := range matches {
		if len(match) > 1 {
			blocks = append(blocks, match[1])
		}
	}
	return blocks
}

func (ct *CallbackTest) getBlockTimestamp(blockNumber string) (float64, error) {
	cmd := exec.Command("flow", "blocks", "get", blockNumber, "-n", network)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return 0, fmt.Errorf("failed to get block %s: %v", blockNumber, err)
	}

	re := regexp.MustCompile(`Proposal Timestamp Unix:\s*([0-9.]+)`)
	matches := re.FindStringSubmatch(string(output))
	if len(matches) < 2 {
		return 0, fmt.Errorf("could not find proposal timestamp in block %s output", blockNumber)
	}

	timestamp, err := strconv.ParseFloat(matches[1], 64)
	if err != nil {
		return 0, fmt.Errorf("failed to parse timestamp: %v", err)
	}

	return timestamp, nil
}

func (ct *CallbackTest) validateEventSequence(eventOutput string) error {
	hasScheduled := strings.Contains(eventOutput, "FlowTransactionScheduler.Scheduled")
	hasPendingExecution := strings.Contains(eventOutput, "FlowTransactionScheduler.PendingExecution")
	hasExecuted := strings.Contains(eventOutput, "FlowTransactionScheduler.Executed")
	hasHandlerExecuted := strings.Contains(eventOutput, "TestFlowCallbackHandler.CallbackExecuted")

	if !hasScheduled {
		return fmt.Errorf("Scheduled event not found")
	}
	if !hasPendingExecution {
		return fmt.Errorf("PendingExecution event not found")
	}
	if !hasExecuted {
		return fmt.Errorf("Executed event not found")
	}
	if !hasHandlerExecuted {
		return fmt.Errorf("TestFlowCallbackHandler.CallbackExecuted event not found")
	}

	return nil
}

func (ct *CallbackTest) hasExpectedEvents(eventOutput string) bool {
	hasScheduled := strings.Contains(eventOutput, "FlowTransactionScheduler.Scheduled")
	hasPendingExecution := strings.Contains(eventOutput, "FlowTransactionScheduler.PendingExecution")
	hasExecuted := strings.Contains(eventOutput, "FlowTransactionScheduler.Executed")
	hasHandlerExecuted := strings.Contains(eventOutput, "TestFlowCallbackHandler.CallbackExecuted")

	ct.t.Logf("Event check - Scheduled: %v, PendingExecution: %v, Executed: %v, HandlerExecuted: %v",
		hasScheduled, hasPendingExecution, hasExecuted, hasHandlerExecuted)

	return hasScheduled && hasPendingExecution && hasExecuted && hasHandlerExecuted
}

func (ct *CallbackTest) hasExpectedEventsForCallbacks(eventOutput string) bool {
	// For multiple callbacks, we need to check if we have enough executed events
	executedData := ct.parseExecutedData(eventOutput)

	// We should have at least as many executed events as we scheduled callbacks
	if len(executedData) < ct.NumCallbacks {
		return false
	}

	// Also check that we have the other event types
	hasScheduled := strings.Contains(eventOutput, "FlowTransactionScheduler.Scheduled")
	hasPendingExecution := strings.Contains(eventOutput, "FlowTransactionScheduler.PendingExecution")
	hasExecuted := strings.Contains(eventOutput, "FlowTransactionScheduler.Executed")

	return hasScheduled && hasPendingExecution && hasExecuted
}

func (ct *CallbackTest) hasExpectedEventsForFailedCallback(eventOutput string) bool {
	// For failed callbacks, we only expect Scheduled and PendingExecution events
	// We should NOT see Executed or HandlerExecuted events
	hasScheduled := strings.Contains(eventOutput, "FlowTransactionScheduler.Scheduled")
	hasPendingExecution := strings.Contains(eventOutput, "FlowTransactionScheduler.PendingExecution")
	hasExecuted := strings.Contains(eventOutput, "FlowTransactionScheduler.Executed")
	hasHandlerExecuted := strings.Contains(eventOutput, "TestFlowCallbackHandler.CallbackExecuted")

	ct.t.Logf("Failed callback event check - Scheduled: %v, PendingExecution: %v, Executed: %v, HandlerExecuted: %v",
		hasScheduled, hasPendingExecution, hasExecuted, hasHandlerExecuted)

	// For a failed callback, we want Scheduled and PendingExecution but NOT Executed or HandlerExecuted
	return hasScheduled && hasPendingExecution && !hasExecuted && !hasHandlerExecuted
}

func (ct *CallbackTest) validateTimestamp(scheduledTime float64, blockTime float64) bool {
	diff := blockTime - scheduledTime
	return diff >= 0 && diff <= 2.0 // Within 2 seconds after scheduled time
}

/* =================================================
Tests section
================================================= */

func TestSingleCallback(t *testing.T) {
	ct := NewCallbackTest(t, 1)

	// Add timestamp to prevent overlap with previous test runs
	testData := fmt.Sprintf("test-single-callback-%d", time.Now().UnixNano())
	priority := uint8(1)
	futureSeconds := 20

	scheduledTime, err := ct.scheduleCallback(testData, priority, futureSeconds)
	if err != nil {
		t.Fatalf("Failed to schedule callback: %v", err)
	}

	eventOutput, err := ct.waitForEventsWithRetry(time.Duration(futureSeconds) * time.Second)
	if err != nil {
		t.Fatalf("Failed to get events: %v", err)
	}

	// Validate event sequence
	if err := ct.validateEventSequence(eventOutput); err != nil {
		t.Fatalf("Event sequence validation failed: %v", err)
	}

	// Validate executed data
	executedData := ct.parseExecutedData(eventOutput)
	found := false
	for _, data := range executedData {
		t.Logf("TestFlowCallbackHandler.CallbackExecuted event data: '%s'", data)
		if data == testData {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("Expected data '%s' not found in executed callbacks: %v", testData, executedData)
	}

	// Validate block timestamps
	blockNumbers := ct.extractBlockNumbers(eventOutput)
	for _, blockNum := range blockNumbers {
		if blockTime, err := ct.getBlockTimestamp(blockNum); err == nil {
			if ct.validateTimestamp(scheduledTime, blockTime) {
				t.Logf("Block #%s timestamp %.1f is within 2 seconds of scheduled time %.1f",
					blockNum, blockTime, scheduledTime)
			} else {
				t.Logf("Block #%s timestamp %.1f differs from scheduled time %.1f by %.1f seconds",
					blockNum, blockTime, scheduledTime, blockTime-scheduledTime)
			}
		}
	}

	t.Logf("Single callback test completed successfully")
}

func TestSingleFailedCallback(t *testing.T) {
	ct := NewCallbackTest(t, 0) // Expecting 0 successful callbacks

	// Schedule a callback with "fail" data to trigger panic
	testData := "fail"
	priority := uint8(1)
	futureSeconds := 20

	scheduledTime, err := ct.scheduleCallback(testData, priority, futureSeconds)
	if err != nil {
		t.Fatalf("Failed to schedule callback: %v", err)
	}

	eventOutput, err := ct.waitForEventsWithRetry(time.Duration(futureSeconds) * time.Second)
	if err != nil {
		t.Fatalf("Failed to get events: %v", err)
	}

	// Check that we have scheduled and pending events
	hasScheduled := strings.Contains(eventOutput, "FlowTransactionScheduler.Scheduled")
	hasPendingExecution := strings.Contains(eventOutput, "FlowTransactionScheduler.PendingExecution")
	hasExecuted := strings.Contains(eventOutput, "FlowTransactionScheduler.Executed")
	hasHandlerExecuted := strings.Contains(eventOutput, "TestFlowCallbackHandler.CallbackExecuted")

	if !hasScheduled {
		t.Errorf("Expected Scheduled event but it was not found")
	}
	if !hasPendingExecution {
		t.Errorf("Expected PendingExecution event but it was not found")
	}
	if hasExecuted {
		t.Errorf("Expected no Executed event but it was found - callback should have failed")
	}
	if hasHandlerExecuted {
		t.Errorf("Expected no CallbackExecuted event but it was found - callback should have failed")
	}

	// Validate executed data should be empty
	executedData := ct.parseExecutedData(eventOutput)
	if len(executedData) > 0 {
		t.Errorf("Expected no executed callbacks but found: %v", executedData)
	}

	// Validate block timestamps
	blockNumbers := ct.extractBlockNumbers(eventOutput)
	for _, blockNum := range blockNumbers {
		if blockTime, err := ct.getBlockTimestamp(blockNum); err == nil {
			if ct.validateTimestamp(scheduledTime, blockTime) {
				t.Logf("Block #%s timestamp %.1f is within 2 seconds of scheduled time %.1f",
					blockNum, blockTime, scheduledTime)
			} else {
				t.Logf("Block #%s timestamp %.1f differs from scheduled time %.1f by %.1f seconds",
					blockNum, blockTime, scheduledTime, blockTime-scheduledTime)
			}
		}
	}

	t.Logf("Single failed callback test completed successfully - callback properly failed as expected")
}

func TestMultipleCallbacks(t *testing.T) {
	numCallbacks := 5 // Configurable - change this to test more callbacks
	ct := NewCallbackTest(t, numCallbacks)

	var scheduledCallbacks []ScheduledCallback
	maxWaitTime := time.Duration(0)

	// Channel to collect results from concurrent scheduling
	type scheduleResult struct {
		callback ScheduledCallback
		err      error
		waitTime time.Duration
	}
	resultChan := make(chan scheduleResult, numCallbacks)

	// Schedule callbacks concurrently
	for i := 0; i < numCallbacks; i++ {
		// Add timestamp to prevent overlap with previous test runs
		testData := fmt.Sprintf("test-multi-%d-%d", i, time.Now().UnixNano())
		priority := uint8(i % 3)             // Priorities 0, 1, 2
		futureSeconds := 20 + rand.Intn(100) // Random between 20-120 seconds

		scheduledTime, err := ct.scheduleCallback(testData, priority, futureSeconds)

		resultChan <- scheduleResult{
			callback: ScheduledCallback{
				Data:      testData,
				Priority:  priority,
				Timestamp: scheduledTime,
			},
			err:      err,
			waitTime: time.Duration(futureSeconds) * time.Second,
		}
	}

	// Collect results
	for i := 0; i < numCallbacks; i++ {
		result := <-resultChan
		if result.err != nil {
			t.Fatalf("Failed to schedule callback: %v", result.err)
		}

		scheduledCallbacks = append(scheduledCallbacks, result.callback)
		if result.waitTime > maxWaitTime {
			maxWaitTime = result.waitTime
		}

		t.Logf("Scheduled callback %d/%d with data: %s", i+1, numCallbacks, result.callback.Data)
	}

	maxWaitTime += 30 * time.Second
	eventOutput, err := ct.waitForEventsWithRetry(maxWaitTime)
	if err != nil {
		t.Fatalf("Failed to get events: %v", err)
	}

	// Validate all callbacks executed with correct data
	executedData := ct.parseExecutedData(eventOutput)
	executedMap := make(map[string]bool)
	for _, data := range executedData {
		t.Logf("TestFlowCallbackHandler.CallbackExecuted event data: '%s'", data)
		executedMap[data] = true
	}

	for _, scheduled := range scheduledCallbacks {
		if !executedMap[scheduled.Data] {
			t.Errorf("Callback with data '%s' was not executed", scheduled.Data)
		} else {
			t.Logf("Callback with data '%s' executed successfully", scheduled.Data)
		}
	}

	// Validate block timestamps
	blockNumbers := ct.extractBlockNumbers(eventOutput)
	for _, blockNum := range blockNumbers {
		if blockTime, err := ct.getBlockTimestamp(blockNum); err == nil {
			t.Logf("Block #%s has timestamp %.1f", blockNum, blockTime)
		}
	}

	t.Logf("Multiple callbacks test completed successfully - executed %d callbacks", len(executedData))
}

func TestMultipleCallbacksWithCancellation(t *testing.T) {
	numCallbacks := 15       // Configurable - total callbacks to schedule
	numToCancelCallback := 5 // Configurable - how many to cancel
	ct := NewCallbackTest(t, numCallbacks-numToCancelCallback)

	var scheduledCallbacks []ScheduledCallback
	var canceledCallbacks []ScheduledCallback
	maxWaitTime := time.Duration(0)

	// Channel to collect results from concurrent scheduling
	type scheduleResult struct {
		callback ScheduledCallback
		err      error
		waitTime time.Duration
	}
	resultChan := make(chan scheduleResult, numCallbacks)

	// Schedule callbacks sequentially to avoid sequence number conflicts
	// Use longer future times to ensure sufficient time for cancellation
	schedulingStartTime := time.Now()
	estimatedSchedulingTime := time.Duration(numCallbacks) * 15 * time.Second // ~15 seconds per transaction

	for i := 0; i < numCallbacks; i++ {
		// Add timestamp to prevent overlap with previous test runs
		testData := fmt.Sprintf("test-cancel-%d-%d", i, time.Now().UnixNano())
		priority := uint8(i % 3) // Priorities 0, 1, 2

		// Calculate future seconds based on estimated total scheduling time plus buffer
		// Ensure we have enough time for all scheduling + cancellation + buffer
		var futureSeconds int
		if i < numToCancelCallback {
			// For callbacks to be canceled: scheduling time + cancellation time + 3 minute buffer
			futureSeconds = int(estimatedSchedulingTime.Seconds()) + int(time.Duration(numToCancelCallback)*15*time.Second/time.Second) + 180 + rand.Intn(60)
		} else {
			// For callbacks to execute: slightly shorter but still sufficient
			futureSeconds = int(estimatedSchedulingTime.Seconds()) + 120 + rand.Intn(30)
		}

		callback, err := ct.scheduleCallbackWithStorage(testData, priority, futureSeconds)

		resultChan <- scheduleResult{
			callback: callback,
			err:      err,
			waitTime: time.Duration(futureSeconds) * time.Second,
		}
	}

	// Collect results
	var allCallbacks []ScheduledCallback
	for i := 0; i < numCallbacks; i++ {
		result := <-resultChan
		if result.err != nil {
			t.Fatalf("Failed to schedule callback: %v", result.err)
		}

		allCallbacks = append(allCallbacks, result.callback)
		if result.waitTime > maxWaitTime {
			maxWaitTime = result.waitTime
		}

		t.Logf("Scheduled callback %d/%d with data: %s, TxID: %d", i+1, numCallbacks, result.callback.Data, result.callback.TxID)
	}

	schedulingDuration := time.Since(schedulingStartTime)
	t.Logf("Completed scheduling %d callbacks in %v", numCallbacks, schedulingDuration)

	// Cancel some callbacks immediately after scheduling to ensure we cancel before execution
	startCancelTime := time.Now()
	for i := 0; i < numToCancelCallback && i < len(allCallbacks); i++ {
		callback := allCallbacks[i]
		timeUntilExecution := time.Unix(int64(callback.Timestamp), 0).Sub(time.Now())
		t.Logf("Canceling callback with data: %s, TxID: %d, scheduled for: %.1f, time until execution: %v",
			callback.Data, callback.TxID, callback.Timestamp, timeUntilExecution)

		// Validate we have sufficient time before execution
		if timeUntilExecution < 30*time.Second {
			t.Errorf("WARNING: Only %v until execution for callback %d - may not cancel in time", timeUntilExecution, callback.TxID)
		}

		err := ct.cancelCallback(callback.TxID)
		if err != nil {
			t.Errorf("Failed to cancel callback with TxID %d: %v", callback.TxID, err)
			continue
		}

		cancelTime := time.Since(startCancelTime)
		t.Logf("Successfully canceled callback TxID %d in %v", callback.TxID, cancelTime)
		canceledCallbacks = append(canceledCallbacks, callback)
	}

	t.Logf("Canceled %d callbacks, now waiting for execution phase...", len(canceledCallbacks))

	// The remaining callbacks should execute
	for i := numToCancelCallback; i < len(allCallbacks); i++ {
		scheduledCallbacks = append(scheduledCallbacks, allCallbacks[i])
	}

	// Wait longer to ensure all non-canceled callbacks execute
	maxWaitTime += 30 * time.Second
	eventOutput, err := ct.waitForEventsWithRetry(maxWaitTime)
	if err != nil {
		t.Fatalf("Failed to get events: %v", err)
	}

	// Validate executed callbacks
	executedData := ct.parseExecutedData(eventOutput)

	// Filter executed data to only include callbacks from this test run
	var filteredExecutedData []string
	allTestData := make(map[string]bool)
	for _, callback := range allCallbacks {
		allTestData[callback.Data] = true
	}

	executedMap := make(map[string]bool)
	for _, data := range executedData {
		t.Logf("TestFlowCallbackHandler.CallbackExecuted event data: '%s'", data)
		if allTestData[data] {
			// This executed callback belongs to the current test
			filteredExecutedData = append(filteredExecutedData, data)
			executedMap[data] = true
		} else {
			t.Logf("Ignoring executed callback from previous test: '%s'", data)
		}
	}

	// Use filtered data for validation
	executedData = filteredExecutedData

	// Check that non-canceled callbacks were executed
	for _, scheduled := range scheduledCallbacks {
		if !executedMap[scheduled.Data] {
			t.Errorf("Non-canceled callback with data '%s' was not executed", scheduled.Data)
		} else {
			t.Logf("Non-canceled callback with data '%s' executed successfully", scheduled.Data)
		}
	}

	// Check that canceled callbacks were NOT executed
	for _, canceled := range canceledCallbacks {
		if executedMap[canceled.Data] {
			t.Errorf("Canceled callback with data '%s' was unexpectedly executed", canceled.Data)
		} else {
			t.Logf("Canceled callback with data '%s' correctly not executed", canceled.Data)
		}
	}

	// Validate that we have canceled events in the output
	hasCanceledEvents := strings.Contains(eventOutput, "FlowTransactionScheduler.Canceled")
	if numToCancelCallback > 0 && !hasCanceledEvents {
		t.Errorf("Expected to find Canceled events but none were found")
	} else if numToCancelCallback > 0 {
		t.Logf("Successfully found Canceled events in output")

		// Check each canceled callback specifically
		for _, canceled := range canceledCallbacks {
			canceledEventFound := ct.checkForCanceledEvent(eventOutput, canceled.TxID)
			if !canceledEventFound {
				t.Errorf("No Canceled event found for TxID %d", canceled.TxID)
			}
		}
	}

	// Validate timing: ensure canceled callbacks had sufficient time before their scheduled execution
	currentTime := time.Now()
	for _, canceled := range canceledCallbacks {
		scheduledTime := time.Unix(int64(canceled.Timestamp), 0)
		timeDiff := scheduledTime.Sub(currentTime)
		if timeDiff <= -60*time.Second {
			t.Logf("Canceled callback TxID %d was successfully canceled before execution", canceled.TxID)
		}
	}

	// Validate that we have the expected number of canceled vs executed callbacks
	expectedExecuted := numCallbacks - numToCancelCallback
	actualExecuted := len(executedData)
	actualCanceled := len(canceledCallbacks)

	if actualExecuted != expectedExecuted {
		t.Errorf("Expected %d executed callbacks but got %d", expectedExecuted, actualExecuted)
	}

	if actualCanceled != numToCancelCallback {
		t.Errorf("Expected %d canceled callbacks but got %d", numToCancelCallback, actualCanceled)
	}

	// Validate block timestamps for executed callbacks
	blockNumbers := ct.extractBlockNumbers(eventOutput)
	for _, blockNum := range blockNumbers {
		if blockTime, err := ct.getBlockTimestamp(blockNum); err == nil {
			t.Logf("Block #%s has timestamp %.1f", blockNum, blockTime)
		}
	}

	t.Logf("Multiple callbacks with cancellation test completed successfully - scheduled %d, canceled %d, executed %d",
		numCallbacks, numToCancelCallback, len(executedData))
}
