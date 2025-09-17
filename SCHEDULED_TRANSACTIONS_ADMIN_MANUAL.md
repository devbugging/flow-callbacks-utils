# Flow Scheduled Transactions - Admin Control Manual

## Overview

This manual provides administrators with comprehensive guidance on controlling the Flow Scheduled Transaction system through configuration parameters. The system can be disabled, throttled, or emergency-stopped through smart contract configuration changes, which is the recommended approach over node-level configuration flags.

## Quick Emergency Actions

### 🚨 EMERGENCY: Completely Disable System
```cadence
// Set all limits to 0 to prevent new scheduling and execution
let emergencyConfig = FlowTransactionScheduler.Config(
    maximumIndividualEffort: 0,
    minimumExecutionEffort: 1,  // Must be > 0 due to validation
    slotSharedEffortLimit: 0,
    priorityEffortReserve: {
        FlowTransactionScheduler.Priority.High: 0,
        FlowTransactionScheduler.Priority.Medium: 0,
        FlowTransactionScheduler.Priority.Low: 0
    },
    priorityEffortLimit: {
        FlowTransactionScheduler.Priority.High: 0,
        FlowTransactionScheduler.Priority.Medium: 0,
        FlowTransactionScheduler.Priority.Low: 0
    },
    maxDataSizeMB: 0.0,
    priorityFeeMultipliers: {
        FlowTransactionScheduler.Priority.High: 10.0,
        FlowTransactionScheduler.Priority.Medium: 5.0,
        FlowTransactionScheduler.Priority.Low: 2.0
    },
    refundMultiplier: 0.5,
    canceledTransactionsLimit: 1000,
    collectionEffortLimit: 0,        // Prevents execution
    collectionTransactionsLimit: 0   // Prevents execution
)
```

## Control Mechanisms

### 1. Disable New Transaction Scheduling

#### Method 1: Set Maximum Effort to 0
```cadence
// Prevents all new transactions from being scheduled
let config = currentConfig
config.maximumIndividualEffort = 0
scheduler.setConfig(newConfig: config)
```

#### Method 2: Set Priority Effort Limits to 0
```cadence
// Prevents new transactions of specific priorities
let config = currentConfig
config.priorityEffortLimit = {
    FlowTransactionScheduler.Priority.High: 0,
    FlowTransactionScheduler.Priority.Medium: 0,
    FlowTransactionScheduler.Priority.Low: 0
}
scheduler.setConfig(newConfig: config)
```

#### Method 3: Set Data Size Limit to 0
```cadence
// Prevents transactions with any data payload
let config = currentConfig
config.maxDataSizeMB = 0.0
scheduler.setConfig(newConfig: config)
```

### 2. Disable Execution of Scheduled Transactions

#### Method 1: Set Collection Effort Limit to 0
```cadence
// Prevents any transactions from being included in execution collections
let config = currentConfig
config.collectionEffortLimit = 0
scheduler.setConfig(newConfig: config)
```

#### Method 2: Set Collection Transaction Limit to 0
```cadence
// Prevents any transactions from being processed per block
let config = currentConfig
config.collectionTransactionsLimit = 0
scheduler.setConfig(newConfig: config)
```

### 3. Slow Down Execution

#### Reduce Collection Limits
```cadence
// Process only 1 transaction per block with minimal effort
let config = currentConfig
config.collectionEffortLimit = 100        // Very low effort limit
config.collectionTransactionsLimit = 1    // Only 1 transaction per block
scheduler.setConfig(newConfig: config)
```

#### Reduce Individual Transaction Limits
```cadence
// Force all new transactions to use minimal effort
let config = currentConfig
config.maximumIndividualEffort = 50       // Very low maximum effort
config.priorityEffortLimit = {
    FlowTransactionScheduler.Priority.High: 30,
    FlowTransactionScheduler.Priority.Medium: 20,
    FlowTransactionScheduler.Priority.Low: 10
}
scheduler.setConfig(newConfig: config)
```

#### Reduce Slot Capacity
```cadence
// Reduce slot capacity to create scheduling delays
let config = currentConfig
config.slotTotalEffortLimit = 100         // Very small slot capacity
config.slotSharedEffortLimit = 50
config.priorityEffortReserve = {
    FlowTransactionScheduler.Priority.High: 30,
    FlowTransactionScheduler.Priority.Medium: 20,
    FlowTransactionScheduler.Priority.Low: 0
}
scheduler.setConfig(newConfig: config)
```

## Configuration Parameters Reference

### Scheduling Control Parameters

| Parameter | Description | Effect When Set to 0 | Emergency Use |
|-----------|-------------|----------------------|---------------|
| `maximumIndividualEffort` | Max effort per transaction | Blocks all new scheduling | ✅ |
| `priorityEffortLimit[High/Medium/Low]` | Max effort per priority per slot | Blocks new scheduling for that priority | ✅ |
| `maxDataSizeMB` | Max data size per transaction | Blocks transactions with data | ✅ |
| `slotTotalEffortLimit` | Total effort per timestamp slot | Severely limits scheduling capacity | ⚠️ |

