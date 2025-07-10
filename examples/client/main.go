// Package main demonstrates a SHIP client that connects to other SHIP devices.
// This example shows how to discover and connect to SHIP services on the network.
//
// For protocol details, see SHIP TS 1.0.1 specification at https://www.eebus.org
package main

import (
	"bufio"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/enbility/ship-go/api"
	"github.com/enbility/ship-go/cert"
	"github.com/enbility/ship-go/hub"
	"github.com/enbility/ship-go/mdns"
)

// ClientHubReader implements HubReaderInterface for client behavior
type ClientHubReader struct {
	discoveredServices map[string]api.RemoteService
	connectedDevices   map[string]time.Time
	servicesMutex      sync.RWMutex
	userInput          chan string
}

// NewClientHubReader creates a new client hub reader
func NewClientHubReader() *ClientHubReader {
	return &ClientHubReader{
		discoveredServices: make(map[string]api.RemoteService),
		connectedDevices:   make(map[string]time.Time),
		userInput:          make(chan string, 10),
	}
}

// HubReaderInterface implementation

func (c *ClientHubReader) RemoteSKIConnected(ski string) {
	c.servicesMutex.Lock()
	c.connectedDevices[ski] = time.Now()
	c.servicesMutex.Unlock()

	if service, exists := c.discoveredServices[ski]; exists {
		fmt.Printf("🎉 Connected to %s %s (SKI: %s)\n", 
			service.Brand, service.Model, ski)
	} else {
		fmt.Printf("🎉 Connected to device: %s\n", ski)
	}
	
	log.Printf("✅ Device connected: %s", ski)
}

func (c *ClientHubReader) RemoteSKIDisconnected(ski string) {
	c.servicesMutex.Lock()
	startTime, existed := c.connectedDevices[ski]
	if existed {
		delete(c.connectedDevices, ski)
	}
	c.servicesMutex.Unlock()

	if existed {
		duration := time.Since(startTime)
		fmt.Printf("👋 Disconnected from device: %s (was connected for %v)\n", ski, duration)
	} else {
		fmt.Printf("👋 Disconnected from device: %s\n", ski)
	}
	
	log.Printf("❌ Device disconnected: %s", ski)
}

func (c *ClientHubReader) SetupRemoteDevice(
	ski string,
	writer api.ShipConnectionDataWriterInterface,
) api.ShipConnectionDataReaderInterface {
	log.Printf("🔧 Setting up SPINE layer for device: %s", ski)
	
	// In a real client implementation, you would:
	// 1. Create a SPINE device model for this device
	// 2. Return a SPINE message reader/handler
	// 3. Start exchanging SPINE messages
	
	fmt.Printf("💡 SPINE layer ready for %s - you can now exchange data\n", ski)
	
	// For this example, we return nil (connection works but no data exchange)
	return nil
}

func (c *ClientHubReader) VisibleRemoteServicesUpdated(services []api.RemoteService) {
	c.servicesMutex.Lock()
	defer c.servicesMutex.Unlock()

	fmt.Printf("\n📡 Discovered %d SHIP devices:\n", len(services))
	fmt.Println("────────────────────────────────────────────────────────")
	
	for i, service := range services {
		c.discoveredServices[service.Ski] = service
		
		// Check if we're connected to this device
		_, connected := c.connectedDevices[service.Ski]
		connectionStatus := "🔒 Not connected"
		if connected {
			connectionStatus = "✅ Connected"
		}
		
		fmt.Printf("%d. %s %s (%s)\n", i+1, service.Brand, service.Model, service.Type)
		fmt.Printf("   SKI: %s\n", service.Ski)
		fmt.Printf("   Status: %s\n", connectionStatus)
		fmt.Println()
	}
	
	if len(services) > 0 {
		fmt.Println("💡 Type 'connect <number>' to connect to a device")
		fmt.Println("💡 Type 'list' to see devices again")
		fmt.Println("💡 Type 'status' to see connection status")
		fmt.Println("💡 Type 'quit' to exit")
	}
}

func (c *ClientHubReader) ServiceShipIDUpdate(ski string, shipID string) {
	log.Printf("🆔 Device %s has SHIP ID: %s", ski, shipID)
}

func (c *ClientHubReader) ServicePairingDetailUpdate(ski string, detail *api.ConnectionStateDetail) {
	log.Printf("🤝 Pairing update for %s: state=%d", ski, detail.State())
}

func (c *ClientHubReader) AllowWaitingForTrust(ski string) bool {
	c.servicesMutex.RLock()
	service, exists := c.discoveredServices[ski]
	c.servicesMutex.RUnlock()

	if exists {
		fmt.Printf("\n🔒 Device wants to connect: %s %s\n", service.Brand, service.Model)
		fmt.Printf("   SKI: %s\n", ski)
		fmt.Printf("   SKI: %s\n", ski)
	} else {
		fmt.Printf("\n🔒 Unknown device wants to connect: %s\n", ski)
	}
	
	fmt.Print("Do you want to trust this device? (yes/no): ")
	
	// Read user input
	reader := bufio.NewReader(os.Stdin)
	response, _ := reader.ReadString('\n')
	response = strings.TrimSpace(strings.ToLower(response))
	
	approved := response == "yes" || response == "y"
	
	if approved {
		fmt.Printf("✅ Approved connection to %s\n", ski)
		log.Printf("✅ User approved device: %s", ski)
	} else {
		fmt.Printf("❌ Rejected connection to %s\n", ski)
		log.Printf("❌ User rejected device: %s", ski)
	}
	
	return approved
}

