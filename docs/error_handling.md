# Error Handling & Resilience

AxiaOps implements comprehensive error handling to ensure reliable scan operations even when AWS APIs are unreliable or misconfigured.

## Overview

The error handling system provides:
- **Retry logic** with exponential backoff for transient errors
- **Partial scan recovery** to continue when individual components fail
- **Error categorization** for intelligent handling decisions
- **Circuit breaker pattern** to prevent cascading failures
- **Scan timeouts** to prevent hung operations
- **UI feedback** with detailed status indicators

## Error Categories

Errors are automatically categorized for appropriate handling:

| Category | Examples | Retryable | Fails Scan |
|----------|----------|-----------|-------------|
| `credentials` | InvalidAccessKeyId, ExpiredToken | ❌ | ✅ |
| `permissions` | AccessDenied, UnauthorizedOperation | ❌ | ✅ |
| `throttling` | RequestLimitExceeded, Throttling | ✅ | ❌ |
| `network` | Connection timeout, DNS errors | ✅ | ❌ |
| `data_unavailable` | Cost Explorer not enabled | ❌ | ✅ |
| `internal` | AWS ServiceUnavailable | ✅ | ❌ |

## Retry Configuration

```go
// Default retry settings
MaxAttempts: 3                    // 3 attempts per API call
BaseDelay:   100 * time.Millisecond  // Starting delay
MaxDelay:    5 * time.Second      // Maximum delay between retries
```

**Exponential backoff**: 100ms → 200ms → 400ms → fail

## Circuit Breaker

Protects against repeated failures by temporarily blocking scan attempts:

```go
// Default circuit breaker settings
MaxFailures:      3               // Failures before opening
ResetTimeout:     30 * time.Second // Wait time before retry
SuccessThreshold: 2               // Successes needed to close
```

### States

1. **Closed (Normal)**: All scans allowed
2. **Open (Protected)**: No scans allowed for 30 seconds after 3 failures
3. **Half-open (Testing)**: Allows 2 test scans to check if issue resolved

## Scan Timeout

- **Default timeout**: 10 minutes per scan
- **Context cancellation**: Graceful shutdown on timeout or cancellation
- **Status tracking**: Timeout errors are logged and displayed in UI

## Partial Recovery

Scans continue even when individual components fail:

- **Cost fetching fails**: Skip that provider, continue with others
- **Usage fetching fails**: Continue with cost-only ghost detection
- **EIP discovery fails**: Continue without EIP data
- **Individual resource fails**: Skip that resource, process others

## UI Status Indicators

Account status is displayed with color-coded dots:

| Status | Color | Description |
|--------|-------|-------------|
| `connected` | 🟢 Green | Normal operation |
| `error` | 🔴 Red | General scan error |
| `scan_timeout` | 🟠 Orange | Scan exceeded 10-minute timeout |
| `circuit_breaker_open` | 🟣 Purple | Circuit breaker protecting system |
| Pending | 🟡 Yellow | Never scanned, waiting for first scan |

### Circuit Breaker UI Behavior

When circuit breaker is open:
- Purple status dot displayed
- "Circuit breaker open" text shown
- Scan button disabled and grayed out
- Automatic re-enable after 30-second timeout

## Error Logging

All errors include structured logging with:

```json
{
  "level": "error",
  "msg": "scan failed",
  "account_id": "uuid",
  "error": "detailed error message",
  "category": "throttling",
  "timeout": false,
  "circuit_breaker_state": "open"
}
```

## Best Practices

### For Operators

1. **Monitor circuit breaker events** - indicates systemic issues
2. **Check timeout patterns** - may indicate resource discovery issues
3. **Review credential errors** - require manual intervention
4. **Watch throttling patterns** - may need rate limiting adjustments

### For Users

1. **Purple status**: Wait 30 seconds before retrying
2. **Orange status**: Check AWS account for large resource counts
3. **Red status**: Verify credentials and permissions
4. **Pending status**: Normal for new accounts

## Implementation Details

### Packages

- `services/shared/retry/` - Exponential backoff retry logic
- `services/shared/errors/` - Error categorization and classification
- `services/shared/circuitbreaker/` - Circuit breaker implementation

### Integration Points

- **Worker**: Circuit breaker protection around scan execution
- **AWS Provider**: Retry logic for Cost Explorer and CloudWatch calls
- **Main Scan**: Partial recovery and context cancellation
- **Dashboard**: Status display and scan button management

## Troubleshooting

### Common Issues

**Circuit breaker frequently opening**:
- Check AWS credentials validity
- Verify IAM permissions
- Monitor AWS service health

**Frequent timeouts**:
- Large AWS accounts may need longer timeouts
- Check network connectivity to AWS
- Review CloudWatch API limits

**Partial scan results**:
- Normal behavior - system prioritizes availability
- Check logs for specific component failures
- Verify individual service permissions

### Configuration

Error handling is configured via:
- Environment variables for timeouts
- Code constants for retry/circuit breaker settings
- AWS IAM policies for permissions