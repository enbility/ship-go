# Error Handling Cheat Sheet

Quick reference for troubleshooting common SHIP protocol errors in ship-go. For detailed protocol information, see [SHIP TS 1.0.1](https://www.eebus.org/media-downloads/).

## Connection Errors

### "connection refused" / "dial tcp :4712: connect: connection refused"

**Cause**: Cannot establish TCP connection to device
- Port 4712 blocked or service not running
- Network connectivity issues
- Device not on same network segment

**Solutions**:
```bash
# Check if port is open
nmap -p 4712 <device_ip>

# Test network connectivity
ping <device_ip>

# Check firewall (Linux)
sudo ufw allow 4712
```

**Code pattern**:
```go
if err := hub.Start(); err != nil {
    if strings.Contains(err.Error(), "connection refused") {
        log.Fatal("Port 4712 blocked or device unreachable")
    }
}
```

---

### "handshake timeout" / Hello phase timeout

**Cause**: Hello phase exceeded 60 seconds (SHIP Section 13.4.4.1.4.3)
- Certificate validation issues
- Trust establishment taking too long
- Network latency problems

**Solutions**:
1. Check certificate validity
2. Verify `AllowWaitingForTrust()` responds quickly
3. Enable debug logging

**Code pattern**:
```go
func (h *MyHubReader) AllowWaitingForTrust(ski string) bool {
    // Must respond within 60 seconds total
    select {
    case result := <-h.promptUser(ski):
        return result
    case <-time.After(30 * time.Second):
        return false // Timeout safety
    }
}
```

---

### "double connection detected"

**Cause**: Two simultaneous connections to same device (SHIP Section 12.2.2)
- Normal behavior during reconnection
- ship-go uses "higher SKI wins" logic (deviates from spec)

**Solutions**: 
- This is expected behavior, not an error
- One connection will be automatically closed
- Monitor which connection is kept

**Code pattern**:
```go
func (h *MyHubReader) RemoteSKIDisconnected(ski string) {
    // Connection may reconnect automatically
    log.Printf("Device %s disconnected (may be due to double connection)", ski)
}
```

---

### "connection dropped unexpectedly"

**Cause**: WebSocket connection lost
- Network issues
- Device shutdown
- Connection limit exceeded

**Solutions**:
1. Implement reconnection logic
2. Check connection limits
3. Monitor network stability

**Code pattern**:
```go
func (h *MyHubReader) RemoteSKIDisconnected(ski string) {
    log.Printf("Device %s disconnected", ski)
    
    // Schedule reconnection
    go func() {
        time.Sleep(5 * time.Second)
        hub.ConnectSKI(ski, true)
    }()
}
```

---

## Handshake Phase Errors

### CMI (Connection Mode Init) Phase

| Error | Cause | Solution |
|-------|-------|----------|
| "invalid CMI message" | Protocol version mismatch | Update ship-go or check device compatibility |
| "CMI timeout" | No response within 10 seconds | Check network connectivity |
| "unsupported mode" | Connection mode not supported | Verify SHIP implementation compatibility |

**Example**:
```go
// Monitor CMI errors in logs
// ship-go handles CMI automatically
```

---

### Hello Phase

| Error | Cause | Solution |
|-------|-------|----------|
| "trust denied" | User rejected pairing | Check `AllowWaitingForTrust()` implementation |
| "hello timeout" | Exceeded 60-second limit | Ensure quick trust decisions |
| "invalid certificate" | Certificate parsing failed | Verify certificate generation |

**Example**:
```go
func (h *MyHubReader) AllowWaitingForTrust(ski string) bool {
    // Log the decision for debugging
    accepted := h.userPrompt(ski)
    log.Printf("Trust decision for %s: %v", ski, accepted)
    return accepted
}
```

---

### Protocol Phase

| Error | Cause | Solution |
|-------|-------|----------|
| "version mismatch" | Incompatible SHIP versions | Update ship-go |
| "protocol not supported" | Feature not implemented | Check required vs optional features |

---

### PIN Phase

| Error | Cause | Solution |
|-------|-------|----------|
| "pin required" | Device requires PIN verification | ship-go only supports "none" PIN state |
| "pin verification failed" | PIN mismatch | Not applicable (ship-go doesn't support PINs) |

**Note**: ship-go only implements PIN state "none" (SHIP Section 13.4.4.3.5.1)

---

### Access Phase

| Error | Cause | Solution |
|-------|-------|----------|
| "access denied" | Final authorization failed | Check access method implementation |
| "access timeout" | Phase took too long | Monitor access method performance |

---

## Certificate Errors

### "invalid SKI"

**Cause**: Subject Key Identifier issues
- SKI not 20 bytes (160 bits)
- Missing or malformed SKI

**Solutions**:
```go
// Verify certificate generation
cert, err := cert.CreateCertificate("Unit", "Org", "DE", "Device")
if err != nil {
    log.Fatal("Certificate creation failed:", err)
}

// Validate SKI
x509Cert, _ := x509.ParseCertificate(cert.Certificate[0])
ski, err := cert.SkiFromCertificate(x509Cert)
if err != nil {
    log.Fatal("Invalid SKI:", err)
}
```

---

### "certificate expired"

**Cause**: Certificate past validity period
- ship-go generates 10-year certificates
- System clock issues

**Solutions**:
1. Check system time
2. Regenerate certificate
3. Verify certificate validity period

```go
// Check certificate expiration
x509Cert, _ := x509.ParseCertificate(cert.Certificate[0])
if time.Now().After(x509Cert.NotAfter) {
    log.Fatal("Certificate expired, regenerate required")
}
```

---

## mDNS Discovery Errors

### "no devices discovered"

**Cause**: mDNS/multicast DNS issues
- Network doesn't support multicast
- Avahi daemon not running (Linux)
- Wrong network interface

**Solutions**:
```bash
# Linux: Check Avahi
sudo systemctl status avahi-daemon
sudo systemctl start avahi-daemon

# Test mDNS manually
avahi-browse -a

# Check multicast routing
ip route show | grep 224
```

**Code pattern**:
```go
// Use Zeroconf fallback if Avahi fails
mdns := mdns.NewMDNS(
    ski, brand, model, deviceType, serial,
    categories, shipID, serviceName, port,
    interfaces,
    mdns.MdnsProviderSelectionGoZeroConfOnly, // Force Zeroconf
)
```

---

### "mDNS registration failed"

**Cause**: Service announcement problems
- Port already in use
- Invalid service parameters
- Network interface issues

**Solutions**:
1. Check port availability
2. Verify service parameters
3. Test with different interfaces

```go
// Check for port conflicts
func checkPortAvailable(port int) error {
    ln, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
    if err != nil {
        return fmt.Errorf("port %d not available: %w", port, err)
    }
    ln.Close()
    return nil
}
```

---

## Resource Errors

### "too many connections"

**Cause**: Connection limit exceeded
- Default limit: 10 connections
- Resource exhaustion protection

**Solutions**:
```go
// Adjust connection limit
hub.SetMaxConnections(20) // Increase limit

// Monitor connection count
func (h *MyHubReader) RemoteSKIConnected(ski string) {
    connectionCount := h.getConnectionCount()
    if connectionCount > 15 {
        log.Warning("High connection count:", connectionCount)
    }
}
```

---

### "out of memory" / Resource exhaustion

**Cause**: Too many concurrent operations
- Memory leaks
- Unbounded goroutines
- Large message buffers

**Solutions**:
1. Monitor resource usage
2. Implement connection limits
3. Add graceful degradation

```go
// Monitor goroutines
ticker := time.NewTicker(30 * time.Second)
go func() {
    for range ticker.C {
        count := runtime.NumGoroutine()
        if count > 100 {
            log.Warning("High goroutine count:", count)
        }
    }
}()
```

---

## Debugging Techniques

### Enable Debug Logging

```bash
# Environment variable (if logging supports it)
export SHIP_LOG=debug

# Or programmatically
logging.SetLogging(logging.Debug)
```

### Connection State Monitoring

```go
func (h *MyHubReader) ServiceConnectionStateChanged(ski string, state api.ConnectionState) {
    log.Printf("Device %s state changed to: %v", ski, state)
    
    switch state {
    case api.ConnectionStateQueued:
        log.Debug("Connection queued")
    case api.ConnectionStateInitiated:
        log.Debug("Connection initiated")
    case api.ConnectionStateInProgress:
        log.Debug("Handshake in progress")
    case api.ConnectionStateCompleted:
        log.Info("Connection established successfully")
    case api.ConnectionStateError:
        log.Error("Connection failed")
    }
}
```

### Network Debugging

```bash
# Monitor SHIP traffic
sudo tcpdump -i any port 4712

# Check mDNS traffic
sudo tcpdump -i any port 5353

# Monitor WebSocket traffic
netstat -tulpn | grep 4712
```

---

## Error Recovery Patterns

### Exponential Backoff

```go
func (h *MyHubReader) scheduleReconnection(ski string, attempt int) {
    delay := time.Duration(math.Pow(2, float64(attempt))) * time.Second
    maxDelay := 5 * time.Minute
    
    if delay > maxDelay {
        delay = maxDelay
    }
    
    time.Sleep(delay)
    
    if err := hub.ConnectSKI(ski, true); err != nil {
        if attempt < 5 {
            h.scheduleReconnection(ski, attempt+1)
        }
    }
}
```

### Circuit Breaker

```go
type ConnectionManager struct {
    failures map[string]int
    lastAttempt map[string]time.Time
}

func (cm *ConnectionManager) ShouldConnect(ski string) bool {
    failures := cm.failures[ski]
    lastAttempt := cm.lastAttempt[ski]
    
    // Back off after repeated failures
    if failures > 3 && time.Since(lastAttempt) < 30*time.Second {
        return false
    }
    
    return true
}
```

---

## Quick Reference

### Most Common Issues

1. **"connection refused"** → Check port 4712 and network connectivity
2. **"handshake timeout"** → Verify `AllowWaitingForTrust()` responds quickly
3. **"no devices discovered"** → Check mDNS/Avahi configuration
4. **"invalid certificate"** → Regenerate certificate with proper parameters
5. **"too many connections"** → Adjust connection limits or monitor resource usage

### Emergency Debugging

```go
// Add this to any HubReader method for quick debugging
func (h *MyHubReader) RemoteSKIConnected(ski string) {
    log.Printf("DEBUG: Connected to %s at %v", ski, time.Now())
    debug.PrintStack() // Print call stack if needed
}
```

### Health Check

```go
func (h *MyHubReader) healthCheck() {
    // Verify hub is operational
    if !hub.IsRunning() {
        log.Error("Hub not running!")
    }
    
    // Check connection count
    if connectionCount := hub.ConnectionCount(); connectionCount == 0 {
        log.Warning("No active connections")
    }
    
    // Verify mDNS
    if !mdns.IsAnnouncing() {
        log.Error("mDNS not announcing")
    }
}
```

For persistent issues, see:
- [TROUBLESHOOTING.md](TROUBLESHOOTING.md) - Comprehensive debugging guide
- [SECURITY.md](../SECURITY.md) - Security-related error explanations
- [GitHub Issues](https://github.com/enbility/ship-go/issues) - Known issues and solutions