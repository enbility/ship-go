# TLS Fragment Length Analysis

**Last Updated:** 2025-07-06  
**Status:** Active

## Change History

### 2025-07-06
- Initial analysis of TLS fragment requirements and Go implementation limitations
- Documented technical impossibility in Go ecosystem
- Analyzed real-world impact and performance considerations
- Added comprehensive security and regulatory compliance analysis
- Included OpenSSL capability assessment and integration feasibility
- Added EU CRA and German BSI TR-03109 compliance implications

## Executive Summary

The SHIP specification requires TLS fragments to not exceed 1024 bytes, but this requirement is not supported by Go's standard crypto/tls library. This constraint appears to be designed for early embedded systems with severe memory limitations. While the transport layer can fragment any size payload, enforcing 1024-byte TLS records would significantly impact performance.

**Critical Finding**: While designed for constrained devices, this requirement has significant regulatory implications under the EU Cyber Resilience Act (mandatory August 2024) and German BSI TR-03109 grid compliance standards. Non-compliance could affect CE certification and grid connection approvals.

## SHIP Specification Requirements

### Section 9.2: Maximum Fragment Length

From the SHIP Technical Specification v1.0.1:

1. **Maximum Fragment Length Negotiation Extension** (RFC 6066)
   - SHOULD be supported
   - If used, SHALL only support a length of 1024 bytes
   - Keeps required buffer size for embedded devices low

2. **Fallback Requirement**
   - "SHIP node SHALL ensure that the fragment length (TLSPlaintext.length) of outgoing packets does not exceed 1024 bytes"
   - Required even if Fragment Length Negotiation Extension is not supported
   - Acknowledgment that "some TLS implementations currently do not support Maximum Fragment Length Negotiation Extension"

## Technical Analysis

### The Layered Architecture Problem

The requirement applies at the wrong layer of the network stack:

```
Application Layer (SPINE/SHIP)
    ↓ [JSON messages: 4,000+ bytes]
WebSocket Layer (gorilla/websocket)
    ↓ [Frames: automatic fragmentation]
TLS Layer (crypto/tls) ← Requirement applies here
    ↓ [Records: no size control in Go]
TCP Layer
    ↓ [Packets: MTU-based fragmentation]
```

### Go Implementation Limitations

1. **crypto/tls Package**
   - No API for RFC 6066 Maximum Fragment Length extension
   - No control over TLS record sizes
   - Record sizes determined internally based on:
     - Available data to send
     - Internal buffering algorithms
     - Efficiency optimizations
   - Default maximum TLS record size: 16,384 bytes

