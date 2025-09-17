import "FlowTransactionScheduler"

access(all) contract TestFlowCallbackHandler {
    access(all) let HandlerStoragePath: StoragePath
    access(all) let HandlerPublicPath: PublicPath
    
    access(all) event CallbackExecuted(data: String)

    access(all) resource Handler: FlowTransactionScheduler.TransactionHandler {
        
        access(FlowTransactionScheduler.Execute) 
        fun executeTransaction(id: UInt64, data: AnyStruct?) {
            if let stringRef = data as? &String {
                if *stringRef == "fail" {
                    panic("Callback execution failed as requested")
                }
                emit CallbackExecuted(data: *stringRef)
            } else if let string: String = data as? String {
                if string == "fail" {
                    panic("Callback execution failed as requested")
                }
                emit CallbackExecuted(data: string)
            } else {
                emit CallbackExecuted(data: "bloop")
            }
        }
    }

    access(all) fun createHandler(): @Handler {
        return <- create Handler()
    }

    access(all) init() {
        self.HandlerStoragePath = /storage/testCallbackHandler
        self.HandlerPublicPath = /public/testCallbackHandler
    }
} 