# Production SHIP Hub Example

This example demonstrates a production-ready SHIP hub implementation with comprehensive error handling, resource management, and operational monitoring.

## Features

- ✅ **Proper error handling** for all connection phases
- ✅ **Connection limits** and resource management
- ✅ **Graceful shutdown** with signal handling
- ✅ **State persistence** for trusted devices
- ✅ **Comprehensive logging** and monitoring
- ✅ **Security best practices** (no auto-accept in production)
- ✅ **Health monitoring** with metrics
- ✅ **Configuration management** via JSON file
- ✅ **Certificate persistence** and rotation warnings

## Quick Start

```bash
# Run with default configuration
go run main.go

# Run with custom configuration
go run main.go config.json
```

## Configuration

Copy and customize `config.json`:

```json
{
  "device_brand": "YourCompany",
  "device_model": "SmartEnergyGateway",
  "device_type": "EnergyManager",
  "device_serial": "SEG-2024-001",
  "organization": "Your Company Ltd",
  "country": "DE",
  "port": 4712,
  "max_connections": 20,
  "auto_accept_pairing": false,
  "trusted_devices_file": "data/trusted_devices.json",
  "certificate_file": "certs/ship.crt",
  "private_key_file": "certs/ship.key",
  "log_level": "info",
  "metrics_enabled": true
}
```

### Critical Settings

- **`auto_accept_pairing`**: MUST be `false` in production
- **`max_connections`**: Adjust based on device capacity
- **`trusted_devices_file`**: Persist paired devices between restarts
- **Certificate files**: Reuse certificates for consistent device identity

## Production Deployment

### 1. Directory Structure

```
production-hub/
├── main.go
├── config.json
├── certs/
│   ├── ship.crt
│   └── ship.key
├── data/
│   ├── trusted_devices.json
│   └── hub_state.json
└── logs/
    └── hub.log
```

### 2. System Service

Create systemd service `/etc/systemd/system/ship-hub.service`:

```ini
[Unit]
Description=SHIP Hub Service
After=network.target

[Service]
Type=simple
User=shipuser
Group=shipuser
WorkingDirectory=/opt/ship-hub
ExecStart=/opt/ship-hub/ship-hub /opt/ship-hub/config.json
Restart=always
RestartSec=10
StandardOutput=journal
StandardError=journal

[Install]
WantedBy=multi-user.target
```

### 3. Security

```bash
# Create dedicated user
sudo useradd -r -s /bin/false shipuser

# Set permissions
sudo chown -R shipuser:shipuser /opt/ship-hub
sudo chmod 600 /opt/ship-hub/certs/ship.key
sudo chmod 644 /opt/ship-hub/certs/ship.crt

# Firewall
sudo ufw allow 4712/tcp
```

### 4. Monitoring

The hub provides built-in health monitoring:

```bash
# Check service status
sudo systemctl status ship-hub

# View logs
sudo journalctl -u ship-hub -f

# Monitor metrics (logged every 5 minutes)
sudo journalctl -u ship-hub | grep "📊 Metrics"
```

## Device Pairing Process

### 1. Discovery

When a new device appears on the network:

```
📡 Discovered 1 devices
  📱 a1b2c3d4e5f6...: Viessmann Vitocaldens (192.168.1.100)
    🔒 Unknown device
```

### 2. Pairing Request

When the device attempts to connect:

```
🔒 Trust decision requested for device: a1b2c3d4e5f6...

🔒 Device Pairing Request
SKI: a1b2c3d4e5f6...
Brand: Viessmann
Model: Vitocaldens
Type: HeatPump

Do you want to trust this device? (yes/no): yes
```

### 3. Successful Pairing

```
✅ User approved device: a1b2c3d4e5f6...
✅ Added trusted device: a1b2c3d4e5f6... (Viessmann Vitocaldens)
✅ Device connected: a1b2c3d4e5f6...
```

### 4. Persistence

Trusted devices are saved to `trusted_devices.json`:

