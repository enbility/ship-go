# SHIP Specification Deviations

**Last Updated:** 2025-07-06  
**Status:** Active

## Change History

### 2025-07-06
- Updated TLS fragment length section with comprehensive analysis
- Added reference to TLS_FRAGMENT_ANALYSIS.md document
- Updated document to follow new documentation standards

## Critical Finding: Implementation Challenges

**The SHIP 1.0.1 specification contains requirements that are extremely difficult or impractical to implement, particularly for Go-based applications.** Many implementations must make engineering trade-offs between strict compliance and practical functionality.

Key challenging requirements:
- **1024-byte TLS fragments**: Not supported by Go's crypto/tls; severe performance impact even when possible
- **Double connection handling**: "Most recent" criterion creates race conditions in distributed systems
- **Certificate validation**: Ambiguous requirements about verification without enforcement

**Regulatory Context**: These implementation challenges affect compliance assessment. Go-based implementations face platform limitations, while all implementations must balance specification compliance with performance and reliability requirements.

## Table of Contents
1. [Critical Deviations](#critical-deviations)
2. [Minor Deviations](#minor-deviations)
3. [Additions Beyond Spec](#additions-beyond-spec)
4. [Unimplemented Optional Features](#unimplemented-optional-features)
5. [Implementation Choices for Ambiguous Spec Areas](#implementation-choices-for-ambiguous-spec-areas)

---

## Critical Deviations

### 1. Double Connection Prevention Logic

**Spec Reference**: Section 12.2.2  
**Spec Requirement**:
> "If a SHIP node recognizes that there are two or more simultaneous connections to another SHIP node, the SHIP node with the bigger 160 bit SKI value SHALL only keep the most recent connection open"

**Implementation**:
```go
// hub/hub_connections.go:230-269
// The connection initiated by the higher SKI will be kept
if incomingRequest {
    keep = remoteSKI > h.localService.SKI()
} else {
    keep = h.localService.SKI() > remoteSKI
}
```

**Deviation Details**:
- Ship-go uses "connection initiator" logic instead of "most recent connection"
- The node with higher SKI keeps the connection it initiated
- The node with lower SKI keeps the connection initiated by the higher SKI node

**Rationale**:
1. The spec's "most recent" approach has an inherent race condition
2. Without synchronized clocks, "most recent" is ambiguous
3. Two nodes could simultaneously decide different connections are "most recent"
4. The initiator-based approach provides deterministic behavior

**Impact**:
- May cause connection drops when interoperating with spec-compliant implementations
- Both approaches prevent double connections, but differently
- Could lead to connection instability during simultaneous connection attempts

**Risk Level**: **HIGH** - Interoperability issue

**Mitigation**:
- Test extensively with other SHIP implementations
- Document this deviation clearly for users
- Consider implementing a compatibility mode

**Race Condition Note**:
The double connection prevention logic has an inherent race condition window that cannot be completely eliminated in distributed systems. The implementation includes the following mitigations:

1. **Atomic Connection Registry Operations**: The `UnregisterConnectionIfMatch()` method ensures atomic compare-and-delete operations to prevent race conditions when connections are being closed while new ones register.

2. **Connection Timing**: There's still a small window between checking for existing connections and registering new ones where race conditions can occur. This is documented and accepted as a limitation of distributed systems.

3. **Testing**: Comprehensive race condition tests have been added to verify the implementation remains stable under concurrent connection scenarios.

---

### 2. PIN Verification Implementation

**Spec Reference**: Section 12.5 and 13.4.4.3  
**Spec Requirement**: PIN verification with multiple states (None, Required, Optional, PinOk)

**Implementation**:
```go
// ship/hs_pin.go
// Only supports PinStateTypeNone
pinState := model.ConnectionPinState{
    ConnectionPinState: model.ConnectionPinStateType{
        PinState: model.PinStateTypeNone,
    },
}
```

**Deviation Details**:
- Only implements `PinStateTypeNone` (no PIN verification)
- Cannot send or receive PINs
- Cannot achieve "second factor trust level" (16-32)
- No PIN generation or validation logic

**Rationale**:
- PIN support deemed unnecessary for the target use cases
- Reduces implementation complexity
- User verification modes provide sufficient security

**Impact**:
- Cannot achieve highest trust levels requiring PIN
- May limit integration with devices requiring PIN verification
- Some commissioning scenarios unavailable

**Risk Level**: **MEDIUM** - Feature limitation

**Note**: This is an acceptable deviation as PIN support is not required for all implementations.

---

## Minor Deviations

### 3. Access Methods Implementation

**Spec Reference**: Section 13.4.6  
**Spec Requirement**: Full access methods information including DNS and mDNS details

**Implementation**:
```go
// ship/hs_access.go
// Only sends ID, no DNS or mDNS information
accessMethods := model.AccessMethodsType{
    Id: h.localService.ID(),
}
```

**Deviation Details**:
- Only exchanges device IDs
- Does not populate `accessMethods.dnsSd_mDns` field
- Does not support `accessMethods.dns.uri` field
- Limited reverse connection capabilities

**Rationale**:
- Simplified implementation for local network use
- Reverse connections not critical for primary use cases

**Impact**:
- Remote device cannot reliably reconnect to this device
- Cloud integration scenarios limited
- Reduced robustness in dynamic network environments

**Risk Level**: **MEDIUM** - Functionality limitation

---

### 4. TLS Fragment Length Control

**Spec Reference**: Section 9.2  
**Spec Requirement**:
> "Maximum Fragment Length Negotiation Extension SHALL only support a length of 1024 bytes"
> "SHIP node SHALL ensure that the fragment length (TLSPlaintext.length) of outgoing packets does not exceed 1024 bytes"

**Implementation**:
- No TLS Maximum Fragment Length extension negotiation
- No control over TLS record sizes
- TLS records likely exceed 1024 bytes for large messages

**Deviation Details**:
- Go's crypto/tls provides no API for fragment size control
- WebSocket libraries operate above TLS and cannot control record sizes
- SPINE messages routinely exceed 4KB (e.g., device discovery: 4,329 bytes)
- Enforcing 1024-byte fragments would require hundreds of TLS records per message

**Rationale**:
- **Technical Impossibility**: Go's TLS implementation doesn't expose fragment control
- **Performance**: 1024-byte fragments would severely degrade performance
- **Reality**: All SHIP implementations must handle large messages anyway
- **Obsolescence**: Requirement predates understanding of actual SPINE message sizes

**Impact**:
- **None observed** - All tested SHIP implementations work correctly
- Modern devices handle standard TLS record sizes (up to 16KB)
- No reported interoperability issues

**Mitigation**:
- Added 100KB message size limit at WebSocket layer for DoS protection
- This provides better security than TLS fragment limiting

**Risk Level**: **VERY LOW** - Spec requirement appears obsolete

**Regulatory Compliance Risk**: **HIGH** for Go-based implementations
- Go's crypto/tls does not support TLS fragment size control
- OpenSSL-based implementations can technically comply but face severe performance penalties
- Fragmenting to 1024 bytes would increase TLS overhead by 5-10x
- EU CRA and BSI TR-03109 compliance depends on whether performance degradation is acceptable

**Further Reading**: 
- [TLS_FRAGMENT_ANALYSIS.md](../specific-issues/TLS_FRAGMENT_ANALYSIS.md) - Comprehensive analysis including regulatory implications
- [TLS_1024_IMPLEMENTATION_EFFORT.md](../specific-issues/TLS_1024_IMPLEMENTATION_EFFORT.md) - Detailed implementation effort assessment

---

## Additions Beyond Spec

### 5. IPv6 Link-Local Address Filtering

**Spec Reference**: Not specified  
**Implementation**:
```go
// mdns/helper.go
// Filters out IPv6 link-local addresses
if ip.To4() == nil && ip.IsLinkLocalUnicast() {
    continue
}
```

**Addition Details**:
- Removes IPv6 link-local addresses from mDNS announcements
- Not mentioned in SHIP specification
- Practical addition for network compatibility

**Rationale**:
- Link-local IPv6 addresses often cause connection issues
- Many networks don't properly support IPv6
- Improves reliability in mixed network environments

**Impact**:
- Positive: Fewer connection failures
- Negative: May prevent IPv6-only operation

**Risk Level**: **LOW** - Improves compatibility

---

### 6. Reconnection Delay Algorithm

**Spec Reference**: Section 6 mentions reconnection but no algorithm  
**Implementation**:
```go
// hub/hub_connections.go
// Implements exponential backoff with jitter
delay := time.Duration(math.Pow(2, float64(attempts))) * time.Second
delay += time.Duration(rand.Intn(1000)) * time.Millisecond
```

**Addition Details**:
- Exponential backoff for failed connections
- Random jitter to prevent thundering herd
- Maximum delay limits
- Not specified in SHIP

**Rationale**:
- Prevents connection storms
- Reduces load on failing devices
- Standard practice for network protocols

**Impact**:
- Positive: Better network behavior
- Positive: Reduced resource usage
- No negative impact on spec compliance

**Risk Level**: **NONE** - Beneficial addition

---

### 7. EEBUS JSON Transformation

**Spec Reference**: Section 11 (JSON representation)  
**Implementation**:
```go
// ship/helper.go
// Special handling for EEBUS format compatibility
```

**Addition Details**:
- Transforms JSON to match EEBUS library expectations
- Handles array-based object representation
- Not part of SHIP spec but needed for compatibility

**Rationale**:
- Required for integration with eebus-go library
- Maintains compatibility with ecosystem

**Impact**:
- Enables practical use with EEBUS devices
- No impact on SHIP protocol compliance

**Risk Level**: **NONE** - Compatibility feature

---

## Unimplemented Optional Features

### 8. JSON-UTF16 Support

**Spec Reference**: Section 11  
**Status**: Optional feature not implemented

**Details**:
- Only JSON-UTF8 is supported
- JSON-UTF16 marked as optional in spec
- No practical demand for UTF16

**Impact**: Minimal - UTF8 is the standard

---

### 9. Optional Cipher Suites

**Spec Reference**: Section 9.1  
**Status**: Optional features not implemented

**Not Implemented**:
- `TLS_ECDHE_ECDSA_WITH_AES_128_CCM_8`
- Only mandatory cipher suite implemented

**Impact**: None - Optional features

---

## Implementation Choices for Ambiguous Spec Areas

### 10. Hello Timer Edge Cases

**Spec Reference**: Section 13.4.4.1.4.3  
**Ambiguity**: Behavior when `T_prolong` < `T_hello_prolong_min`

**Implementation Choice**:
```go
// ship/hs_hello.go:158
// TODO: clarify: the other side sent us a value below min,
// should this be a handshake error instead? Spec is unclear
```

**Current Behavior**: Accepts the value but adds TODO comment

---

### 11. Certificate Validation

**Spec Reference**: Section 12.1.1  
**Ambiguity**: When to reject connections vs. warnings

**Implementation Choice**:
- Never rejects connections based on certificate properties
- Only validates SKI extraction
- Follows spec guidance: "SHALL NOT affect the general SHIP authentication"

---

### 12. Connection Close During Handshake

**Spec Reference**: Section 13.4.7  
**Ambiguity**: Close behavior during handshake phases

**Implementation Choice**:
- Allows graceful close at any handshake stage
- Sends proper close messages when possible
- Falls back to WebSocket close

---

## Summary and Recommendations

### Understanding Compliance in Context

**Key Reality**: The SHIP 1.0.1 specification contains requirements that are difficult to implement correctly and may require performance trade-offs. Go-based implementations face additional platform-specific limitations.

### Items Requiring Clear Documentation:
1. **Double Connection Logic** - Document the race condition mitigation strategy
2. **PIN Support** - Document as intentionally unsupported  
3. **Access Methods** - Consider implementing for robustness
4. **TLS Fragment Control** - Document Go platform limitation (reference Go issue #20420)

### Regulatory Strategy:
- **Platform limitations**: Explain Go's crypto/tls constraints
- **Performance analysis**: Document the 5-10x overhead of 1024-byte fragments
- **Industry coordination**: Work with other implementations on common approaches
- **Technical education**: Help regulators understand engineering trade-offs

### Low Risk Items:
- IPv6 filtering
- Optional features

### Beneficial Additions:
- Reconnection delays
- EEBUS compatibility
- Robust error handling

### For New Implementations:
1. Clearly document all deviations
2. Test with reference implementations
3. Consider compatibility modes for critical deviations
4. Participate in spec clarification discussions

### For Spec Improvements:
1. **Reconsider 1024-byte requirement** - Allow flexibility for modern implementations
2. **Clarify certificate validation** - Resolve ambiguity about enforcement
3. **Improve double connection handling** - Address distributed system race conditions
4. **Consider performance impacts** - Balance security with practical performance
5. **Platform considerations** - Acknowledge different TLS library capabilities

---

## Version History

- **v1.0** (2024-01): Initial documentation of deviations
- Last updated: 2024-01
- Based on: SHIP TS v1.0.1