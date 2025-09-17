# Flow Transaction Scheduler Project

This project contains Flow Transaction Scheduler smart contracts and related tools.

## Components

- **Smart Contracts**: 
  - `FlowTransactionScheduler.cdc` - Main scheduler contract
  - `TestFlowCallbackHandler.cdc` - Test callback handler
  - `schedule.cdc`, `cancel.cdc` - Transaction scripts

- **Tests**: 
  - `callback_test.go` - Test suite for scheduler functionality
  - `test_*.go` - Additional test utilities

- **Monitor Service**: 
  - `monitor-service/` - Real-time monitoring service for scheduler events
  - See `monitor-service/README.md` for detailed documentation

## Quick Start

### Running Tests
```bash
go test -v
```

### Running Monitor Service
```bash
cd monitor-service
go run monitor.go -network localnet
```

Visit `http://localhost:8080` to view the monitoring dashboard.

## Configuration

Flow configuration is stored in `flow.json` in the root directory.
