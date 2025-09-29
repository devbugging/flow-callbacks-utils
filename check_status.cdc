import "FlowTransactionScheduler"

access(all) fun main() {
            // Get current timestamp
        let currentTimestamp = getCurrentBlock().timestamp
        
        // one year ago
        let oneYearInSeconds: UFix64 = 1726587702.0
        let startTimestamp = currentTimestamp - oneYearInSeconds
        
        // Call getTransactionsForTimeframe
        let result = FlowTransactionScheduler.getTransactionsForTimeframe(
            startTimestamp: startTimestamp, 
            endTimestamp: currentTimestamp
        )
        
        if result.length > 0 {
            panic("Expected empty result from getTransactionsForTimeframe, but got transactions: ".concat(result.length.toString()))
        }
        
        log("Success: getTransactionsForTimeframe returned empty result as expected")
}