func (c *ClientHubReader) ServiceConnectionStateChanged(ski string, state api.ConnectionState) {
	timestamp := time.Now().Format("15:04:05")
	log.Printf("[%s] 🔄 %s: %v", timestamp, ski, state)

	// Show user-friendly status updates
	switch state {
	case api.ConnectionStateInitiated:
		fmt.Printf("🔄 Connecting to %s...\n", ski)
	case api.ConnectionStateInProgress:
		fmt.Printf("🤝 Handshaking with %s...\n", ski)
	case api.ConnectionStateCompleted:
		fmt.Printf("✅ Successfully connected to %s!\n", ski)
	case api.ConnectionStateError:
		fmt.Printf("❌ Failed to connect to %s\n", ski)
	case api.ConnectionStateRemoteDeniedTrust:
		fmt.Printf("🚫 Device %s rejected our connection\n", ski)
	}
}

// Interactive client functions

func (c *ClientHubReader) listDevices() {
	c.servicesMutex.RLock()
	defer c.servicesMutex.RUnlock()

	if len(c.discoveredServices) == 0 {
		fmt.Println("No devices discovered yet. Make sure devices are on the network.")
		return
	}

	fmt.Printf("\n📱 Available devices (%d):\n", len(c.discoveredServices))
	fmt.Println("────────────────────────────────────────────────────────")
	
	i := 1
	for ski, service := range c.discoveredServices {
		_, connected := c.connectedDevices[ski]
		connectionStatus := "🔒 Not connected"
		if connected {
			connectionStatus = "✅ Connected"
		}
		
		fmt.Printf("%d. %s %s (%s)\n", i, service.Brand, service.Model, service.Type)
		fmt.Printf("   SKI: %s\n", ski)
		fmt.Printf("   Status: %s\n", connectionStatus)
		fmt.Println()
		i++
	}
}

func (c *ClientHubReader) showStatus() {
	c.servicesMutex.RLock()
	defer c.servicesMutex.RUnlock()

	fmt.Printf("\n📊 Connection Status:\n")
	fmt.Println("────────────────────────────────────────────────────────")
	fmt.Printf("Discovered devices: %d\n", len(c.discoveredServices))
	fmt.Printf("Connected devices: %d\n", len(c.connectedDevices))
	
	if len(c.connectedDevices) > 0 {
		fmt.Println("\nActive connections:")
		for ski, connTime := range c.connectedDevices {
			if service, exists := c.discoveredServices[ski]; exists {
				duration := time.Since(connTime)
				fmt.Printf("  ✅ %s %s (connected for %v)\n", 
					service.Brand, service.Model, duration)
			}
		}
	}
	fmt.Println()
}

func (c *ClientHubReader) getServiceByIndex(index int) (string, api.RemoteService, bool) {
	c.servicesMutex.RLock()
	defer c.servicesMutex.RUnlock()

	if index < 1 || index > len(c.discoveredServices) {
		return "", api.RemoteService{}, false
	}

	i := 1
	for ski, service := range c.discoveredServices {
		if i == index {
			return ski, service, true
		}
		i++
	}

	return "", api.RemoteService{}, false
}