### Execution Control Parameters

| Parameter | Description | Effect When Set to 0 | Emergency Use |
|-----------|-------------|----------------------|---------------|
| `collectionEffortLimit` | Max effort per execution collection | Stops all execution | ✅ |
| `collectionTransactionsLimit` | Max transactions per collection | Stops all execution | ✅ |

### Other Parameters

| Parameter | Description | Typical Range | Notes |
|-----------|-------------|---------------|-------|
| `minimumExecutionEffort` | Min effort required | 10-100 | Cannot be 0 |
| `priorityFeeMultipliers` | Fee scaling by priority | 1.0-20.0 | Higher = more expensive |
| `refundMultiplier` | Refund percentage on cancel | 0.0-1.0 | 0.5 = 50% refund |
| `canceledTransactionsLimit` | Canceled transaction history | 100-10000 | Storage management |

## Example Configuration Scripts

### Emergency Stop Everything
```cadence
import FlowTransactionScheduler from 0x123...

transaction {
    prepare(signer: auth(Storage, Capabilities) &Account) {
        let schedulerRef = signer.storage.borrow<auth(FlowTransactionScheduler.UpdateConfig) &FlowTransactionScheduler.SharedScheduler>(
            from: FlowTransactionScheduler.storagePath
        ) ?? panic("Could not borrow scheduler")

        let emergencyConfig = FlowTransactionScheduler.Config(
            maximumIndividualEffort: 0,
            minimumExecutionEffort: 1,
            slotSharedEffortLimit: 0,
            priorityEffortReserve: {
                FlowTransactionScheduler.Priority.High: 0,
                FlowTransactionScheduler.Priority.Medium: 0,
                FlowTransactionScheduler.Priority.Low: 0
            },
            priorityEffortLimit: {
                FlowTransactionScheduler.Priority.High: 0,
                FlowTransactionScheduler.Priority.Medium: 0,
                FlowTransactionScheduler.Priority.Low: 0
            },
            maxDataSizeMB: 0.0,
            priorityFeeMultipliers: {
                FlowTransactionScheduler.Priority.High: 10.0,
                FlowTransactionScheduler.Priority.Medium: 5.0,
                FlowTransactionScheduler.Priority.Low: 2.0
            },
            refundMultiplier: 0.5,
            canceledTransactionsLimit: 1000,
            collectionEffortLimit: 0,
            collectionTransactionsLimit: 0
        )

        schedulerRef.setConfig(newConfig: emergencyConfig)
    }
}
```

### Throttle to Minimal Activity
```cadence
import FlowTransactionScheduler from 0x123...

transaction {
    prepare(signer: auth(Storage, Capabilities) &Account) {
        let schedulerRef = signer.storage.borrow<auth(FlowTransactionScheduler.UpdateConfig) &FlowTransactionScheduler.SharedScheduler>(
            from: FlowTransactionScheduler.storagePath
        ) ?? panic("Could not borrow scheduler")

        let throttleConfig = FlowTransactionScheduler.Config(
            maximumIndividualEffort: 100,     // Very low individual limit
            minimumExecutionEffort: 10,
            slotSharedEffortLimit: 50,        // Small shared pool
            priorityEffortReserve: {
                FlowTransactionScheduler.Priority.High: 50,
                FlowTransactionScheduler.Priority.Medium: 25,
                FlowTransactionScheduler.Priority.Low: 0
            },
            priorityEffortLimit: {
                FlowTransactionScheduler.Priority.High: 100,
                FlowTransactionScheduler.Priority.Medium: 75,
                FlowTransactionScheduler.Priority.Low: 50
            },
            maxDataSizeMB: 0.1,               // Very small data limit
            priorityFeeMultipliers: {
                FlowTransactionScheduler.Priority.High: 50.0,  // Make it expensive
                FlowTransactionScheduler.Priority.Medium: 25.0,
                FlowTransactionScheduler.Priority.Low: 10.0
            },
            refundMultiplier: 0.9,            // High refund to encourage cancellation
            canceledTransactionsLimit: 1000,
            collectionEffortLimit: 200,       // Very low collection limit
            collectionTransactionsLimit: 2    // Only 2 transactions per block
        )

        schedulerRef.setConfig(newConfig: emergencyConfig)
    }
}
```

