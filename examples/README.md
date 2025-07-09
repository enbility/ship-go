# ship-go Examples

These examples demonstrate how to use ship-go to implement SHIP protocol devices. For complete SHIP protocol details, refer to the specification at https://www.eebus.org.

## Examples

### 1. [quickstart](quickstart/) - Minimal SHIP Hub
The simplest possible SHIP hub implementation that:
- Creates a SHIP-compliant certificate (Section 12)
- Starts mDNS service discovery (Section 5)
- Accepts incoming SHIP connections (Section 13.4)
- Auto-accepts pairing for demo purposes

**Run it:**
```bash
cd quickstart
go run main.go
```

### Coming Soon

- **client/** - Connect to another SHIP device
- **pairing/** - Implement proper user verification for pairing
- **production/** - Production-ready hub with error handling and persistence

## Key Concepts

Each example includes comments referencing relevant SHIP specification sections:

```go
// Start mDNS discovery (SHIP Section 5)
mdns.Start()

// Begin handshake (SHIP Section 13.4.4)
connection.StartHandshake()
```

## Security Note

These examples use auto-accept pairing for simplicity. **Never use auto-accept in production!** See [SECURITY.md](../SECURITY.md) for proper security implementation.

## Prerequisites

- Go 1.19 or later
- Linux with Avahi daemon (recommended) or any OS for Zeroconf fallback
- Network that supports mDNS (most local networks)

## Troubleshooting

If examples don't work:

1. **Check mDNS**: Ensure your network allows multicast traffic
2. **Firewall**: Port 4712 must be accessible
3. **Avahi**: On Linux, ensure `avahi-daemon` is running
4. **Logs**: Set `SHIP_LOG=debug` for detailed logging

## Learn More

- [Getting Started Guide](../docs/GETTING_STARTED.md)
- [SHIP Specification](https://www.eebus.org/media-downloads/)
- [API Documentation](https://pkg.go.dev/github.com/enbility/ship-go)