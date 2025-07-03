# TLS Security Analysis for ship-go in Local Network Context

## Executive Summary

After analyzing the SHIP specification v1.0.1 and the ship-go implementation, the TLS configuration with `InsecureSkipVerify: true` is **NOT a critical security vulnerability** in this specific context. Instead, it's a **required implementation detail** that aligns with the SHIP specification's security model. However, there are still important security considerations to address.

## Key Findings from SHIP Specification

### 1. SHIP Uses Self-Signed Certificates (Section 12.1)

The SHIP specification explicitly states:
- "A self-signed or PKI based certificate MUST be used" (Section 12.1.1)
- "all certificates are locally signed" (referenced in code comment)
- "interoperability within SHIP does not depend on using a certain PKI"
- "If a SHIP node does not know the PKI of another SHIP node, the corresponding PKI based certificate is just treated like a self-signed certificate"

### 2. Trust is Based on Public Key/SKI, Not PKI (Section 12.1.1)

Critical insight from the specification:
> "In general, a SHIP node MUST at least verify the public key of the certificate with one of the four registration modes described in chapter 12.3.1. Any other evaluation of the certificate is optional or manufacturer specific and SHALL NOT affect the general SHIP authentication and communication."

> "An invalid PKI certificate SHOULD be handled like a self-signed certificate, as trust in SHIP relies on the SHIP node specific public key and not a PKI."

### 3. Certificate Validation Requirements

The specification explicitly states that traditional certificate validation should NOT prevent communication:
- Certificate lifetime checks are OPTIONAL
- PKI validation is OPTIONAL
- "if optional or manufacturer specific checks fail, but the received public key is authenticated correctly, the SHIP node SHOULD still allow communication"

## Why `InsecureSkipVerify: true` is Correct

### 1. It's Not Actually "Insecure" in SHIP Context

The code correctly implements SHIP's security model:
```go
// SHIP 12.1: all certificates are locally signed
InsecureSkipVerify: true, // #nosec G402
```

This setting bypasses Go's default certificate validation (which expects PKI-signed certificates) because:
- SHIP uses self-signed certificates by design
- Trust is established through the SHIP handshake, not PKI
- The actual security comes from verifying the SKI during the SHIP protocol handshake

### 2. Actual Security Implementation

The code DOES implement proper security checks:

```go
// hub/hub_connections.go:188-199
if len(remoteCerts) == 0 || remoteCerts[0].SubjectKeyId == nil {
    // Close connection as we couldn't get the remote SKI
    return errors.New("could not get remote SKI from certificate")
}

if _, err := cert.SkiFromCertificate(remoteCerts[0]); err != nil {
    // Close connection as the remote SKI can't be correct
    return errors.New(err)
}
```

The security model:
1. TLS provides encryption and initial certificate exchange
2. SKI is extracted from the certificate
3. Trust is verified through SHIP's four verification modes:
   - Auto accept (user trust level = 8)
   - User verify (user trust level = 32)
   - Commissioning (user trust level = 32-96)
   - User input (user trust level = 64)

### 3. Local Network Context

The specification acknowledges local network usage:
- "For local connections, the server name SHALL be equal to the mDNS host name" (Section 9.4)
- Discovery happens via mDNS on local network
- No dependency on internet PKI infrastructure

## Real Security Issues to Address

### 1. Missing Certificate Expiration Warnings (Low Priority)

While the spec says lifetime checks are optional, implementing them would be beneficial:
- Add logging when certificates are expired
- Allow connection but warn users
- This aligns with spec: "a SHIP node MAY inform the user about the reasons for a failed optional check"

### 2. Weak Cipher Suite (Medium Priority)

The required cipher suite is outdated:
```go
TLS_ECDHE_ECDSA_WITH_AES_128_CBC_SHA256 // Required by SHIP 9.1
```

Recommendation:
- Document that this is a SHIP specification requirement
- Consider proposing spec update to EEBUS for modern ciphers
- The optional cipher `TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256` is more secure

### 3. SHA1 for SKI Generation (Low Priority)

```go
// SHIP 12.2: Required to be created according to RFC 3280 4.2.1.2
ski := sha1.Sum(publicKey.Bytes())
```

This is required by RFC 3280 and SHIP spec, not a implementation choice. SHA1 is only used for identifier generation, not for cryptographic security.

### 4. Connection Flooding Protection (High Priority)

This IS a real security issue not addressed by the spec:
- No rate limiting for connection attempts
- No protection against SKI enumeration attacks
- Could lead to resource exhaustion

## Recommendations

### 1. Document the Security Model
Add clear documentation explaining:
- Why `InsecureSkipVerify: true` is correct for SHIP
- How trust is established through SKI verification
- The role of the four verification modes

### 2. Implement Optional Security Features
- Add certificate expiration logging (not blocking)
- Implement connection rate limiting
- Add metrics for security monitoring

### 3. Improve Error Messages
Replace generic "insecure" warnings with SHIP-specific explanations:
```go
// Instead of: "InsecureSkipVerify: true // security risk!"
// Use: "InsecureSkipVerify: true // Required by SHIP 12.1 for self-signed certificates"
```

### 4. Add Security Configuration Options
- Allow stricter validation for enterprise deployments
- Make rate limiting configurable
- Add option to require specific verification modes

## Conclusion

The TLS configuration in ship-go correctly implements the SHIP specification's security model. The use of `InsecureSkipVerify: true` is not a security vulnerability but a required implementation detail for a protocol that uses self-signed certificates and establishes trust through other means.

The real security focus should be on:
1. Proper SKI verification (already implemented)
2. User consent through verification modes (already implemented)
3. Rate limiting and DoS protection (needs implementation)
4. Clear documentation of the security model (needs improvement)

The SHIP protocol's security model is appropriate for local network IoT devices where:
- Traditional PKI is impractical
- User consent is required for device pairing
- Physical proximity provides additional security
- Devices need to work without internet connectivity