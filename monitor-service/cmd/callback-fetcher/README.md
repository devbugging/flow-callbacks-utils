# Flow Callback Fetcher

A command-line tool to fetch system transaction callback execution information by transaction ID and block ID from the Flow blockchain using the direct Flow protobuf gRPC Access API client.

## Features

- Fetch system transaction results by transaction ID and block ID
- Uses GetSystemTransaction and GetSystemTransactionResult from Flow protobuf API
- Direct protobuf gRPC client connection (no SDK wrapper)
- Multiple output formats (table, JSON)
- Support for all Flow networks (localnet, testnet, mainnet, emulator)
- Detailed event parsing and display
- Strict validation - no fallback behavior

## Building

```bash
# From the monitor-service directory
go build -o callback-fetcher ./cmd/callback-fetcher
```

## Usage

### Basic Usage

Fetch system transaction callback information by transaction ID and block ID:

```bash
./callback-fetcher -tx <transaction_id> -block <block_id> -network <network>
```

**Note: Both transaction ID and block ID are required.** This tool is specifically designed for system transactions that require precise block context.

### Output Formats

#### Table Format (Default)

```bash
./callback-fetcher -tx abc123 -format table
```

Output:
```
Transaction ID: abc123
Block Height: 12345
Status: SEALED

Events (2):
  1. Type: A.123.FlowTransactionScheduler.Executed
     Values:
       id: 789
       success: true
```

#### JSON Format

```bash
./callback-fetcher -tx abc123 -format json
```

Output:
```json
{
  "transaction_id": "abc123",
  "block_height": 12345,
  "status": "SEALED",
  "events": [
    {
      "type": "A.123.FlowTransactionScheduler.Executed",
      "values": {
        "id": 789,
        "success": true
      }
    }
  ]
}
```

## Command Line Options

| Flag | Description | Default | Required |
|------|-------------|---------|----------|
| `-tx` | Transaction ID to fetch callback for | - | Yes |
| `-block` | Block ID to fetch transaction from | - | Yes |
| `-network` | Flow network (localnet, testnet, mainnet, emulator) | localnet | No |
| `-format` | Output format (table, json) | table | No |

## Examples

### Example 1: Fetch from Localnet

```bash
./callback-fetcher -tx 5a7c8b9d2e3f4a6b -block 1a2b3c4d5e6f7890 -network localnet
```

### Example 2: Fetch from Mainnet with JSON Output

```bash
./callback-fetcher -tx 5a7c8b9d2e3f4a6b -block 1a2b3c4d5e6f7890 -network mainnet -format json
```

### Example 3: Fetch from Testnet

```bash
./callback-fetcher -tx 5a7c8b9d2e3f4a6b -block 1a2b3c4d5e6f7890 -network testnet
```

## Transaction Status Values

The tool reports the following transaction status values:

- `SEALED`: Transaction has been sealed in a block
- `EXECUTED`: Transaction has been executed but not yet sealed
- `PENDING`: Transaction is still pending execution
- `FAILED`: Transaction execution failed
- `UNKNOWN`: Transaction status could not be determined

## Integration with Monitor Service

This tool is designed to work alongside the Flow Transaction Scheduler Monitor service. You can use transaction IDs discovered by the monitor to fetch detailed callback execution information:

1. Use the monitor service to discover executed transactions
2. Use this tool to get detailed callback information for specific transactions
3. Optionally use block IDs from the monitor data for precise transaction lookup

## Flow Protobuf gRPC Access API

This tool connects directly to the Flow Access API using the raw protobuf gRPC client from `github.com/onflow/flow/protobuf/go/flow/access`. It does not use the Flow Go SDK wrapper or require the Flow CLI, making it the most direct way to interact with Flow Access nodes.

The tool uses the GetSystemTransaction and GetSystemTransactionResult methods from the Flow protobuf-generated gRPC client as referenced in the Flow repository at:
- https://github.com/onflow/flow/blob/master/protobuf/go/flow/access/access_grpc.pb.go#L256
- https://github.com/onflow/flow/blob/master/protobuf/go/flow/access/access_grpc.pb.go#L404C1-L405C1

## Error Handling

- If a transaction ID is not found at the specified block, the tool will fail with an error
- If the system transaction lookup fails, the tool will fail immediately (no fallback behavior)
- If the transaction ID at index 0 in the block doesn't match the provided transaction ID, the tool will fail
- Network connectivity issues to Flow Access nodes will be reported with appropriate error messages
- Invalid transaction ID or block ID formats will be validated before making API calls
- Missing required parameters (transaction ID or block ID) will cause the tool to exit with an error
