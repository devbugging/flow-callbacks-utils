// SPDX-License-Identifier: MIT
pragma solidity ^0.8.20;

import "@openzeppelin/contracts/token/ERC20/IERC20.sol";
import "@openzeppelin/contracts/access/AccessControl.sol";
import "@openzeppelin/contracts/security/ReentrancyGuard.sol";
import "@openzeppelin/contracts/utils/math/Math.sol";

/// @title FlowTransactionScheduler
/// @notice Enables smart contracts to schedule autonomous execution in the future
/// @dev Implements scheduled transaction system with prioritization and fee management
contract FlowTransactionScheduler is AccessControl, ReentrancyGuard {
    using Math for uint256;

    // Access control roles
    bytes32 public constant EXECUTE_ROLE = keccak256("EXECUTE_ROLE");
    bytes32 public constant PROCESS_ROLE = keccak256("PROCESS_ROLE");
    bytes32 public constant CANCEL_ROLE = keccak256("CANCEL_ROLE");
    bytes32 public constant UPDATE_CONFIG_ROLE = keccak256("UPDATE_CONFIG_ROLE");

    // Token interface for fees (equivalent to FlowToken)
    IERC20 public flowToken;
    address public feeVault;
    address public storageFeeVault;

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

    event ResourceDestroyed(uint64 id, uint256 timestamp);

    // Interfaces
    interface ITransactionHandler {
        function executeTransaction(uint64 id, bytes calldata data) external;
        function getViews() external view returns (bytes32[] memory);
        function resolveView(bytes32 view) external view returns (bytes memory);
    }

    // Structs
    struct ScheduledTransaction {
        uint64 id;
        uint256 timestamp;
    }

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

    struct Config {
        uint64 maximumIndividualEffort;
        uint64 minimumExecutionEffort;
        uint64 slotTotalEffortLimit;
        uint64 slotSharedEffortLimit;
        mapping(Priority => uint64) priorityEffortReserve;
        mapping(Priority => uint64) priorityEffortLimit;
        uint256 maxDataSizeMB;
        mapping(Priority => uint256) priorityFeeMultipliers;
        uint256 refundMultiplier;
        uint256 canceledTransactionsLimit;
        uint64 collectionEffortLimit;
        int256 collectionTransactionsLimit;
    }

    struct SortedTimestamps {
        uint256[] timestamps;
    }

    // State variables
    uint64 private nextID;
    mapping(uint64 => TransactionData) private transactions;
    mapping(uint256 => mapping(Priority => mapping(uint64 => uint64))) private slotQueue;
    mapping(uint256 => mapping(Priority => uint64)) private slotUsedEffort;
    SortedTimestamps private sortedTimestamps;
    uint64[] private canceledTransactions;
    Config private config;

    // Constructor
    constructor(address _flowToken, address _feeVault, address _storageFeeVault) {
        flowToken = IERC20(_flowToken);
        feeVault = _feeVault;
        storageFeeVault = _storageFeeVault;

        _grantRole(DEFAULT_ADMIN_ROLE, msg.sender);
        _grantRole(EXECUTE_ROLE, msg.sender);
        _grantRole(PROCESS_ROLE, msg.sender);
        _grantRole(CANCEL_ROLE, msg.sender);
        _grantRole(UPDATE_CONFIG_ROLE, msg.sender);

        _initializeConfig();
        nextID = 1;
        canceledTransactions.push(0);
    }

    // Internal initialization function
    function _initializeConfig() private {
        uint64 sharedEffortLimit = 10_000;
        uint64 highPriorityEffortReserve = 20_000;
        uint64 mediumPriorityEffortReserve = 5_000;

        config.maximumIndividualEffort = 9999;
        config.minimumExecutionEffort = 10;
        config.slotSharedEffortLimit = sharedEffortLimit;
        config.slotTotalEffortLimit = sharedEffortLimit + highPriorityEffortReserve + mediumPriorityEffortReserve;

        config.priorityEffortReserve[Priority.High] = highPriorityEffortReserve;
        config.priorityEffortReserve[Priority.Medium] = mediumPriorityEffortReserve;
        config.priorityEffortReserve[Priority.Low] = 0;

        config.priorityEffortLimit[Priority.High] = highPriorityEffortReserve + sharedEffortLimit;
        config.priorityEffortLimit[Priority.Medium] = mediumPriorityEffortReserve + sharedEffortLimit;
        config.priorityEffortLimit[Priority.Low] = 5_000;

        config.maxDataSizeMB = 3 * 10**18; // 3.0 MB in wei representation

        config.priorityFeeMultipliers[Priority.High] = 10 * 10**18;
        config.priorityFeeMultipliers[Priority.Medium] = 5 * 10**18;
        config.priorityFeeMultipliers[Priority.Low] = 2 * 10**18;

        config.refundMultiplier = 5 * 10**17; // 0.5 in wei representation
        config.canceledTransactionsLimit = 1000;
        config.collectionEffortLimit = 500_000;
        config.collectionTransactionsLimit = 150;
    }

    // Configuration functions
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
    ) external onlyRole(UPDATE_CONFIG_ROLE) {
        require(_refundMultiplier >= 0 && _refundMultiplier <= 10**18, "Invalid refund multiplier");
        require(_priorityFeeMultipliers[2] >= 10**18, "Low priority multiplier must be >= 1.0");
        require(_priorityFeeMultipliers[1] > _priorityFeeMultipliers[2], "Medium priority must be > Low");
        require(_priorityFeeMultipliers[0] > _priorityFeeMultipliers[1], "High priority must be > Medium");
        require(_priorityEffortLimit[0] >= _priorityEffortReserve[0], "High effort limit must be >= reserve");
        require(_priorityEffortLimit[1] >= _priorityEffortReserve[1], "Medium effort limit must be >= reserve");
        require(_priorityEffortLimit[2] >= _priorityEffortReserve[2], "Low effort limit must be >= reserve");
        require(_collectionTransactionsLimit >= 0, "Collection transactions limit must be >= 0");
        require(_canceledTransactionsLimit >= 1, "Canceled transactions limit must be >= 1");

        config.maximumIndividualEffort = _maximumIndividualEffort;
        config.minimumExecutionEffort = _minimumExecutionEffort;
        config.slotSharedEffortLimit = _slotSharedEffortLimit;
        config.slotTotalEffortLimit = _slotSharedEffortLimit + _priorityEffortReserve[0] + _priorityEffortReserve[1];

        config.priorityEffortReserve[Priority.High] = _priorityEffortReserve[0];
        config.priorityEffortReserve[Priority.Medium] = _priorityEffortReserve[1];
        config.priorityEffortReserve[Priority.Low] = _priorityEffortReserve[2];

        config.priorityEffortLimit[Priority.High] = _priorityEffortLimit[0];
        config.priorityEffortLimit[Priority.Medium] = _priorityEffortLimit[1];
        config.priorityEffortLimit[Priority.Low] = _priorityEffortLimit[2];

        config.maxDataSizeMB = _maxDataSizeMB;

        config.priorityFeeMultipliers[Priority.High] = _priorityFeeMultipliers[0];
        config.priorityFeeMultipliers[Priority.Medium] = _priorityFeeMultipliers[1];
        config.priorityFeeMultipliers[Priority.Low] = _priorityFeeMultipliers[2];

        config.refundMultiplier = _refundMultiplier;
        config.canceledTransactionsLimit = _canceledTransactionsLimit;
        config.collectionEffortLimit = _collectionEffortLimit;
        config.collectionTransactionsLimit = _collectionTransactionsLimit;

        require(config.collectionEffortLimit > config.slotTotalEffortLimit, "Collection effort must be > slot total");

        emit ConfigUpdated();
    }

    // Main scheduling function
    function schedule(
        address handlerCap,
        bytes calldata data,
        uint256 timestamp,
        Priority priority,
        uint64 executionEffort,
        uint256 fees
    ) external nonReentrant returns (uint64, uint256) {
        EstimatedScheduledTransaction memory estimate = _estimate(
            data,
            timestamp,
            priority,
            executionEffort
        );

        if (bytes(estimate.error).length > 0 && estimate.timestamp == 0) {
            revert(estimate.error);
        }

        require(fees >= estimate.flowFee, "Insufficient fees");

        // Transfer fees from sender
        require(flowToken.transferFrom(msg.sender, address(this), fees), "Fee transfer failed");

        uint64 transactionID = _getNextIDAndIncrement();

        TransactionData memory txData = TransactionData({
            id: transactionID,
            priority: priority,
            executionEffort: executionEffort,
            status: Status.Scheduled,
            fees: fees,
            scheduledTimestamp: estimate.timestamp,
            handler: handlerCap,
            handlerTypeIdentifier: _getHandlerTypeIdentifier(handlerCap),
            handlerAddress: handlerCap,
            data: data
        });

        emit Scheduled(
            txData.id,
            uint8(txData.priority),
            txData.scheduledTimestamp,
            txData.executionEffort,
            txData.fees,
            txData.handlerAddress,
            txData.handlerTypeIdentifier
        );

        _addTransaction(estimate.timestamp, txData);

        return (transactionID, estimate.timestamp);
    }

    // Estimate function
    function estimate(
        bytes calldata data,
        uint256 timestamp,
        Priority priority,
        uint64 executionEffort
    ) external view returns (EstimatedScheduledTransaction memory) {
        return _estimate(data, timestamp, priority, executionEffort);
    }

    function _estimate(
        bytes calldata data,
        uint256 timestamp,
        Priority priority,
        uint64 executionEffort
    ) private view returns (EstimatedScheduledTransaction memory) {
        // Remove fractional values from timestamp
        uint256 sanitizedTimestamp = timestamp - (timestamp % 1);

        if (sanitizedTimestamp <= block.timestamp) {
            return EstimatedScheduledTransaction({
                flowFee: 0,
                timestamp: 0,
                error: "Invalid timestamp: is in the past"
            });
        }

        if (executionEffort > config.maximumIndividualEffort) {
            return EstimatedScheduledTransaction({
                flowFee: 0,
                timestamp: 0,
                error: "Invalid execution effort: exceeds maximum"
            });
        }

        if (executionEffort > config.priorityEffortLimit[priority]) {
            return EstimatedScheduledTransaction({
                flowFee: 0,
                timestamp: 0,
                error: "Invalid execution effort: exceeds priority limit"
            });
        }

        if (executionEffort < config.minimumExecutionEffort) {
            return EstimatedScheduledTransaction({
                flowFee: 0,
                timestamp: 0,
                error: "Invalid execution effort: below minimum"
            });
        }

        uint256 dataSizeMB = _getSizeOfData(data);
        if (dataSizeMB > config.maxDataSizeMB) {
            return EstimatedScheduledTransaction({
                flowFee: 0,
                timestamp: 0,
                error: "Invalid data size: exceeds maximum"
            });
        }

        uint256 fee = _calculateFee(executionEffort, priority, dataSizeMB);
        uint256 scheduledTimestamp = _calculateScheduledTimestamp(
            sanitizedTimestamp,
            priority,
            executionEffort
        );

        if (scheduledTimestamp == 0) {
            return EstimatedScheduledTransaction({
                flowFee: 0,
                timestamp: 0,
                error: "Invalid execution effort: exceeds available effort"
            });
        }

        if (priority == Priority.Low) {
            return EstimatedScheduledTransaction({
                flowFee: fee,
                timestamp: scheduledTimestamp,
                error: "Low Priority: will execute when space available"
            });
        }

        return EstimatedScheduledTransaction({
            flowFee: fee,
            timestamp: scheduledTimestamp,
            error: ""
        });
    }

    // Cancel function
    function cancel(uint64 id) external onlyRole(CANCEL_ROLE) returns (uint256) {
        TransactionData storage tx = transactions[id];
        require(tx.id == id, "Transaction not found");
        require(tx.status == Status.Scheduled, "Transaction must be scheduled");

        // Update slot effort
        slotUsedEffort[tx.scheduledTimestamp][tx.priority] =
            _saturatingSubtract(slotUsedEffort[tx.scheduledTimestamp][tx.priority], tx.executionEffort);

        uint256 totalFees = tx.fees;
        uint256 refundedFees = _payAndRefundFees(tx.id, config.refundMultiplier);

        // Add to canceled transactions
        _addToCanceledTransactions(id);

        emit Canceled(
            tx.id,
            uint8(tx.priority),
            refundedFees,
            totalFees - refundedFees,
            tx.handlerAddress,
            tx.handlerTypeIdentifier
        );

        _removeTransaction(tx.id);

        return refundedFees;
    }

    // Process function (called by FVM equivalent)
    function process() external onlyRole(PROCESS_ROLE) {
        uint256 currentTimestamp = block.timestamp;

        if (!_hasBefore(currentTimestamp)) {
            return;
        }

        _removeExecutedTransactions(currentTimestamp);

        uint64[] memory pendingIds = _pendingQueue();

        if (pendingIds.length == 0) {
            return;
        }

        for (uint256 i = 0; i < pendingIds.length; i++) {
            TransactionData storage tx = transactions[pendingIds[i]];

            if (_isHandlerValid(tx.handler)) {
                emit PendingExecution(
                    tx.id,
                    uint8(tx.priority),
                    tx.executionEffort,
                    tx.fees,
                    tx.handlerAddress,
                    tx.handlerTypeIdentifier
                );
            }

            tx.status = Status.Executed;
        }
    }

    // Execute transaction function
    function executeTransaction(uint64 id) external onlyRole(EXECUTE_ROLE) {
        TransactionData storage tx = transactions[id];
        require(tx.id == id, "Transaction not found");
        require(tx.status == Status.Executed, "Invalid status for execution");

        emit Executed(
            tx.id,
            uint8(tx.priority),
            tx.executionEffort,
            tx.handlerAddress,
            tx.handlerTypeIdentifier
        );

        ITransactionHandler(tx.handler).executeTransaction(id, tx.data);
    }

    // View functions
    function getStatus(uint64 id) external view returns (Status) {
        if (id == 0 || id >= nextID) {
            return Status.Unknown;
        }

        if (transactions[id].id == id) {
            return transactions[id].status;
        }

        for (uint256 i = 0; i < canceledTransactions.length; i++) {
            if (canceledTransactions[i] == id) {
                return Status.Canceled;
            }
        }

        if (id > canceledTransactions[0]) {
            return Status.Executed;
        }

        return Status.Unknown;
    }

    function getTransactionData(uint64 id) external view returns (TransactionData memory) {
        return transactions[id];
    }

    function getCanceledTransactions() external view returns (uint64[] memory) {
        return canceledTransactions;
    }

    function getSlotAvailableEffort(uint256 timestamp, Priority priority) external view returns (uint64) {
        return _getSlotAvailableEffort(timestamp, priority);
    }

    function getTransactionsForTimeframe(
        uint256 startTimestamp,
        uint256 endTimestamp
    ) external view returns (uint64[] memory) {
        require(startTimestamp <= endTimestamp, "Invalid timeframe");

        uint64[] memory result = new uint64[](100); // Temporary fixed size
        uint256 count = 0;

        uint256[] memory timestamps = _getBefore(endTimestamp);

        for (uint256 i = 0; i < timestamps.length; i++) {
            if (timestamps[i] < startTimestamp) continue;

            for (uint8 p = 0; p < 3; p++) {
                Priority priority = Priority(p);
                mapping(uint64 => uint64) storage txMap = slotQueue[timestamps[i]][priority];
                // Note: This would need iteration support in Solidity
            }
        }

        // Resize and return
        uint64[] memory finalResult = new uint64[](count);
        for (uint256 i = 0; i < count; i++) {
            finalResult[i] = result[i];
        }

        return finalResult;
    }

    // Internal helper functions
    function _getNextIDAndIncrement() private returns (uint64) {
        uint64 id = nextID;
        nextID++;
        return id;
    }

    function _calculateFee(
        uint64 executionEffort,
        Priority priority,
        uint256 dataSizeMB
    ) private view returns (uint256) {
        // Simplified fee calculation
        uint256 baseFee = uint256(executionEffort) * 1e15; // Base calculation
        uint256 scaledExecutionFee = (baseFee * config.priorityFeeMultipliers[priority]) / 1e18;
        uint256 storageFee = dataSizeMB * 1e15; // Storage fee calculation

        return scaledExecutionFee + storageFee;
    }

    function _calculateScheduledTimestamp(
        uint256 timestamp,
        Priority priority,
        uint64 executionEffort
    ) private view returns (uint256) {
        uint64 available = _getSlotAvailableEffort(timestamp, priority);

        if (executionEffort <= available) {
            return timestamp;
        }

        if (priority == Priority.High) {
            return 0; // Cannot schedule
        }

        // Try next timestamp recursively (would need loop in production)
        return _calculateScheduledTimestamp(timestamp + 1, priority, executionEffort);
    }

    function _getSlotAvailableEffort(uint256 timestamp, Priority priority) private view returns (uint64) {
        uint64 priorityLimit = config.priorityEffortLimit[priority];

        mapping(Priority => uint64) storage slotEfforts = slotUsedEffort[timestamp];

        if (slotEfforts[Priority.High] == 0 &&
            slotEfforts[Priority.Medium] == 0 &&
            slotEfforts[Priority.Low] == 0) {
            return priorityLimit;
        }

        uint64 highUsed = slotEfforts[Priority.High];
        uint64 mediumUsed = slotEfforts[Priority.Medium];

        if (priority == Priority.Low) {
            uint64 highPlusMediumUsed = highUsed + mediumUsed;
            uint64 totalEffortRemaining = _saturatingSubtract(config.slotTotalEffortLimit, highPlusMediumUsed);
            uint64 lowEffortRemaining = totalEffortRemaining < priorityLimit ? totalEffortRemaining : priorityLimit;
            uint64 lowUsed = slotEfforts[Priority.Low];
            return _saturatingSubtract(lowEffortRemaining, lowUsed);
        }

        uint64 highReserve = config.priorityEffortReserve[Priority.High];
        uint64 mediumReserve = config.priorityEffortReserve[Priority.Medium];

        uint64 highSharedUsed = _saturatingSubtract(highUsed, highReserve);
        uint64 mediumSharedUsed = _saturatingSubtract(mediumUsed, mediumReserve);

        uint64 totalShared = _saturatingSubtract(_saturatingSubtract(config.slotTotalEffortLimit, highReserve), mediumReserve);
        uint64 sharedAvailable = _saturatingSubtract(totalShared, highSharedUsed + mediumSharedUsed);

        uint64 reserve = config.priorityEffortReserve[priority];
        uint64 used = slotEfforts[priority];
        uint64 unusedReserve = _saturatingSubtract(reserve, used);

        return sharedAvailable + unusedReserve;
    }

    function _addTransaction(uint256 slot, TransactionData memory txData) private {
        // Initialize slot if needed
        if (slotUsedEffort[slot][Priority.High] == 0 &&
            slotUsedEffort[slot][Priority.Medium] == 0 &&
            slotUsedEffort[slot][Priority.Low] == 0) {
            _addTimestamp(slot);
        }

        // Add transaction to queue
        slotQueue[slot][txData.priority][txData.id] = txData.executionEffort;

        // Update used effort
        slotUsedEffort[slot][txData.priority] += txData.executionEffort;

        // Calculate total effort
        uint64 newTotalEffort = slotUsedEffort[slot][Priority.High] +
                                slotUsedEffort[slot][Priority.Medium] +
                                slotUsedEffort[slot][Priority.Low];

        // Handle low priority rescheduling if needed
        if (newTotalEffort > config.slotTotalEffortLimit) {
            _rescheduleLowPriorityTransactions(slot);
        }

        // Store transaction
        transactions[txData.id] = txData;
    }

    function _removeTransaction(uint64 txId) private {
        TransactionData memory tx = transactions[txId];
        delete transactions[txId];
        delete slotQueue[tx.scheduledTimestamp][tx.priority][txId];

        // Clean up if slot is empty
        bool slotEmpty = true;
        for (uint8 p = 0; p < 3; p++) {
            // Check if priority queue is empty
            // Note: This would need proper implementation in Solidity
        }

        if (slotEmpty) {
            delete slotUsedEffort[tx.scheduledTimestamp];
            _removeTimestamp(tx.scheduledTimestamp);
        }
    }

    function _rescheduleLowPriorityTransactions(uint256 slot) private {
        // Implementation for rescheduling low priority transactions
        // This would need proper iteration support in Solidity
    }

    function _removeExecutedTransactions(uint256 currentTimestamp) private {
        uint256[] memory pastTimestamps = _getBefore(currentTimestamp);

        for (uint256 i = 0; i < pastTimestamps.length; i++) {
            // Iterate through priorities and remove executed transactions
            for (uint8 p = 0; p < 3; p++) {
                // Implementation needed with proper iteration
            }
        }
    }

    function _pendingQueue() private view returns (uint64[] memory) {
        uint256 currentTimestamp = block.timestamp;
        uint64[] memory pending = new uint64[](100); // Temporary fixed size
        uint256 count = 0;

        uint64 collectionAvailableEffort = config.collectionEffortLimit;
        int256 transactionsAvailableCount = config.collectionTransactionsLimit;

        uint256[] memory pastTimestamps = _getBefore(currentTimestamp);

        for (uint256 i = 0; i < pastTimestamps.length; i++) {
            // Process transactions by priority
            // Implementation needed with proper iteration support
        }

        // Resize and return
        uint64[] memory result = new uint64[](count);
        for (uint256 i = 0; i < count; i++) {
            result[i] = pending[i];
        }

        return result;
    }

    function _payAndRefundFees(uint64 txId, uint256 refundMultiplier) private returns (uint256) {
        TransactionData storage tx = transactions[txId];
        uint256 totalFees = tx.fees;

        if (refundMultiplier == 0) {
            // Pay all fees to vault
            require(flowToken.transfer(feeVault, totalFees), "Fee payment failed");
            return 0;
        } else {
            uint256 amountToReturn = (totalFees * refundMultiplier) / 1e18;
            uint256 amountToKeep = totalFees - amountToReturn;

            // Return fees to sender
            require(flowToken.transfer(msg.sender, amountToReturn), "Refund failed");
            // Keep rest as fees
            require(flowToken.transfer(feeVault, amountToKeep), "Fee payment failed");

            return amountToReturn;
        }
    }

    function _addToCanceledTransactions(uint64 id) private {
        // Insert maintaining sorted order
        uint256 insertIndex = canceledTransactions.length;
        for (uint256 i = 0; i < canceledTransactions.length; i++) {
            if (id < canceledTransactions[i]) {
                insertIndex = i;
                break;
            }
        }

        // Insert at position
        canceledTransactions.push(0);
        for (uint256 i = canceledTransactions.length - 1; i > insertIndex; i--) {
            canceledTransactions[i] = canceledTransactions[i - 1];
        }
        canceledTransactions[insertIndex] = id;

        // Keep under limit
        if (canceledTransactions.length > config.canceledTransactionsLimit) {
            // Remove first element
            for (uint256 i = 0; i < canceledTransactions.length - 1; i++) {
                canceledTransactions[i] = canceledTransactions[i + 1];
            }
            canceledTransactions.pop();
        }
    }

    // Sorted timestamps helper functions
    function _addTimestamp(uint256 timestamp) private {
        uint256 insertIndex = sortedTimestamps.timestamps.length;
        for (uint256 i = 0; i < sortedTimestamps.timestamps.length; i++) {
            if (timestamp < sortedTimestamps.timestamps[i]) {
                insertIndex = i;
                break;
            }
        }

        sortedTimestamps.timestamps.push(0);
        for (uint256 i = sortedTimestamps.timestamps.length - 1; i > insertIndex; i--) {
            sortedTimestamps.timestamps[i] = sortedTimestamps.timestamps[i - 1];
        }
        sortedTimestamps.timestamps[insertIndex] = timestamp;
    }

    function _removeTimestamp(uint256 timestamp) private {
        for (uint256 i = 0; i < sortedTimestamps.timestamps.length; i++) {
            if (sortedTimestamps.timestamps[i] == timestamp) {
                for (uint256 j = i; j < sortedTimestamps.timestamps.length - 1; j++) {
                    sortedTimestamps.timestamps[j] = sortedTimestamps.timestamps[j + 1];
                }
                sortedTimestamps.timestamps.pop();
                break;
            }
        }
    }

    function _getBefore(uint256 current) private view returns (uint256[] memory) {
        uint256 count = 0;
        for (uint256 i = 0; i < sortedTimestamps.timestamps.length; i++) {
            if (sortedTimestamps.timestamps[i] <= current) {
                count++;
            } else {
                break;
            }
        }

        uint256[] memory result = new uint256[](count);
        for (uint256 i = 0; i < count; i++) {
            result[i] = sortedTimestamps.timestamps[i];
        }

        return result;
    }

    function _hasBefore(uint256 current) private view returns (bool) {
        return sortedTimestamps.timestamps.length > 0 &&
               sortedTimestamps.timestamps[0] <= current;
    }

    function _getSizeOfData(bytes calldata data) private pure returns (uint256) {
        if (data.length == 0) {
            return 0;
        }
        // Convert bytes to MB (simplified)
        return (data.length * 1e18) / (1024 * 1024);
    }

    function _getHandlerTypeIdentifier(address handler) private pure returns (string memory) {
        // Return a string identifier for the handler type
        return string(abi.encodePacked("Handler:", _toHexString(handler)));
    }

    function _isHandlerValid(address handler) private view returns (bool) {
        // Check if handler contract exists and implements interface
        uint256 size;
        assembly {
            size := extcodesize(handler)
        }
        return size > 0;
    }

    function _saturatingSubtract(uint64 a, uint64 b) private pure returns (uint64) {
        if (b > a) {
            return 0;
        }
        return a - b;
    }

    function _toHexString(address addr) private pure returns (string memory) {
        bytes32 value = bytes32(uint256(uint160(addr)));
        bytes memory alphabet = "0123456789abcdef";
        bytes memory str = new bytes(42);
        str[0] = '0';
        str[1] = 'x';
        for (uint256 i = 0; i < 20; i++) {
            str[2+i*2] = alphabet[uint8(value[i + 12] >> 4)];
            str[3+i*2] = alphabet[uint8(value[i + 12] & 0x0f)];
        }
        return string(str);
    }
}