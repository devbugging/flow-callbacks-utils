import "FlowTransactionScheduler"
import "TestFlowCallbackHandler"
import "FlowToken" 
import "FungibleToken" 


transaction(timestamp: UFix64, feeAmount: UFix64, effort: UInt64, priority: UInt8, testData: String) {

    prepare(account: auth(BorrowValue, SaveValue, IssueStorageCapabilityController, PublishCapability, GetStorageCapabilityController) &Account) {
        
        // If a transaction handler has not been created for this account yet, create one,
        // store it, and issue a capability that will be used to create the scheduled transaction
        if !account.storage.check<@TestFlowCallbackHandler.Handler>(from: TestFlowCallbackHandler.HandlerStoragePath) {
            let handler <- TestFlowCallbackHandler.createHandler()
        
            account.storage.save(<-handler, to: TestFlowCallbackHandler.HandlerStoragePath)
            account.capabilities.storage.issue<auth(FlowTransactionScheduler.Execute) &{FlowTransactionScheduler.TransactionHandler}>(TestFlowCallbackHandler.HandlerStoragePath)
        }

        // Get the capability that will be used to create the scheduled transaction
        let handlerCap = account.capabilities.storage
                            .getControllers(forPath: TestFlowCallbackHandler.HandlerStoragePath)[0]
                            .capability as! Capability<auth(FlowTransactionScheduler.Execute) &{FlowTransactionScheduler.TransactionHandler}>
        
        // borrow a reference to the vault that will be used for fees
        let vault = account.storage.borrow<auth(FungibleToken.Withdraw) &FlowToken.Vault>(from: /storage/flowTokenVault)
            ?? panic("Could not borrow FlowToken vault")
        
        let fees <- vault.withdraw(amount: feeAmount) as! @FlowToken.Vault
        let priorityEnum = FlowTransactionScheduler.Priority(rawValue: priority)
            ?? FlowTransactionScheduler.Priority.High

        // Schedule the transaction with the main contract
        let scheduledTransaction <- FlowTransactionScheduler.schedule(
            handlerCap: handlerCap,
            data: testData,
            timestamp: timestamp,
            priority: priorityEnum,
            executionEffort: effort,
            fees: <-fees
        )
        
        // Store the scheduled transaction for potential cancellation
        // Use the transaction ID in the storage path to make it unique
        let txID = scheduledTransaction.id
        let storagePath = StoragePath(identifier: "scheduledTx_".concat(txID.toString()))!
        account.storage.save(<-scheduledTransaction, to: storagePath)
    }
} 