### Restore Normal Operation
```cadence
import FlowTransactionScheduler from 0x123...

transaction {
    prepare(signer: auth(Storage, Capabilities) &Account) {
        let schedulerRef = signer.storage.borrow<auth(FlowTransactionScheduler.UpdateConfig) &FlowTransactionScheduler.SharedScheduler>(
            from: FlowTransactionScheduler.storagePath
        ) ?? panic("Could not borrow scheduler")

        // Restore default production values
        let normalConfig = FlowTransactionScheduler.Config(
            maximumIndividualEffort: 9999,
            minimumExecutionEffort: 10,
            slotSharedEffortLimit: 10_000,
            priorityEffortReserve: {
                FlowTransactionScheduler.Priority.High: 20_000,
                FlowTransactionScheduler.Priority.Medium: 5_000,
                FlowTransactionScheduler.Priority.Low: 0
            },
            priorityEffortLimit: {
                FlowTransactionScheduler.Priority.High: 30_000,
                FlowTransactionScheduler.Priority.Medium: 15_000,
                FlowTransactionScheduler.Priority.Low: 5_000
            },
            maxDataSizeMB: 3.0,
            priorityFeeMultipliers: {
                FlowTransactionScheduler.Priority.High: 10.0,
                FlowTransactionScheduler.Priority.Medium: 5.0,
                FlowTransactionScheduler.Priority.Low: 2.0
            },
            refundMultiplier: 0.5,
            canceledTransactionsLimit: 1000,
            collectionEffortLimit: 500_000,
            collectionTransactionsLimit: 150
        )

        schedulerRef.setConfig(newConfig: normalConfig)
    }
}
```

## Node-Level Control (Not Recommended)

### Flow Node Configuration Flag
```bash
# Add to node startup parameters - requires ALL nodes to be updated
--scheduled-callbacks-enabled=false
```

**⚠️ Important:** This approach requires coordinating updates across all nodes in the network and is much more complex than smart contract configuration changes. Use smart contract configuration instead.

## Monitoring and Verification

### Check Current Configuration
```cadence
import FlowTransactionScheduler from 0x123...

access(all) fun main(): {String: AnyStruct} {
    let config = FlowTransactionScheduler.getConfig()
    return {
        "maximumIndividualEffort": config.maximumIndividualEffort,
        "collectionEffortLimit": config.collectionEffortLimit,
        "collectionTransactionsLimit": config.collectionTransactionsLimit,
        "priorityEffortLimits": config.priorityEffortLimit,
        "maxDataSizeMB": config.maxDataSizeMB
    }
}
```

### Monitor Events
Listen for these events to verify configuration changes:
- `FlowTransactionScheduler.ConfigUpdated` - Configuration was changed
- `FlowTransactionScheduler.CollectionLimitReached` - Execution limits are being hit
- `FlowTransactionScheduler.Scheduled` - New transactions (should stop when disabled)
- `FlowTransactionScheduler.PendingExecution` - Executions starting (should stop when disabled)

## Recovery Procedures

### Gradual Re-enabling
1. **Start with execution disabled, scheduling enabled at low limits**
2. **Gradually increase collection limits**
3. **Monitor system performance**
4. **Increase scheduling limits**
5. **Return to normal operation**

### Example Gradual Recovery
```cadence
// Step 1: Enable minimal scheduling, no execution
config.maximumIndividualEffort = 100
config.collectionEffortLimit = 0
config.collectionTransactionsLimit = 0

// Step 2: Enable minimal execution
config.collectionEffortLimit = 1000
config.collectionTransactionsLimit = 5

// Step 3: Gradually increase limits
config.collectionEffortLimit = 10_000
config.collectionTransactionsLimit = 25

// Step 4: Return to normal
config.collectionEffortLimit = 500_000
config.collectionTransactionsLimit = 150
config.maximumIndividualEffort = 9999
```

## Best Practices

### Emergency Response
1. **Use smart contract configuration, not node flags**
2. **Set multiple limits to 0 for complete shutdown**
3. **Monitor events to verify changes took effect**
4. **Communicate changes to users/developers**

### Operational Monitoring
1. **Monitor `CollectionLimitReached` events**
2. **Track scheduling vs execution rates**
3. **Watch for unusual transaction patterns**
4. **Set up alerting on configuration changes**

### Testing Changes
1. **Test configuration changes on testnet first**
2. **Verify transaction scheduling fails as expected**
3. **Verify execution stops as expected**
4. **Test recovery procedures**

## Troubleshooting

### Issue: Transactions still being scheduled after setting limits to 0
- **Check:** Verify `maximumIndividualEffort` is 0
- **Check:** Verify `priorityEffortLimit` values are all 0
- **Check:** Verify `maxDataSizeMB` is 0.0 if transactions have data

### Issue: Transactions still executing after setting collection limits to 0
- **Check:** Verify both `collectionEffortLimit` and `collectionTransactionsLimit` are 0
- **Monitor:** Watch for `CollectionLimitReached` events

### Issue: Configuration changes not taking effect
- **Verify:** Transaction that updates config was successful
- **Check:** Listen for `ConfigUpdated` event
- **Confirm:** Query current config with read script

## Emergency Contacts

When implementing emergency changes:
1. **Notify stakeholders immediately**
2. **Document reason for changes**
3. **Provide timeline for restoration**
4. **Monitor system behavior continuously**

---

**Remember**: Smart contract configuration changes are immediate and affect the entire network. Always test on testnet first when possible, and have a clear recovery plan before making emergency changes.
