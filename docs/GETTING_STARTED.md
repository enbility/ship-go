# Getting Started with ship-go

This guide helps you create your first SHIP (Smart Home IP) device using ship-go. For complete protocol specifications, see [SHIP TS 1.0.1](https://www.eebus.org/media-downloads/).

## What is SHIP?

SHIP is a protocol for secure communication between smart home devices like heat pumps, EV chargers, and energy management systems. It provides:

- **Device Discovery** - Find devices on your network via mDNS (Section 5)
- **Secure Communication** - TLS encryption with certificate-based identity (Section 9)
- **Trust Management** - Explicit pairing before data exchange (Section 13.4.4.2)
- **Protocol Negotiation** - 5-phase handshake for reliable connections (Section 13.4.4)

## Prerequisites

- **Go 1.19+** - Required for ship-go
- **Linux with Avahi** (recommended) or any OS for Zeroconf fallback
- **Network with mDNS** - Most local networks support multicast traffic
- **Port 4712** - Must be accessible (SHIP standard port)

## Quick Installation

```bash
go mod init my-ship-device
go get github.com/enbility/ship-go
```

## Your First SHIP Hub (5 minutes)

### Step 1: Run the Example

Copy and run our [quickstart example](../examples/quickstart/main.go):

```bash
# Get the example
curl -o quickstart.go https://raw.githubusercontent.com/enbility/ship-go/dev/examples/quickstart/main.go

# Run it
go mod tidy
go run quickstart.go
```

You should see:
```
🚀 Starting SHIP Hub Quickstart
================================
🔐 Creating new certificate...
📜 Device SKI (identifier): a1b2c3d4e5f6...
✅ SHIP hub running on port 4712
```

Your hub is now discoverable and accepting connections!

### Step 2: Understanding What Just Happened

The quickstart example:

1. **Created a certificate** (SHIP Section 12) - Self-signed cert with unique SKI
2. **Started mDNS announcement** (SHIP Section 5) - Broadcasts "I'm available"
3. **Opened WebSocket server** (SHIP Section 9) - Listens on port 4712
4. **Set auto-accept pairing** - Automatically trusts new devices (dev mode only!)

## Building Your Own Hub

### Basic Hub Structure

```go
package main

import (
    "github.com/enbility/ship-go/api"
    "github.com/enbility/ship-go/cert"
    "github.com/enbility/ship-go/hub"
    "github.com/enbility/ship-go/mdns"
)

// Implement HubReaderInterface to receive SHIP events
type MyHubReader struct{}

func (h *MyHubReader) RemoteSKIConnected(ski string) {
    // Device connected - start SPINE communication
}

func (h *MyHubReader) AllowWaitingForTrust(ski string) bool {
    // Production: Ask user for approval
    // Development: return true
    return askUserForApproval(ski)
}

// ... implement other required methods

func main() {
    // 1. Create certificate
    cert, _ := cert.CreateCertificate("MyUnit", "MyOrg", "DE", "MyDevice")
    
    // 2. Set up mDNS
    mdns := mdns.NewMDNS(/* parameters */)
    
    // 3. Create hub
    reader := &MyHubReader{}
    hub := hub.NewHub(reader, mdns, 4712, cert, serviceDetails)
    
    // 4. Start
    hub.Start()
}
```

### Certificate Management

Every SHIP device needs a unique certificate:

```go
// Create new certificate
certificate, err := cert.CreateCertificate(
    "DeviceUnit",      // Organizational Unit
    "YourCompany",     // Organization
    "DE",              // Country
    "HeatPump-001",    // Common Name (device model + serial)
)

// Extract SKI (device identifier)
x509Cert, _ := x509.ParseCertificate(certificate.Certificate[0])
ski, _ := cert.SkiFromCertificate(x509Cert)
```

**Important**: In production, save and reuse certificates! See [SECURITY.md](../SECURITY.md).

### Service Discovery Setup

Configure mDNS to announce your device:

```go
deviceCategories := []api.DeviceCategoryType{
    // Empty for generic device
}

mdnsManager := mdns.NewMDNS(
    ski,                               // Device SKI
    "YourBrand",                      // Device brand
    "YourModel",                      // Device model  
    "HeatPump",                       // Device type
    "SN12345",                        // Serial number
    deviceCategories,                 // Device categories
    "your-ship-id",                   // SHIP identifier
    "SHIP-YourDevice",                // Service name
    4712,                             // Port
    []string{},                       // Interfaces (empty = all)
    mdns.MdnsProviderSelectionAll,    // Provider (auto-select)
)
```

### Implementing HubReaderInterface

Your hub reader handles all SHIP events:

```go
type ProductionHubReader struct {
    userInterface UserInterface
    spineHandler  SpineHandler
}

// Connection events
func (h *ProductionHubReader) RemoteSKIConnected(ski string) {
    log.Printf("Device %s connected", ski)
    // Set up SPINE message handling
}

func (h *ProductionHubReader) RemoteSKIDisconnected(ski string) {
    log.Printf("Device %s disconnected", ski)
    // Clean up resources
}

// SPINE integration
func (h *ProductionHubReader) SetupRemoteDevice(
    ski string,
    writer api.ShipConnectionDataWriterInterface,
) api.ShipConnectionDataReaderInterface {
    // Return your SPINE message handler
    return h.spineHandler.NewDeviceHandler(ski, writer)
}

// Discovery events
func (h *ProductionHubReader) VisibleRemoteServicesUpdated(services []api.RemoteService) {
    for _, service := range services {
        log.Printf("Found device: %s (%s %s)", service.Ski, service.Brand, service.Model)
    }
}

// Pairing management
func (h *ProductionHubReader) AllowWaitingForTrust(ski string) bool {
    // CRITICAL: Never auto-accept in production!
    return h.userInterface.PromptUserForTrust(ski)
}

func (h *ProductionHubReader) ServicePairingDetailUpdate(ski string, detail *api.ConnectionStateDetail) {
    // Update pairing progress in UI
    h.userInterface.UpdatePairingProgress(ski, detail)
}
```

## Connecting to Other Devices

### Discovering Devices

Once your hub is running, it will automatically discover other SHIP devices:

```go
func (h *MyHubReader) VisibleRemoteServicesUpdated(services []api.RemoteService) {
    for _, service := range services {
        fmt.Printf("Found: %s (Brand: %s, Model: %s)\n", 
            service.Ski, service.Brand, service.Model)
    }
}
```

### Initiating Connections

To connect to a discovered device:

```go
// In your hub reader
hub.RegisterRemoteService(ski, shipID) // Register device
hub.ConnectSKI(ski, true)             // Initiate connection
```

### Handling Pairing

The pairing process (SHIP Section 13.4.4.2) involves trust verification:

```go
func (h *MyHubReader) AllowWaitingForTrust(ski string) bool {
    // Show user the device requesting connection
    deviceInfo := h.getDeviceInfo(ski)
    
    // Prompt user for approval
    fmt.Printf("Device '%s %s' wants to connect. Accept? (y/n): ", 
        deviceInfo.Brand, deviceInfo.Model)
    
    var response string
    fmt.Scanln(&response)
    return response == "y"
}
```

## Production Considerations

### Security

1. **Never auto-accept pairing** in production
   ```go
   func (h *MyHubReader) AllowWaitingForTrust(ski string) bool {
       return false // Force user verification
   }
   ```

2. **Set connection limits** for resource-constrained devices
   ```go
   hub.SetMaxConnections(10) // Adjust based on device capacity
   ```

3. **Validate certificates** and monitor failed connections

### Resource Management

```go
// Monitor connections
func (h *MyHubReader) ServiceConnectionStateChanged(ski string, state api.ConnectionState) {
    switch state {
    case api.ConnectionStateError:
        h.logSecurityEvent("connection_failed", ski)
    case api.ConnectionStateCompleted:
        h.metrics.IncrementConnections()
    }
}

// Graceful shutdown
defer func() {
    log.Println("Shutting down...")
    hub.Shutdown()
}()
```

### Error Handling

```go
// Robust startup
if err := hub.Start(); err != nil {
    log.Fatalf("Failed to start hub: %v", err)
}

// Connection monitoring
func (h *MyHubReader) RemoteSKIDisconnected(ski string) {
    log.Printf("Device %s disconnected", ski)
    
    // Implement reconnection logic if needed
    go h.scheduleReconnection(ski)
}
```

## Common Issues and Solutions

### "Connection refused"
- Check port 4712 is accessible
- Verify firewall settings
- Ensure device is on same network

### "No devices discovered"
- Check mDNS/multicast support on network
- On Linux, ensure `avahi-daemon` is running
- Verify network interfaces are up

### "Handshake timeout"
- Check certificate validity
- Verify trust establishment works
- Enable debug logging for details

### "Certificate errors"
- Ensure certificates have valid SKI
- Don't share certificates between devices
- Check certificate hasn't expired

## Next Steps

1. **Integration with SPINE** - Add higher-level protocol support
2. **Persistent storage** - Save trusted devices and certificates
3. **User interface** - Create pairing and device management UI
4. **Production deployment** - See [production guide](PRODUCTION.md)

## Learning Resources

- [Examples Directory](../examples/) - Working code samples
- [SECURITY.md](../SECURITY.md) - Security model and best practices
- [API Reference](https://pkg.go.dev/github.com/enbility/ship-go) - Complete API documentation
- [SHIP Specification](https://www.eebus.org/media-downloads/) - Official protocol documentation
- [EEBUS Architecture](https://www.eebus.org) - Overview of smart energy ecosystem

## Getting Help

- Check [troubleshooting guide](TROUBLESHOOTING.md) for common issues
- Review [error handling](ERROR_HANDLING.md) patterns
- Search existing [GitHub issues](https://github.com/enbility/ship-go/issues)
- For security issues, see [SECURITY.md](../SECURITY.md) for reporting guidelines

Welcome to the SHIP protocol ecosystem! 🚀