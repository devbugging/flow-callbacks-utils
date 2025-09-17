import "FlowTransactionScheduler"
import "FlowToken"
import "FungibleToken"

transaction(transactionId: UInt64) {

    prepare(account: auth(BorrowValue, SaveValue, LoadValue) &Account) {
        
        // Borrow the scheduled transaction from storage
        let scheduledTx <- account.storage.load<@FlowTransactionScheduler.ScheduledTransaction>(from: StoragePath(identifier: "scheduledTx_".concat(transactionId.toString()))!)
            ?? panic("Could not load scheduled transaction with ID ".concat(transactionId.toString()))
        
        // Cancel the transaction and get refunded fees
        let refundedFees <- FlowTransactionScheduler.cancel(scheduledTx: <-scheduledTx)
        
        // Deposit refunded fees back to the account's vault
        let vault = account.storage.borrow<&FlowToken.Vault>(from: /storage/flowTokenVault)
            ?? panic("Could not borrow FlowToken vault")
        
        vault.deposit(from: <-refundedFees)
    }
}
