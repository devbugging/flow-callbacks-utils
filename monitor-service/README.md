# Flow Transaction Scheduler Monitor

A monitoring service that tracks Flow Transaction Scheduler events and displays them in a web interface.

## Directory Structure

This monitor service is located in the `monitor-service/` directory to keep it separate from test files and other project components.

```
monitor-service/
├── monitor.go              # Main monitoring service
├── cmd/
│   └── callback-fetcher/   # Callback fetcher tool
│       ├── main.go         # Callback fetcher source
│       └── README.md       # Callback fetcher documentation
├── db.json                 # Local event database
├── flow.json               # Flow configuration
├── index.html              # Web interface
└── README.md               # This file
```

## Features

- **Real-time monitoring** of Flow Transaction Scheduler events:
  - `Scheduled` - When transactions are scheduled
  - `PendingExecution` - When scheduled transactions are ready for execution
  - `Executed` - When transactions are executed
  - `Canceled` - When transactions are canceled
  - `CollectionLimitReached` - When collection limits are reached
  - `ConfigUpdated` - When configuration is updated

- **Persistent storage** in local JSON database (`db.json`)
- **Resume from last block** when restarted
- **Automatic pagination** - Queries events in chunks of 1000 blocks to respect Flow CLI limits
- **Web interface** with:
  - Event statistics dashboard
  - Sortable table with all event fields (type, transaction ID, priority, timestamps, data, fees, owner, handler)
  - Search by Transaction ID, data, owner, or handler
  - Filter by event type
  - Color-coded event types
  - Auto-refresh every 10 seconds

- **Callback fetcher tool** for detailed transaction analysis by ID and block ID

## Usage

### Basic Usage

```bash
# Navigate to monitor-service directory first
cd monitor-service

# Monitor localnet (service account is required)
go run monitor.go -account 0xe467b9dd11fa00df

# Monitor specific network
go run monitor.go -account 0xe467b9dd11fa00df -network testnet

# Monitor from specific block height (when database is empty)
go run monitor.go -account 0xe467b9dd11fa00df -network testnet -start 12345

# Monitor with custom settings
go run monitor.go -account 0xe467b9dd11fa00df -network mainnet -port 8080 -interval 5s -start 100000
```

### CLI Options

- `-network` - Flow network to connect to (localnet, testnet, mainnet, emulator) [default: localnet]
- `-account` - Service account address **[required]**
- `-db` - Path to local database file [default: db.json]
- `-port` - Web server port [default: 8080]
- `-interval` - Polling interval for new events [default: 10s]
- `-start` - Starting block height to monitor from (only used when database is empty) [default: 0]

### Web Interface

Once running, open your browser to:
```
http://localhost:8080
```

The interface provides:
- **Statistics Cards** - Count of each event type
- **Search Box** - Search by transaction ID or event data
- **Type Filter** - Filter events by type
- **Sortable Table** - Click column headers to sort
- **Auto-refresh** - Toggle automatic refresh

### Database

The monitor stores events in a local JSON file (`db.json` by default) with the structure:
```json
{
  "last_block": 12345,
  "events": [
    {
      "id": "scheduled-123-12345",
      "type": "scheduled",
      "tx_id": 123,
      "priority": 1,
      "timestamp": 1640995200.0,
      "block_height": 12345,
      "block_time": 1640995200.0,
      "data": "test-data",
      "fees": 0.1,
      "owner": "0xf8d6e0586b0a20c7",
      "handler": "A.f8d6e0586b0a20c7.TestHandler"
    }
  ]
}
```

### Callback Fetcher Tool

The monitor service includes a separate tool for fetching detailed callback execution information:

```bash
# Build the callback fetcher
go build -o callback-fetcher ./cmd/callback-fetcher

# Fetch transaction details by transaction ID
./callback-fetcher -tx 5a7c8b9d2e3f4a6b -network localnet

# Fetch system transaction with block ID
./callback-fetcher -tx 5a7c8b9d2e3f4a6b -block 1a2b3c4d5e6f7890 -network mainnet -format json
```

See `cmd/callback-fetcher/README.md` for detailed documentation.

## Requirements

- Go 1.19+
- Flow CLI installed and configured
- Access to Flow network (local/test/main)

## Event Types

The monitor tracks these event types with color coding:

- 🟢 **Scheduled** (Green) - Transaction has been scheduled
- 🟡 **Pending** (Yellow) - Transaction is ready for execution
- 🔵 **Executed** (Blue) - Transaction has been executed
- 🔴 **Canceled** (Red) - Transaction has been canceled

## Resuming

When the monitor restarts, it automatically resumes from the last processed block stored in the database, ensuring no events are missed.

## Starting from Specific Block Height

The `-start` parameter allows you to specify a starting block height **only when the database file doesn't exist**.

**When `-start` is used:**
- ✅ Database file (`db.json`) doesn't exist - uses the specified start height
- ❌ Database file exists (even if empty with `last_block: 0`) - ignores `-start` and uses database value

**Examples:**
```bash
# Case 1: No db.json file exists - will start from block 100000
go run monitor.go -network testnet -start 100000

# Case 2: db.json exists with last_block: 5000 - will resume from block 5001
go run monitor.go -network testnet -start 100000  # start parameter ignored

# Case 3: db.json exists but last_block: 0 - will start from current block
go run monitor.go -network testnet -start 100000  # start parameter ignored
```

This ensures that existing monitoring sessions are never disrupted by accidentally providing a `-start` parameter.



# Stop the service
sudo systemctl stop flow-monitor

# Reload systemd configuration
sudo systemctl daemon-reload

# Start the service
sudo systemctl start flow-monitor

# Check status
sudo systemctl status flow-monitor

# View logs
sudo journalctl -u flow-monitor -f

# Location 
`/home/monitor-user/apps/flow-callbacks-utils/monitor-service/monitor`