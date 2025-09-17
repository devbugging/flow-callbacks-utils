# Flow Scheduled Transactions - Admin Control Manual

## Overview

This manual provides administrators with specific configuration changes to control the Flow Scheduled Transaction system. Each control mechanism uses a single configuration parameter for maximum clarity and effectiveness.

## Control Actions

### 1. Disable New Transaction Scheduling
**Configuration:** `maximumIndividualEffort = 0`

This prevents all new transactions from being scheduled because any transaction with effort > 0 will fail validation.

```cadence
import FlowTransactionScheduler from 0x123...

transaction {
    prepare(signer: auth(Storage) &Account) {
        let schedulerRef = signer.storage.borrow<auth(FlowTransactionScheduler.UpdateConfig) &FlowTransactionScheduler.SharedScheduler>(
            from: FlowTransactionScheduler.storagePath
        ) ?? panic("Could not borrow scheduler")

        let currentConfig = schedulerRef.getConfig()
        let newConfig = FlowTransactionScheduler.Config(
            maximumIndividualEffort: 0,  // DISABLE SCHEDULING
            minimumExecutionEffort: currentConfig.minimumExecutionEffort,
            slotSharedEffortLimit: currentConfig.slotSharedEffortLimit,
            priorityEffortReserve: currentConfig.priorityEffortReserve,
            priorityEffortLimit: currentConfig.priorityEffortLimit,
            maxDataSizeMB: currentConfig.maxDataSizeMB,
            priorityFeeMultipliers: currentConfig.priorityFeeMultipliers,
            refundMultiplier: currentConfig.refundMultiplier,
            canceledTransactionsLimit: currentConfig.canceledTransactionsLimit,
            collectionEffortLimit: currentConfig.collectionEffortLimit,
            collectionTransactionsLimit: currentConfig.collectionTransactionsLimit
        )

        schedulerRef.setConfig(newConfig: newConfig)
    }
}
```

### 2. Disable Execution of All Transactions (Existing and New)
**Configuration:** `collectionTransactionsLimit = 0`

This prevents any transactions from being processed per block, effectively stopping all execution.

```cadence
import FlowTransactionScheduler from 0x123...

transaction {
    prepare(signer: auth(Storage) &Account) {
        let schedulerRef = signer.storage.borrow<auth(FlowTransactionScheduler.UpdateConfig) &FlowTransactionScheduler.SharedScheduler>(
            from: FlowTransactionScheduler.storagePath
        ) ?? panic("Could not borrow scheduler")

        let currentConfig = schedulerRef.getConfig()
        let newConfig = FlowTransactionScheduler.Config(
            maximumIndividualEffort: currentConfig.maximumIndividualEffort,
            minimumExecutionEffort: currentConfig.minimumExecutionEffort,
            slotSharedEffortLimit: currentConfig.slotSharedEffortLimit,
            priorityEffortReserve: currentConfig.priorityEffortReserve,
            priorityEffortLimit: currentConfig.priorityEffortLimit,
            maxDataSizeMB: currentConfig.maxDataSizeMB,
            priorityFeeMultipliers: currentConfig.priorityFeeMultipliers,
            refundMultiplier: currentConfig.refundMultiplier,
            canceledTransactionsLimit: currentConfig.canceledTransactionsLimit,
            collectionEffortLimit: currentConfig.collectionEffortLimit,
            collectionTransactionsLimit: 0  // DISABLE EXECUTION
        )

        schedulerRef.setConfig(newConfig: newConfig)
    }
}
```

### 3. Disable Scheduling for Specific Priority
**Configuration:** Set `priorityEffortLimit[Priority] = 0`

This prevents new transactions of the specified priority from being scheduled.

