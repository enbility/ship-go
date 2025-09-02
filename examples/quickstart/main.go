// Package main demonstrates a minimal SHIP hub implementation.
// This example creates a basic SHIP hub that can accept connections from other SHIP devices.
//
// For protocol details, see SHIP TS 1.0.1 specification at https://www.eebus.org
package main

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/enbility/ship-go/api"
	"github.com/enbility/ship-go/cert"
	"github.com/enbility/ship-go/hub"
	"github.com/enbility/ship-go/mdns"
)

// SimpleHubReader implements api.HubReaderInterface to handle SHIP events
type SimpleHubReader struct{}

// RemoteServiceConnected is called when a remote device connects
func (s *SimpleHubReader) RemoteServiceConnected(identity api.ServiceIdentity) {
	log.Printf("✅ Device connected: %s", identity.SKI)
}

// RemoteServiceDisconnected is called when a remote device disconnects
func (s *SimpleHubReader) RemoteServiceDisconnected(identity api.ServiceIdentity) {
	log.Printf("❌ Device disconnected: %s", identity.SKI)
}

// SetupRemoteService provides the SPINE layer interface for message handling
// In a full implementation, this would return a SPINE message handler
func (s *SimpleHubReader) SetupRemoteService(
	identity api.ServiceIdentity,
	writeI api.ShipConnectionDataWriterInterface,
) api.ShipConnectionDataReaderInterface {
	log.Printf("Setting up SPINE layer for device: %s", identity.SKI)
	// In a real implementation, return a SPINE protocol handler here
	// For this example, we return nil (connection will work but no data exchange)
	return nil
}

// VisibleRemoteMdnsServicesUpdated is called when mDNS discovers or loses devices
func (s *SimpleHubReader) VisibleRemoteMdnsServicesUpdated(entries []api.RemoteMdnsService) {
	log.Printf("📡 Discovered %d remote devices", len(entries))
	for _, entry := range entries {
		log.Printf("  - SKI: %s, Brand: %s, Model: %s", entry.Ski, entry.Brand, entry.Model)
	}
}

// ServiceUpdated is called when a device's information is updated
func (s *SimpleHubReader) ServiceUpdated(identity api.ServiceIdentity) {
	log.Printf("Device %s updated - SHIP ID: %s", identity.SKI, identity.ShipID)
}

// ServicePairingDetailUpdate provides pairing process updates
func (s *SimpleHubReader) ServicePairingDetailUpdate(identity api.ServiceIdentity, detail *api.ConnectionStateDetail) {
	log.Printf("Pairing update for %s: %+v", identity.SKI, detail)
}

// AllowWaitingForTrust determines if we accept a new device's pairing request
// WARNING: Only return true for development. Production should prompt the user!
func (s *SimpleHubReader) AllowWaitingForTrust(identity api.ServiceIdentity) bool {
	log.Printf("🤝 Auto-accepting pairing from device: %s (DEV MODE)", identity.SKI)
	return true // Auto-accept for this demo - see SECURITY.md for production guidance
}

func main() {
	fmt.Println("🚀 Starting SHIP Hub Quickstart")
	fmt.Println("================================")

	// Step 1: Create a certificate for this device
	// In production, you would load this from secure storage
	certificate, ski, err := createCertificate()
	if err != nil {
		log.Fatal("Certificate error:", err)
	}
	fmt.Printf("📜 Device SKI (identifier): %s\n", ski)

	// Step 2: Create service details for mDNS announcement
	serviceDetails := api.NewServiceDetails(ski, "", "")

	// Step 3: Create mDNS manager for device discovery
	// Parameters: SKI, brand, model, type, serial, categories, shipID, serviceName, port, interfaces, provider
	deviceCategories := []api.DeviceCategoryType{} // Empty for this example
	interfaces := []string{}                       // Empty = use all interfaces
	mdnsManager := mdns.NewMDNS(
		ski,                           // Device SKI
		"ship-go",                     // Device brand
		"QuickstartHub",               // Device model
		"Generic",                     // Device type
		"DEMO-001",                    // Device serial
		deviceCategories,              // Device categories
		"quickstart-ship-id",          // SHIP identifier
		"SHIP-QuickstartHub",          // Service name
		4712,                          // Port
		interfaces,                    // Network interfaces (empty = all)
		mdns.MdnsProviderSelectionAll, // Provider selection (auto-select Avahi or Zeroconf)
	)

	// Step 4: Create the hub
	hubReader := &SimpleHubReader{}
	port := 4712 // SHIP standard port
	// No pairing configuration = no history provider needed
	h, err := hub.NewHub(hubReader, mdnsManager, port, certificate, serviceDetails, nil, nil)
	if err != nil {
		log.Fatal("Failed to create hub:", err)
	}
	// Optional: Set connection limit for small devices
	h.SetMaxConnections(5)

	// Step 5: Start the hub (begins mDNS announcement and WebSocket server)
	if err := h.Start(); err != nil {
		log.Fatal("Failed to start hub:", err)
	}

	fmt.Printf("\n✅ SHIP hub running on port %d\n", port)
	fmt.Println("\nThe hub is now:")
	fmt.Println("  - Announcing itself via mDNS")
	fmt.Println("  - Accepting SHIP connections")
	fmt.Println("  - Auto-accepting pairing requests (dev mode)")
	fmt.Println("\nPress Ctrl+C to stop...")

	// Wait for shutdown signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	<-sigChan

	fmt.Println("\n🛑 Shutting down...")
	h.Shutdown()
	fmt.Println("Goodbye!")
}

// createCertificate creates a new SHIP-compliant certificate
func createCertificate() (tls.Certificate, string, error) {
	fmt.Println("🔐 Creating new certificate...")

	// Create certificate with EEBUS/SHIP required fields
	certificate, err := cert.CreateCertificate(
		"Demo",               // OrganizationalUnit
		"ship-go Quickstart", // Organization
		"DE",                 // Country
		"QuickstartHub",      // CommonName
	)
	if err != nil {
		return tls.Certificate{}, "", fmt.Errorf("failed to create certificate: %w", err)
	}

	// Parse the certificate to extract the SKI
	x509Cert, err := x509.ParseCertificate(certificate.Certificate[0])
	if err != nil {
		return tls.Certificate{}, "", fmt.Errorf("failed to parse certificate: %w", err)
	}

	ski, err := cert.SkiFromCertificate(x509Cert)
	if err != nil {
		return tls.Certificate{}, "", fmt.Errorf("failed to get SKI: %w", err)
	}

	// Note: In production, save this certificate securely and reuse it
	fmt.Println("⚠️  Note: In production, save and reuse certificates!")

	return certificate, ski, nil
}
