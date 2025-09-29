// SPDX-License-Identifier: MIT
pragma solidity ^0.8.20;

/// @title IFlowTransactionScheduler
/// @notice Interface for the FlowTransactionScheduler contract
/// @dev Enables smart contracts to schedule autonomous execution in the future
interface IFlowTransactionScheduler {
    // Enums
    enum Priority {
        High,
        Medium,
        Low
    }

    enum Status {
        Unknown,
        Scheduled,
        Executed,
        Canceled
    }

    // Structs
    struct EstimatedScheduledTransaction {
        uint256 flowFee;
        uint256 timestamp;
        string error;
    }

    struct TransactionData {
        uint64 id;
        Priority priority;
        uint64 executionEffort;
        Status status;
        uint256 fees;
        uint256 scheduledTimestamp;
        address handler;
        string handlerTypeIdentifier;
        address handlerAddress;
        bytes data;
    }

    // Events
    event Scheduled(
        uint64 indexed id,
        uint8 priority,
        uint256 timestamp,
        uint64 executionEffort,
        uint256 fees,
        address transactionHandlerOwner,
        string transactionHandlerTypeIdentifier
    );

    event PendingExecution(
        uint64 indexed id,
        uint8 priority,
        uint64 executionEffort,
        uint256 fees,
        address transactionHandlerOwner,
        string transactionHandlerTypeIdentifier
    );

    event Executed(
        uint64 indexed id,
        uint8 priority,
        uint64 executionEffort,
        address transactionHandlerOwner,
        string transactionHandlerTypeIdentifier
    );

    event Canceled(
        uint64 indexed id,
        uint8 priority,
        uint256 feesReturned,
        uint256 feesDeducted,
        address transactionHandlerOwner,
        string transactionHandlerTypeIdentifier
    );

    event CollectionLimitReached(
        uint64 collectionEffortLimit,
        int256 collectionTransactionsLimit
    );

    event ConfigUpdated();

    // Core scheduling functions
    
    /// @notice Schedule a transaction for future execution
    /// @param handlerCap Address of the transaction handler contract
    /// @param data Encoded data to pass to the handler
    /// @param timestamp Desired execution timestamp
    /// @param priority Transaction priority level
    /// @param executionEffort Computational effort required
    /// @param fees Fee amount to pay for scheduling
    /// @return transactionId The assigned transaction ID
    /// @return scheduledTimestamp The actual scheduled execution timestamp
    function schedule(
        address handlerCap,
        bytes calldata data,
        uint256 timestamp,
        Priority priority,
        uint64 executionEffort,
        uint256 fees
    ) external returns (uint64 transactionId, uint256 scheduledTimestamp);

    /// @notice Estimate fees and validate scheduling parameters
    /// @param data Encoded data for the transaction
    /// @param timestamp Desired execution timestamp
    /// @param priority Transaction priority level
    /// @param executionEffort Computational effort required
    /// @return estimate Estimated transaction details including fees and errors
    function estimate(
        bytes calldata data,
        uint256 timestamp,
        Priority priority,
        uint64 executionEffort
    ) external view returns (EstimatedScheduledTransaction memory estimate);

    /// @notice Cancel a scheduled transaction
    /// @param id Transaction ID to cancel
    /// @return refundedFees Amount of fees refunded to the caller
    function cancel(uint64 id) external returns (uint256 refundedFees);

    // Processing functions (restricted access)
    
    /// @notice Process pending transactions (system call)
    function process() external;

    /// @notice Execute a specific transaction (system call)
    /// @param id Transaction ID to execute
    function executeTransaction(uint64 id) external;

    // View functions
    
    /// @notice Get the status of a transaction
    /// @param id Transaction ID
    /// @return status Current status of the transaction
    function getStatus(uint64 id) external view returns (Status status);

    /// @notice Get full transaction data
    /// @param id Transaction ID
    /// @return transactionData Complete transaction information
    function getTransactionData(uint64 id) external view returns (TransactionData memory transactionData);

    /// @notice Get list of canceled transaction IDs
    /// @return canceledIds Array of canceled transaction IDs
    function getCanceledTransactions() external view returns (uint64[] memory canceledIds);

    /// @notice Get available execution effort for a time slot and priority
    /// @param timestamp Time slot to check
    /// @param priority Priority level
    /// @return availableEffort Available execution effort
    function getSlotAvailableEffort(uint256 timestamp, Priority priority) external view returns (uint64 availableEffort);

    /// @notice Get transactions scheduled within a timeframe
    /// @param startTimestamp Start of timeframe
    /// @param endTimestamp End of timeframe
    /// @return transactionIds Array of transaction IDs in the timeframe
    function getTransactionsForTimeframe(
        uint256 startTimestamp,
        uint256 endTimestamp
    ) external view returns (uint64[] memory transactionIds);

    // Configuration functions (admin only)
    
    /// @notice Update system configuration parameters
    /// @param _maximumIndividualEffort Maximum effort per transaction
    /// @param _minimumExecutionEffort Minimum effort required
    /// @param _slotSharedEffortLimit Shared effort limit per slot
    /// @param _priorityEffortReserve Reserved effort per priority [High, Medium, Low]
    /// @param _priorityEffortLimit Total effort limit per priority [High, Medium, Low]
    /// @param _maxDataSizeMB Maximum data size in MB
    /// @param _priorityFeeMultipliers Fee multipliers per priority [High, Medium, Low]
    /// @param _refundMultiplier Refund percentage for cancellations
    /// @param _canceledTransactionsLimit Max canceled transactions to track
    /// @param _collectionEffortLimit Collection processing effort limit
    /// @param _collectionTransactionsLimit Collection processing transaction limit
    function setConfig(
        uint64 _maximumIndividualEffort,
        uint64 _minimumExecutionEffort,
        uint64 _slotSharedEffortLimit,
        uint64[3] memory _priorityEffortReserve,
        uint64[3] memory _priorityEffortLimit,
        uint256 _maxDataSizeMB,
        uint256[3] memory _priorityFeeMultipliers,
        uint256 _refundMultiplier,
        uint256 _canceledTransactionsLimit,
        uint64 _collectionEffortLimit,
        int256 _collectionTransactionsLimit
    ) external;
}

/// @title ITransactionHandler
/// @notice Interface that transaction handlers must implement
interface ITransactionHandler {
    /// @notice Execute a scheduled transaction
    /// @param id Transaction ID
    /// @param data Encoded transaction data
    function executeTransaction(uint64 id, bytes calldata data) external;

    /// @notice Get available view functions
    /// @return views Array of view function identifiers
    function getViews() external view returns (bytes32[] memory views);

    /// @notice Resolve a specific view
    /// @param view View identifier
    /// @return result Encoded view result
    function resolveView(bytes32 view) external view returns (bytes memory result);
}