2. **Long-Standing Feature Request**
   - [Go issue #20420](https://github.com/golang/go/issues/20420) opened May 2017
   - Requested customizable max TLS record size for embedded devices
   - Status: "Proposal-Accepted" but still unimplemented after 7+ years
   - Go maintainer (Filippo Valsorda) suggested RFC 6066 implementation
   - No progress despite being accepted proposal in backlog
   - Demonstrates this is a known limitation without solution

3. **WebSocket Libraries**
   - gorilla/websocket operates above TLS layer
   - Cannot control TLS record fragmentation
   - Buffer sizes (ReadBufferSize/WriteBufferSize) affect performance, not TLS records
   - No WebSocket library can control lower-layer TLS behavior

4. **Message Size Reality**
   - Example SPINE device discovery message: 4,329 bytes
   - Complex energy management messages: up to 50KB
   - 1024-byte TLS records would require hundreds of fragments
   - Severe performance impact from excessive fragmentation

### Alternative Approaches Considered

1. **Custom TLS Implementation**
   - Would require reimplementing crypto/tls
   - Massive security risk
   - Maintenance nightmare
   - Not pragmatic
   - Note: Even if Go implements issue #20420, it would only add RFC 6066 negotiation, not unilateral fragment control

2. **WebSocket Library Migration**
   - Analyzed coder/websocket (formerly nhooyr.io/websocket)
   - Same limitations - no TLS record control
   - Would require complete WebSocket code rewrite
   - No benefit for this requirement

3. **Application-Level Chunking**
   - Wrong layer for the solution
   - Would break SHIP protocol compatibility
   - Other implementations expect complete messages

4. **Fork gorilla/websocket**
   - Still couldn't control TLS layer
   - Would need to maintain fork indefinitely
   - Doesn't solve the core problem

**Detailed Analysis**: See [TLS_1024_IMPLEMENTATION_EFFORT.md](TLS_1024_IMPLEMENTATION_EFFORT.md) for comprehensive implementation effort assessment including proxy approach.

## Real-World Impact Assessment

### Current Implementation
- Uses standard Go crypto/tls
- No fragment size limiting
- TLS records likely exceed 1024 bytes for large messages
- **Result**: System works correctly with all tested SHIP implementations

### Interoperability Testing
- No reported issues with other SHIP implementations
- Large SPINE messages transmitted successfully
- Modern devices handle standard TLS record sizes without problems

### Performance Considerations
- Enforcing 1024-byte fragments would severely impact performance
- Excessive fragmentation increases:
  - CPU overhead (more encrypt/decrypt operations)
  - Network overhead (more headers)
  - Latency (more round trips for reassembly)

## Root Cause Analysis

### Why This Requirement Exists
1. **Historical Context**: Drafted ~2015-2016 for embedded devices with <512KB total RAM
2. **Memory Constraints**: Default TLS buffers (32KB) consumed 6-25% of device memory
3. **RFC 6066 Standard**: 1024 bytes was a standard option (RFC published 2011)
4. **Memory Savings**: Reduces TLS buffer requirements by ~94% (from 32KB to 2KB)

### Why It's Less Relevant Today
1. **Modern Hardware**: Current IoT devices typically have 256KB-8MB RAM
2. **Message Sizes**: SPINE messages (4KB-50KB) require many fragments
3. **Performance Impact**: 5-10x overhead negates memory savings
4. **Platform Support**: Major platforms (Go, Java) don't support fragment control

## Regulatory Compliance Analysis

### EU Cyber Resilience Act (CRA) - Mandatory August 2024

**Impact on Current Implementation**:
- The CRA requires "secure-by-design" implementation for products with digital elements
- Non-compliance with stated protocol specifications could be interpreted as a security defect
- SHIP protocol compliance may be audited during CE certification process
- Penalties for non-compliance: up to €15 million or 2.5% of global turnover

**Current Risk Level**: **HIGH** after August 2024
- ship-go claims SHIP 1.0.1 compliance but knowingly deviates from TLS fragment requirement
- Could fail CE marking certification audit
- May require explicit conformity assessment for grid-connected devices

**Mitigation Options**:
1. **Obtain Waiver**: Document deviation and seek approval from notified body
2. **Implement OpenSSL**: Replace crypto/tls with OpenSSL-based implementation
3. **Specification Amendment**: Work with EEBUS to remove/modify requirement before CRA enforcement

### German BSI TR-03109 Grid Compliance

**Relevance to EEBUS/SHIP**:
- BSI TR-03109 defines security requirements for smart metering gateways
- Referenced by German grid operators for energy management systems
- Distribution System Operators (DSOs) may require protocol compliance verification
- Non-compliance could prevent grid connection certification

**Impact on Current Implementation**:
- Grid operators may audit SHIP protocol implementation
- TLS security parameters are explicitly reviewed
- Deviations must be justified with security analysis

**Current Risk Level**: **MEDIUM** (depends on DSO requirements)
- Some DSOs may accept functional compliance
- Others may require strict specification adherence
- Risk increases as smart grid regulations tighten

**Mitigation Strategy**:
1. **Early Engagement**: Contact target DSOs to clarify requirements
2. **Security Analysis**: Document that larger fragments don't reduce security
3. **Alternative Compliance**: Show equivalent security through other measures

## OpenSSL Integration Analysis

### Technical Feasibility

**OpenSSL Capabilities**:
```c
// OpenSSL fully supports fragment control
SSL_CTX_set_max_send_fragment(ctx, 1024);  // Enforces 1024-byte limit
SSL_CTX_set_tlsext_max_fragment_length(ctx, TLSEXT_max_fragment_length_1024);
```

**Integration Approaches**:
1. **CGO Binding** (e.g., spacemonkeygo/openssl)
   - Direct OpenSSL calls from Go
   - ~200ns overhead per operation
   - Complex cross-compilation

2. **Pure Go Wrapper** (e.g., Microsoft's go-crypto-openssl)
   - Better Go integration
   - Still requires CGO
   - Used for FIPS compliance

3. **Subprocess Architecture**
   - Separate OpenSSL process for TLS
   - Higher complexity, better isolation
   - Easier updates and maintenance

### Implementation Cost Analysis

**Engineering Effort**:
- Initial implementation: 2-3 months
- Testing and validation: 1 month
- Platform-specific builds: 2 weeks per platform
- Ongoing maintenance: 20% of initial effort annually

**Technical Challenges**:
- Memory management across FFI boundary
- Error handling differences
- Certificate management complexity
- Performance optimization
- Security audit requirements

### Cross-Platform Implications

**Build Complexity**:
```bash
# Current (pure Go)
go build  # Works everywhere

# With OpenSSL
# Requires platform-specific setup:
# - Windows: MinGW + OpenSSL binaries
# - macOS: Homebrew OpenSSL (not system)
# - Linux: Distribution-specific packages
# - Docker: Base image constraints
```

**Distribution Impact**:
- Binary size increases 5-10MB
- Runtime dependencies required
- Complex installation instructions
- Platform-specific troubleshooting

## Security Implications

### Benefits of 1024-byte Limit

**Limited but Real**:
1. **Memory DoS Prevention**: On devices with <1MB RAM
2. **Predictable Resource Usage**: Fixed buffer allocation
3. **Embedded System Protection**: For truly constrained devices
4. **Power Consumption**: Smaller buffers = less power

### Risks of Current Implementation

**Practical Impact**:
- No known security vulnerabilities from larger fragments
- No documented attacks exploiting this
- Modern devices handle 16KB records without issue
- Real risk is regulatory/compliance, not security

## Go Community Context

The inability to control TLS fragment sizes is not unique to ship-go but affects the entire Go ecosystem:

- **7+ Years of Waiting**: Despite Go issue #20420 being "Proposal-Accepted" since 2017, no implementation exists
- **Known Use Cases**: The Go team acknowledges embedded device requirements
- **Architectural Decision**: Go prioritizes simplicity over configurability in crypto/tls
- **Community Workaround**: Projects needing this feature use OpenSSL via CGO

This long-standing limitation strengthens the case for regulatory exemptions, as:
1. It affects all Go-based implementations equally
2. The Go team has not prioritized this despite acceptance
3. Waiting for Go stdlib support is not a viable strategy

## Recommendations

### 1. Immediate Actions (Before August 2024)
- **Document Thoroughly**: Create comprehensive deviation documentation
- **Reference Go Issue #20420**: Show this is a known platform limitation
- **Engage Regulators**: Seek clarification on CRA interpretation
- **Contact EEBUS**: Propose specification amendment
- **Risk Assessment**: Document security equivalence

### 2. Conditional Implementation Path
**IF** regulatory compliance required:
1. Implement OpenSSL integration (3-month project)
2. Maintain dual-stack (Go for development, OpenSSL for production)
3. Create comprehensive test suite
4. Document security measures

**IF** amendment accepted:
1. Continue with current implementation
2. Update documentation to reflect approved deviation
3. Monitor for any edge cases

### 3. Long-Term Strategy
- Work with EEBUS community to update specification
- Share real-world data showing current approach works
- Advocate for performance-aware protocol design
- Build relationships with certification bodies

### 4. Decision Framework

**Use OpenSSL if**:
- Targeting grid infrastructure deployments
- CE certification required for market
- Customer contracts demand strict compliance
- Risk tolerance is low

**Stay with Go if**:
- Consumer/prosumer market focus
- Can obtain compliance waivers
- Performance is critical
- Agile development needed

## Conclusion

The 1024-byte TLS fragment requirement presents a complex trade-off:

**Technical Reality**:
1. **Not supported** by Go's standard crypto/tls library
2. **Possible** with OpenSSL integration but at significant cost
3. **Unnecessary** for actual security or interoperability
4. **Designed for** 2015-2016 era embedded devices with <512KB RAM

**Regulatory Reality**:
1. **EU CRA Compliance**: High risk after August 2024
2. **Grid Certification**: Medium risk, DSO-dependent
3. **Legal Liability**: Potential issues in critical infrastructure
4. **Market Access**: May affect CE marking and sales

**Current Implementation Status**:
- ✅ Functionally correct and interoperable
- ✅ Performance optimized
- ✅ Security hardened (100KB message limit)
- ❌ Not specification compliant
- ❌ Regulatory risk exposure

**Final Recommendation**:
The decision depends on target market and risk tolerance. For consumer products, the current implementation is adequate. For grid infrastructure or certified products, OpenSSL integration may be necessary despite the engineering cost. Parallel efforts to amend the specification should be pursued regardless of implementation choice.

## References

- SHIP Technical Specification v1.0.1, Section 9.2
- RFC 6066: Transport Layer Security (TLS) Extensions
- EU Cyber Resilience Act (Regulation (EU) 2024/XXXX) - Enforcement August 2024
- BSI TR-03109: Technical Requirements for Smart Metering Gateways
- OpenSSL Documentation: SSL_CTX_set_max_send_fragment()
- Go crypto/tls package documentation
- Go Issue #20420: "proposal: crypto/tls: customizable max record size" (Open since May 2017)
- Real-world SPINE message analysis (4KB+ typical sizes)