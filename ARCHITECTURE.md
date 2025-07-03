# ship-go Architecture Overview

This document provides a comprehensive overview of the ship-go library architecture, component relationships, and data flow patterns.

## Table of Contents
- [Overview](#overview)
- [System Architecture](#system-architecture)
- [Core Components](#core-components)
- [Connection Flows](#connection-flows)
- [State Machines](#state-machines)
- [Security Architecture](#security-architecture)
- [Threading and Concurrency](#threading-and-concurrency)
- [Error Handling](#error-handling)
- [Extension Points](#extension-points)

## Overview

ship-go is a Go implementation of the SHIP (Smart Home IP) protocol version 1.0.1, which is part of the EEBUS specification. It provides secure device discovery, pairing, and communication capabilities for smart home energy management systems.

## System Architecture

### High-Level Component Diagram

```mermaid
graph TB
    subgraph "Application Layer"
        APP[eebus-go Application]
    end
    
    subgraph "ship-go Library"
        subgraph "API Layer"
            HI[HubInterface]
            HRI[HubReaderInterface]
        end
        
        subgraph "Core Components"
            HUB[Hub<br/>Coordinator]
            SHIP[SHIP<br/>Connection]
            MDNS[mDNS<br/>Manager]
        end
        
        subgraph "Transport Layer"
            WS[WebSocket<br/>Handler]
        end
        
        subgraph "Security Layer"
            CERT[Certificate<br/>Manager]
        end
    end
    
    subgraph "Network"
        NET[Network<br/>TCP/UDP]
    end
    
    APP -->|Uses| HI
    APP -->|Implements| HRI
    HI --> HUB
    HUB --> SHIP
    HUB --> MDNS
    HUB -.->|Events| HRI
    SHIP --> WS
    SHIP --> CERT
    MDNS --> NET
    WS --> NET
    
    style APP fill:#e1f5fe
    style HUB fill:#fff3e0
    style SHIP fill:#f3e5f5
    style MDNS fill:#e8f5e9
    style WS fill:#fce4ec
    style CERT fill:#fff9c4
```

### Data Flow Architecture

```mermaid
flowchart TD
    subgraph "Application"
        A[Application Logic]
    end
    
    subgraph "Hub Layer"
        H[Hub]
        HR[Service Registry]
        PC[Pairing Controller]
    end
    
    subgraph "Protocol Layer"
        SC[SHIP Connection]
        HS[Handshake FSM]
        MR[Message Router]
    end
    
    subgraph "Discovery Layer"
        MD[mDNS Discovery]
        AV[Avahi Provider]
        ZC[Zeroconf Provider]
    end
    
    subgraph "Transport Layer"
        WS[WebSocket]
        TLS[TLS Security]
    end
    
    A <-->|HubInterface| H
    H --> HR
    H --> PC
    H <--> SC
    H <--> MD
    SC --> HS
    SC --> MR
    MD --> AV
    MD --> ZC
    SC <--> WS
    WS --> TLS
    
    A -.->|Events| H
    MD -.->|Discovery Events| H
    SC -.->|Connection Events| H
```

## Core Components

### 1. Hub (`hub/`)
The Hub is the central coordinator that orchestrates all SHIP/SPINE communication.

```mermaid
classDiagram
    class Hub {
        -connections map[string]ShipConnection
        -remoteServices map[string]RemoteService
        -mdns MdnsInterface
        -server WebsocketServer
        +Start()
        +Stop()
        +RegisterRemoteService(service)
        +PairRemoteService(ski)
        +UnpairRemoteService(ski)
        +ConnectionForSKI(ski) ShipConnection
    }
    
    class HubInterface {
        <<interface>>
        +Start() error
        +Stop()
        +RegisterRemoteService(service)
        +PairRemoteService(ski)
        +ServiceForSKI(ski) RemoteService
    }
    
    class HubReaderInterface {
        <<interface>>
        +ServiceConnectionStateChanged(ski, state)
        +ServicePairingDetailUpdate(ski, detail)
        +AllowWaitingForTrust(ski) bool
    }
    
    Hub ..|> HubInterface
    Hub --> HubReaderInterface : notifies
```

**Key Responsibilities:**
- Manages WebSocket server for incoming connections
- Coordinates mDNS discovery and announcements  
- Handles device pairing and connection state management
- Maintains registry of known remote services
- Prevents double connections using SKI comparison

### 2. SHIP Connection (`ship/`)
Implements the SHIP protocol handshake and message routing.

```mermaid
classDiagram
    class ShipConnection {
        -state HandshakeState
        -localService ServiceDetails
        -remoteService ServiceDetails
        -wsConnection WebsocketConnection
        +HandleConnection()
        +Send(message)
        +Close()
    }
    
    class HandshakeState {
        <<enumeration>>
        Init
        PendingHello
        PendingProtocol
        PendingPin
        PendingAccess
        Completed
        Aborted
    }
    
    ShipConnection --> HandshakeState
    ShipConnection --> WebsocketConnection
```

### 3. WebSocket Layer (`ws/`)
Provides reliable message transport over WebSocket connections.

**Features:**
- Automatic ping/pong for connection health monitoring
- Buffered write operations with thread safety
- Graceful connection closure handling
- Configurable timeouts and buffer sizes

### 4. mDNS Discovery (`mdns/`)
Handles service discovery and announcement using multicast DNS.

```mermaid
graph LR
    subgraph "mDNS Manager"
        MM[MdnsManager]
        MM --> |selects| MP{Provider?}
        MP -->|Linux| AV[Avahi Provider]
        MP -->|Fallback| ZC[Zeroconf Provider]
    end
    
    subgraph "Operations"
        ANN[Announce Service]
        DISC[Discover Services]
        RES[Resolve Service]
    end
    
    MM --> ANN
    MM --> DISC
    MM --> RES
```

### 5. Certificate Management (`cert/`)
Handles X.509 certificate operations for device identification.

**Key Functions:**
- Generate self-signed certificates with EEBUS extensions
- Extract and normalize SKI (Subject Key Identifier)
- Configure TLS for secure connections
- Validate certificate chains

## Connection Flows

### Device Discovery and Connection Flow

```mermaid
sequenceDiagram
    participant App as Application
    participant Hub as Hub
    participant mDNS as mDNS Manager
    participant Net as Network
    participant Remote as Remote Device
    
    App->>Hub: Start()
    Hub->>mDNS: StartSearch()
    
    loop Discovery
        mDNS->>Net: Multicast Query
        Remote->>Net: Service Announcement
        Net->>mDNS: Service Found
        mDNS->>Hub: ServiceFound(details)
        Hub->>App: ServiceConnectionStateChanged()
    end
    
    App->>Hub: PairRemoteService(ski)
    Hub->>Hub: CreateConnection()
    Hub->>Remote: WebSocket Connect
    Remote->>Hub: Accept Connection
    
    Note over Hub,Remote: SHIP Handshake begins
```

### SHIP Handshake Sequence

```mermaid
sequenceDiagram
    participant Client
    participant Server
    
    Note over Client,Server: WebSocket Connected
    
    rect rgb(200, 220, 240)
        Note right of Client: CMI Phase
        Client->>Server: ConnectionModeInit
        Server->>Client: ConnectionModeInit
    end
    
    rect rgb(220, 240, 200)
        Note right of Client: Hello Phase
        Client->>Server: ConnectionHello
        Server->>Client: ConnectionHello
        Note over Client,Server: Trust check
    end
    
    rect rgb(240, 220, 200)
        Note right of Client: Protocol Phase
        Client->>Server: MessageProtocolHandshake
        Server->>Client: MessageProtocolHandshake
        Note over Client,Server: Protocol negotiation
    end
    
    rect rgb(240, 200, 220)
        Note right of Client: PIN Phase
        Client->>Server: ConnectionPinState
        Server->>Client: ConnectionPinState
        Note over Client,Server: PIN verification (none)
    end
    
    rect rgb(200, 240, 220)
        Note right of Client: Access Phase
        Client->>Server: AccessMethodsRequest
        Server->>Client: AccessMethods
    end
    
    Note over Client,Server: Ready for SPINE communication
```

### Pairing Process Flow

```mermaid
flowchart TD
    Start([Service Discovered])
    Start --> Check{Already<br/>Paired?}
    
    Check -->|Yes| Connect[Establish Connection]
    Check -->|No| Pair[Initiate Pairing]
    
    Pair --> Auto{Auto-Accept<br/>Mode?}
    Auto -->|Yes| Trust[Add to Trusted]
    Auto -->|No| Wait[Wait for User]
    
    Wait --> User{User<br/>Approves?}
    User -->|Yes| Trust
    User -->|No| Reject[Reject Pairing]
    
    Trust --> Connect
    Connect --> Handshake[SHIP Handshake]
    Handshake --> Ready([Connection Ready])
    
    Reject --> End([Connection Refused])
```

## State Machines

### Connection State Machine

```mermaid
stateDiagram-v2
    [*] --> Disconnected
    
    Disconnected --> Connecting: Connect()
    Connecting --> Connected: Success
    Connecting --> Error: Failed
    
    Connected --> Handshaking: Start SHIP
    Handshaking --> Ready: Handshake Complete
    Handshaking --> Error: Handshake Failed
    
    Ready --> Disconnecting: Close()
    Error --> Disconnecting: Close()
    
    Disconnecting --> Disconnected: Closed
    
    Ready --> Error: Connection Lost
    Error --> Connecting: Reconnect
```

### SHIP Handshake State Machine

```mermaid
stateDiagram-v2
    [*] --> Init
    
    Init --> PendingHello: Send/Receive CMI
    PendingHello --> PendingProtocol: Hello Exchanged
    PendingProtocol --> PendingPin: Protocol Agreed
    PendingPin --> PendingAccess: PIN Verified
    PendingAccess --> Completed: Access Granted
    
    PendingHello --> Aborted: Trust Failed
    PendingProtocol --> Aborted: Protocol Mismatch
    PendingPin --> Aborted: PIN Failed
    PendingAccess --> Aborted: Access Denied
    
    Aborted --> [*]
    Completed --> [*]
```

## Security Architecture

### Trust Model

```mermaid
graph TD
    subgraph "Device Identity"
        CERT[X.509 Certificate]
        SKI[Subject Key Identifier]
        CERT --> SKI
    end
    
    subgraph "Trust Establishment"
        PAIR[Pairing Process]
        REG[Service Registry]
        PAIR --> REG
    end
    
    subgraph "Connection Security"
        TLS[TLS Encryption]
        SHIP[SHIP Handshake]
        TLS --> SHIP
    end
    
    SKI --> PAIR
    REG --> TLS
```

### Security Layers
1. **Transport Security**: TLS 1.2+ with mutual authentication
2. **Device Identity**: X.509 certificates with EEBUS extensions
3. **Trust Management**: Explicit pairing with persistent trust store
4. **Protocol Security**: SHIP handshake validates trust before data exchange

## Threading and Concurrency

### Goroutine Architecture

```mermaid
graph TD
    subgraph "Hub Goroutines"
        HM[Main Hub Loop]
        HS[HTTP Server]
    end
    
    subgraph "Per Connection"
        CR[Connection Reader]
        CW[Connection Writer]
        CP[Ping Handler]
    end
    
    subgraph "Discovery"
        MS[mDNS Search]
        MA[mDNS Announce]
    end
    
    HM --> CR
    HM --> CW
    HS --> HM
    MS --> HM
    MA --> HM
```

### Synchronization Strategy
- **Mutexes**: Protect shared state (connection maps, service registry)
- **Channels**: Coordinate goroutine lifecycle and event propagation
- **Atomic Operations**: For connection state and counters
- **Context**: Graceful cancellation of operations

## Error Handling

### Error Propagation Flow

```mermaid
flowchart TD
    E1[Transport Error] --> SC[SHIP Connection]
    E2[Protocol Error] --> SC
    E3[Certificate Error] --> SC
    
    SC --> H[Hub]
    H --> CB[Callbacks]
    CB --> APP[Application]
    
    H --> LOG[Logging]
    
    SC --> RC{Recoverable?}
    RC -->|Yes| RETRY[Retry Logic]
    RC -->|No| CLOSE[Close Connection]
    
    RETRY --> SC
    CLOSE --> H
```

### Error Categories
1. **Transport Errors**: Connection loss, timeouts
2. **Protocol Errors**: Invalid messages, state violations
3. **Security Errors**: Certificate validation, trust failures
4. **Discovery Errors**: mDNS failures, network issues

## Extension Points

### Interface Implementation Guide

```mermaid
graph LR
    subgraph "Implement for Integration"
        HRI[HubReaderInterface]
        LI[LoggingInterface]
    end
    
    subgraph "Implement for Customization"
        MPI[MdnsProviderInterface]
        WSI[WebsocketInterface]
    end
    
    subgraph "Your Application"
        APP[Application Code]
        CUST[Custom Providers]
    end
    
    APP -.->|implements| HRI
    APP -.->|implements| LI
    CUST -.->|implements| MPI
    CUST -.->|implements| WSI
```

### Extension Examples
1. **Custom mDNS Provider**: Implement `MdnsProviderInterface` for platform-specific discovery
2. **Alternative Transport**: Implement `WebsocketDataWriterInterface` for non-WebSocket transport
3. **Custom Logging**: Implement `LoggingInterface` for application-specific logging
4. **Authentication Extensions**: Extend handshake for custom authentication methods

## Configuration and Deployment

### Deployment Architecture

```mermaid
graph TB
    subgraph "Linux Deployment"
        LD[ship-go Application]
        AD[Avahi Daemon]
        LD --> AD
    end
    
    subgraph "Cross-Platform Deployment"
        CP[ship-go Application]
        ZC[Built-in Zeroconf]
        CP --> ZC
    end
    
    subgraph "Container Deployment"
        CD[ship-go Container]
        NET[Host Network Mode]
        CD --> NET
    end
```

### Configuration Requirements
- **Certificate**: Valid X.509 certificate with EEBUS extensions
- **Network**: Multicast-enabled network for mDNS
- **Ports**: Configurable WebSocket server port (default: 4712)
- **Permissions**: Network access for mDNS and WebSocket

This architecture provides a robust, secure, and extensible foundation for EEBUS device communication while maintaining clean separation of concerns and platform flexibility.