func (c *ClientHubReader) startUserInterface(h *hub.Hub) {
	fmt.Println("\n🎮 Interactive Client Commands:")
	fmt.Println("  connect <number> - Connect to device by number")
	fmt.Println("  disconnect <number> - Disconnect from device")
	fmt.Println("  list - Show all discovered devices")
	fmt.Println("  status - Show connection status")
	fmt.Println("  quit - Exit the client")
	fmt.Println()

	scanner := bufio.NewScanner(os.Stdin)
	
	for {
		fmt.Print("ship-client> ")
		if !scanner.Scan() {
			break
		}

		command := strings.TrimSpace(scanner.Text())
		if command == "" {
			continue
		}

		parts := strings.Fields(command)
		cmd := strings.ToLower(parts[0])

		switch cmd {
		case "quit", "exit", "q":
			fmt.Println("👋 Goodbye!")
			return

		case "list", "ls":
			c.listDevices()

		case "status", "stat":
			c.showStatus()

		case "connect", "conn":
			if len(parts) < 2 {
				fmt.Println("Usage: connect <device_number>")
				continue
			}

			var deviceIndex int
			if _, err := fmt.Sscanf(parts[1], "%d", &deviceIndex); err != nil {
				fmt.Println("Invalid device number")
				continue
			}

			ski, service, found := c.getServiceByIndex(deviceIndex)
			if !found {
				fmt.Println("Device not found. Use 'list' to see available devices.")
				continue
			}

			// Check if already connected
			if _, connected := c.connectedDevices[ski]; connected {
				fmt.Printf("Already connected to %s %s\n", service.Brand, service.Model)
				continue
			}

			fmt.Printf("🔄 Connecting to %s %s...\n", service.Brand, service.Model)
			
			// Register the service and initiate connection
			h.RegisterRemoteSKI(ski, "")

		case "disconnect", "disc":
			if len(parts) < 2 {
				fmt.Println("Usage: disconnect <device_number>")
				continue
			}

			var deviceIndex int
			if _, err := fmt.Sscanf(parts[1], "%d", &deviceIndex); err != nil {
				fmt.Println("Invalid device number")
				continue
			}

			ski, service, found := c.getServiceByIndex(deviceIndex)
			if !found {
				fmt.Println("Device not found. Use 'list' to see available devices.")
				continue
			}

			// Check if connected
			if _, connected := c.connectedDevices[ski]; !connected {
				fmt.Printf("Not connected to %s %s\n", service.Brand, service.Model)
				continue
			}

			fmt.Printf("👋 Disconnecting from %s %s...\n", service.Brand, service.Model)
			h.DisconnectSKI(ski, "user requested disconnect")

		case "help", "h":
			fmt.Println("Available commands:")
			fmt.Println("  connect <number> - Connect to device by number")
			fmt.Println("  disconnect <number> - Disconnect from device")
			fmt.Println("  list - Show all discovered devices")
			fmt.Println("  status - Show connection status")
			fmt.Println("  help - Show this help")
			fmt.Println("  quit - Exit the client")

		default:
			fmt.Printf("Unknown command: %s. Type 'help' for available commands.\n", cmd)
		}
	}
}

// Certificate creation for client
func createClientCertificate() (tls.Certificate, string, error) {
	fmt.Println("🔐 Creating client certificate...")
	
	// Generate unique certificate for this client
	hostname, _ := os.Hostname()
	commonName := fmt.Sprintf("SHIP-Client-%s", hostname)
	
	certificate, err := cert.CreateCertificate(
		"ClientUnit",          // OrganizationalUnit
		"SHIP Client",         // Organization
		"DE",                  // Country
		commonName,            // CommonName
	)
	if err != nil {
		return tls.Certificate{}, "", fmt.Errorf("failed to create certificate: %w", err)
	}

	// Extract SKI
	x509Cert, err := x509.ParseCertificate(certificate.Certificate[0])
	if err != nil {
		return tls.Certificate{}, "", fmt.Errorf("failed to parse certificate: %w", err)
	}

	ski, err := cert.SkiFromCertificate(x509Cert)
	if err != nil {
		return tls.Certificate{}, "", fmt.Errorf("failed to extract SKI: %w", err)
	}

	return certificate, ski, nil
}

func main() {
	fmt.Println("🚀 SHIP Client Example")
	fmt.Println("======================")
	fmt.Println("This client will discover and connect to SHIP devices on your network.")
	fmt.Println()

	// Create client certificate
	certificate, ski, err := createClientCertificate()
	if err != nil {
		log.Fatal("Certificate error:", err)
	}
	fmt.Printf("📜 Client SKI: %s\n", ski)

	// Create service details for this client
	serviceDetails := api.NewServiceDetails(ski)

	// Create client hub reader
	clientReader := NewClientHubReader()

	// Create mDNS manager for discovery
	deviceCategories := []api.DeviceCategoryType{} // Empty for client
	mdnsManager := mdns.NewMDNS(
		ski,                              // SKI
		"SHIP-Client",                    // Brand
		"ExampleClient",                  // Model
		"Client",                         // Type
		"CLI-001",                        // Serial
		deviceCategories,                 // Categories
		"client-ship-id",                 // SHIP ID
		"SHIP-Client",                    // Service name
		4712,                             // Port
		[]string{},                       // Interfaces (empty = all)
		mdns.MdnsProviderSelectionAll,    // Provider
	)

	// Create hub
	h := hub.NewHub(clientReader, mdnsManager, 4712, certificate, serviceDetails)

	// Set reasonable connection limit for client
	h.SetMaxConnections(5)

	// Start hub
	if err := h.Start(); err != nil {
		log.Fatal("Failed to start client hub:", err)
	}

	fmt.Printf("✅ SHIP client started on port 4712\n")
	fmt.Println("🔍 Searching for SHIP devices...")
	fmt.Println("   (This may take a few seconds)")
	fmt.Println()

	// Give some time for initial discovery
	time.Sleep(3 * time.Second)

	// Start interactive user interface in a goroutine
	go clientReader.startUserInterface(h)

	// Setup graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	// Wait for shutdown signal
	<-sigChan

	fmt.Println("\n🛑 Shutting down client...")
	h.Shutdown()
	fmt.Println("👋 Client stopped")
}