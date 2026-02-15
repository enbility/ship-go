# Security Model

ship-go implements the SHIP (Smart Home IP) protocol security model according to SHIP TS 1.0.1. This document explains the security architecture and addresses common security concerns.

## Quick Summary

**Why `InsecureSkipVerify: true` is correct and secure:**
- SHIP requires self-signed certificates (SHIP Section 12.1.1)
- Traditional X.509 PKI validation would always fail
- Security is provided by SKI-based verification at the SHIP protocol level
- This is NOT a security vulnerability - it's required by the specification

## SHIP Security Architecture

### 1. Certificate-Based Identity

SHIP uses self-signed X.509 certificates for device identification:

```go
// Generate SHIP-compliant certificate
cert, err := cert.Generate("MyDevice", "MyOrganization")
if err != nil {
    log.Fatal(err)
}

// Extract SKI (Subject Key Identifier) - the device's identity
ski := cert.SubjectKeyIdentifier()
```

### 2. TLS Configuration Explained

```go
// This configuration is CORRECT for SHIP
tlsConfig := &tls.Config{
    InsecureSkipVerify: true,        // Required - SHIP uses self-signed certs
    Certificates: []tls.Certificate{cert},
    MinVersion: tls.VersionTLS12,    // SHIP requires TLS 1.2+
}
```

**Why this is secure:**
1. **TLS provides encryption** - Data is still encrypted in transit
2. **SHIP validates identity** - SKI verification happens after TLS handshake
3. **Trust is explicit** - Devices must be paired before communication
4. **Local network context** - SHIP is designed for local networks, not internet

### 3. Trust Establishment

SHIP supports four verification modes for establishing trust:

#### Verification Mode: None (Auto-Accept)
```go
func (h *MyHubReader) AllowWaitingForTrust(ski string) bool {
    return true  // Auto-accept - ONLY for development/testing
}
```
**WARNING**: Never use auto-accept in production!

#### Verification Mode: User Confirmation
```go
func (h *MyHubReader) AllowWaitingForTrust(ski string) bool {
    // Show user the device requesting connection
    fmt.Printf("Device %s wants to connect. Accept? (y/n): ", ski)
    
    var response string
    fmt.Scanln(&response)
    return response == "y"
}
```

#### Verification Mode: Matching Display Code
Not implemented in ship-go (requires PIN support)

#### Verification Mode: Verified Certificate
Certificate pre-verification - suitable for managed deployments

### 4. Security During Handshake

The SHIP handshake (Section 13.4.4) provides multiple security checkpoints:

1. **CMI Phase** - Connection initialization
2. **Hello Phase** - Trust verification (60-second timeout)
3. **Protocol Phase** - Version negotiation
4. **PIN Phase** - Additional verification (ship-go supports "none" only)
5. **Access Phase** - Final authorization

Each phase can reject unauthorized connections.

## Security Best Practices

### Production Deployment

1. **Never use auto-accept in production**
   ```go
   // BAD - Don't do this in production
   func (h *MyHubReader) AllowWaitingForTrust(ski string) bool {
       return true  // Security risk!
   }
   
   // GOOD - Implement user verification
   func (h *MyHubReader) AllowWaitingForTrust(ski string) bool {
       return h.userInterface.PromptUserForTrust(ski)
   }
   ```

2. **Set connection limits to prevent DoS**
   ```go
   hub.SetMaxConnections(10)  // Adjust based on your device capacity
   ```

3. **Monitor failed connection attempts**
   ```go
   func (h *MyHubReader) ServiceConnectionStateChanged(ski string, state api.ConnectionState) {
       if state == api.ConnectionStateError {
           h.logSecurityEvent("connection_failed", ski)
       }
   }
   ```

### Network Security

1. **Use network isolation** - Run SHIP devices on isolated network segments
2. **Firewall configuration** - Only expose port 4712 to trusted networks
3. **Monitor mDNS traffic** - Watch for unusual discovery patterns

### Certificate Management

1. **Secure storage** - Protect private keys
   ```go
   // Store certificates securely
   certPath := filepath.Join(secureDir, "ship.crt")
   keyPath := filepath.Join(secureDir, "ship.key")
   os.Chmod(keyPath, 0600)  // Restrict key file permissions
   ```

2. **Certificate expiration** - ship-go monitors and warns about expiring certificates
   ```go
   // Warnings appear 90, 30, 7, and 1 days before expiration
   // Log output: "SHIP certificate 'MyDevice' expires in 30 days"
   ```

3. **Unique certificates** - Never share certificates between devices

## Security Configuration

### Connection Security Options

```go
// Maximum connections (default: 10)
hub.SetMaxConnections(20)

// Connection timeouts are fixed by SHIP spec:
// - Hello timeout: 60 seconds (production)
// - CMI timeout: 10 seconds
```

### Debug Mode Security

When running tests with fast timeouts:
```bash
# Fast timeouts for testing only - reduces security
go test -tags=test ./...
```

## Common Security Questions

### Q: Is `InsecureSkipVerify: true` a security vulnerability?

**No.** This is required by SHIP specification because:
- SHIP uses self-signed certificates by design
- Traditional PKI validation would always fail
- Security verification happens at the SHIP protocol level using SKI
- The TLS connection is still encrypted

### Q: How do I secure my SHIP deployment?

1. Implement proper trust verification (don't auto-accept)
2. Use network isolation
3. Set appropriate connection limits
4. Monitor for unusual connection patterns
5. Keep certificates secure and unique per device

### Q: What about the cipher suite?

ship-go uses Go's default TLS 1.2+ cipher suites, which include the mandatory SHIP cipher suite `TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256`.

### Q: Can I use ship-go over the internet?

SHIP is designed for local networks. While technically possible, internet use is not recommended because:
- mDNS discovery doesn't work across networks
- Security model assumes local network trust boundaries
- No built-in authentication beyond device pairing

## Vulnerability Reporting

Security vulnerabilities should be reported responsibly. If you discover a security vulnerability:

1. **Do NOT** open a public GitHub issue
2. Email details to: security@enbility.net
3. Include:
   - Description of the vulnerability
   - Steps to reproduce
   - Potential impact
   - Suggested fix (if any)

Reports will be reviewed and addressed based on severity and available resources. This is an open source project developed and maintained on a voluntary basis.

## Further Reading

- [SHIP Specification Security Sections](https://www.eebus.org/media-downloads/)
  - Section 9: TLS Requirements
  - Section 12: Certificate Model
  - Section 13.4.4.2: Trust Establishment
- [TLS Security Analysis](analysis-docs/detailed-analysis/TLS_SECURITY_ANALYSIS.md)
- [SHIP Implementation Analysis](analysis-docs/detailed-analysis/SHIP_1.0.1_IMPL_ANALYSIS.md)

## Summary

The SHIP security model is designed for local network environments with explicit trust establishment. While `InsecureSkipVerify: true` may appear concerning, it's the correct implementation for SHIP's self-signed certificate model. Security is maintained through:

1. Encrypted TLS connections
2. SKI-based device identity verification  
3. Explicit pairing and trust establishment
4. Connection limiting and timeout protections
5. Local network deployment context

Always implement proper user verification for production deployments and never use auto-accept mode outside of development environments.