#### Disable High Priority Scheduling
```cadence
import FlowTransactionScheduler from 0x123...

transaction {
    prepare(signer: auth(Storage) &Account) {
        let schedulerRef = signer.storage.borrow<auth(FlowTransactionScheduler.UpdateConfig) &FlowTransactionScheduler.SharedScheduler>(
            from: FlowTransactionScheduler.storagePath
        ) ?? panic("Could not borrow scheduler")

        let currentConfig = schedulerRef.getConfig()
        let newConfig = FlowTransactionScheduler.Config(
            maximumIndividualEffort: currentConfig.maximumIndividualEffort,
            minimumExecutionEffort: currentConfig.minimumExecutionEffort,
            slotSharedEffortLimit: currentConfig.slotSharedEffortLimit,
            priorityEffortReserve: currentConfig.priorityEffortReserve,
            priorityEffortLimit: {
                FlowTransactionScheduler.Priority.High: 0,  // DISABLE HIGH PRIORITY
                FlowTransactionScheduler.Priority.Medium: currentConfig.priorityEffortLimit[FlowTransactionScheduler.Priority.Medium]!,
                FlowTransactionScheduler.Priority.Low: currentConfig.priorityEffortLimit[FlowTransactionScheduler.Priority.Low]!
            },
            maxDataSizeMB: currentConfig.maxDataSizeMB,
            priorityFeeMultipliers: currentConfig.priorityFeeMultipliers,
            refundMultiplier: currentConfig.refundMultiplier,
            canceledTransactionsLimit: currentConfig.canceledTransactionsLimit,
            collectionEffortLimit: currentConfig.collectionEffortLimit,
            collectionTransactionsLimit: currentConfig.collectionTransactionsLimit
        )

        schedulerRef.setConfig(newConfig: newConfig)
    }
}
```

#### Disable All Priority Scheduling
```cadence
import FlowTransactionScheduler from 0x123...

transaction {
    prepare(signer: auth(Storage) &Account) {
        let schedulerRef = signer.storage.borrow<auth(FlowTransactionScheduler.UpdateConfig) &FlowTransactionScheduler.SharedScheduler>(
            from: FlowTransactionScheduler.storagePath
        ) ?? panic("Could not borrow scheduler")

        let currentConfig = schedulerRef.getConfig()
        let newConfig = FlowTransactionScheduler.Config(
            maximumIndividualEffort: currentConfig.maximumIndividualEffort,
            minimumExecutionEffort: currentConfig.minimumExecutionEffort,
            slotSharedEffortLimit: currentConfig.slotSharedEffortLimit,
            priorityEffortReserve: currentConfig.priorityEffortReserve,
            priorityEffortLimit: {
                FlowTransactionScheduler.Priority.High: 0,    // DISABLE ALL
                FlowTransactionScheduler.Priority.Medium: 0,  // DISABLE ALL  
                FlowTransactionScheduler.Priority.Low: 0      // DISABLE ALL
            },
            maxDataSizeMB: currentConfig.maxDataSizeMB,
            priorityFeeMultipliers: currentConfig.priorityFeeMultipliers,
            refundMultiplier: currentConfig.refundMultiplier,
            canceledTransactionsLimit: currentConfig.canceledTransactionsLimit,
            collectionEffortLimit: currentConfig.collectionEffortLimit,
            collectionTransactionsLimit: currentConfig.collectionTransactionsLimit
        )

        schedulerRef.setConfig(newConfig: newConfig)
    }
}
```

### 4. Slow Down Execution (Throttle)
**Configuration:** `collectionTransactionsLimit = 1`

This limits execution to 1 transaction per block, significantly slowing down the system.

```cadence
import FlowTransactionScheduler from 0x123...

transaction {
    prepare(signer: auth(Storage) &Account) {
        let schedulerRef = signer.storage.borrow<auth(FlowTransactionScheduler.UpdateConfig) &FlowTransactionScheduler.SharedScheduler>(
            from: FlowTransactionScheduler.storagePath
        ) ?? panic("Could not borrow scheduler")

        let currentConfig = schedulerRef.getConfig()
        let newConfig = FlowTransactionScheduler.Config(
            maximumIndividualEffort: currentConfig.maximumIndividualEffort,
            minimumExecutionEffort: currentConfig.minimumExecutionEffort,
            slotSharedEffortLimit: currentConfig.slotSharedEffortLimit,
            priorityEffortReserve: currentConfig.priorityEffortReserve,
            priorityEffortLimit: currentConfig.priorityEffortLimit,
            maxDataSizeMB: currentConfig.maxDataSizeMB,
            priorityFeeMultipliers: currentConfig.priorityFeeMultipliers,
            refundMultiplier: currentConfig.refundMultiplier,
            canceledTransactionsLimit: currentConfig.canceledTransactionsLimit,
            collectionEffortLimit: currentConfig.collectionEffortLimit,
            collectionTransactionsLimit: 1  // THROTTLE TO 1 TX PER BLOCK
        )

        schedulerRef.setConfig(newConfig: newConfig)
    }
}
```

### 5. Restore Normal Operation
**Configuration:** Restore default values

```cadence
import FlowTransactionScheduler from 0x123...

transaction {
    prepare(signer: auth(Storage) &Account) {
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

## Monitoring Configuration Changes

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