```json
{
  "a1b2c3d4e5f6...": {
    "ski": "a1b2c3d4e5f6...",
    "brand": "Viessmann",
    "model": "Vitocaldens",
    "device_type": "HeatPump",
    "paired_at": "2024-01-09T10:30:00Z",
    "last_connection": "2024-01-09T10:30:00Z",
    "connection_count": 1
  }
}
```

## Error Handling

### Connection Failures

```
❌ Device a1b2c3d4e5f6... error: connection timeout
[10:30:15] 🔄 a1b2c3d4e5f6...: ConnectionStateError
📱 Device disconnected: a1b2c3d4e5f6... (connected for 45s)
```

### Resource Limits

```
⚠️  Approaching connection limit: 18/20
```

### Short-lived Connections

```
⚠️  Short-lived connection detected for a1b2c3d4e5f6...
```

## Health Monitoring

### Automatic Health Checks

Every minute, the hub performs health checks:

```
🏥 Health Check: uptime=2h15m, connections=5, total=12, failed=2, goroutines=15
```

### Metrics Reporting

Every 5 minutes (if enabled):

```
📊 Metrics Report:
  Uptime: 2h15m30s
  Connections: active=5, total=12, failed=2
  Handshakes: count=12
  Trusted devices: 8
  Error counts:
    connection_failed: 2
    trust_rejected: 1
```

## Operational Commands

### View Trusted Devices

```bash
cat data/trusted_devices.json | jq '.'
```

### Reset Trusted Devices

```bash
# Stop service
sudo systemctl stop ship-hub

# Remove trusted devices
rm data/trusted_devices.json

# Start service
sudo systemctl start ship-hub
```

### Certificate Rotation

```bash
# Stop service
sudo systemctl stop ship-hub

# Backup old certificate
cp certs/ship.crt certs/ship.crt.backup

# Remove certificate (will be regenerated)
rm certs/ship.crt certs/ship.key

# Start service (generates new certificate)
sudo systemctl start ship-hub
```

## Troubleshooting

### No Devices Discovered

```bash
# Check mDNS
sudo systemctl status avahi-daemon
avahi-browse -r _ship._tcp

# Check network interfaces
ip addr show
```

### Connection Issues

```bash
# Check port
sudo netstat -tulpn | grep 4712

# Check firewall
sudo ufw status

# Monitor connections
sudo tcpdump -i any port 4712
```

### High Resource Usage

```bash
# Check goroutines in health logs
sudo journalctl -u ship-hub | grep "goroutines="

# Monitor memory
ps aux | grep ship-hub
```

## Integration with SPINE

To integrate with SPINE protocol:

1. **Implement SPINE message handler** in `SetupRemoteDevice()`
2. **Add SPINE device models** to your application
3. **Handle SPINE messages** through the connection writer interface

Example:

```go
func (r *ProductionHubReader) SetupRemoteDevice(
    ski string,
    writer api.ShipConnectionDataWriterInterface,
) api.ShipConnectionDataReaderInterface {
    // Create SPINE device handler
    spineHandler := spine.NewDeviceHandler(ski, writer)
    
    // Configure device features based on trusted device info
    if device, exists := r.trustedDevices[ski]; exists {
        spineHandler.ConfigureForDeviceType(device.DeviceType)
    }
    
    return spineHandler
}
```

## Security Considerations

1. **Never enable auto-accept** in production
2. **Protect private keys** with proper file permissions
3. **Monitor for unusual patterns** (frequent disconnections, unknown devices)
4. **Implement proper user authentication** for pairing decisions
5. **Use network segmentation** to isolate SHIP devices
6. **Regularly audit trusted devices** and remove unused ones

## Performance Tuning

### Connection Limits

```json
{
  "max_connections": 50  // Increase for powerful devices
}
```

### Network Interfaces

```json
{
  "network_interfaces": ["eth0"]  // Limit to specific interface
}
```

### Resource Monitoring

Monitor the health check logs for:
- High goroutine counts (>100)
- Low connection success rates (<80%)
- Memory usage trends
- Connection duration patterns

This production example provides a solid foundation for deploying ship-go in real-world environments with proper operational practices.