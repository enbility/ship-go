// Package main demonstrates different SHIP pairing workflows and trust establishment.
// This example shows various approaches to device pairing and trust management.
//
// For protocol details, see SHIP TS 1.0.1 specification Section 13.4.4.2
package main

import (
	"bufio"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/enbility/ship-go/api"
	"github.com/enbility/ship-go/cert"
	"github.com/enbility/ship-go/hub"
	"github.com/enbility/ship-go/mdns"
)

// PairingMode represents different pairing strategies
type PairingMode int

const (
	PairingModeManual PairingMode = iota // Manual user approval for each device
	PairingModeWhitelist                 // Auto-approve whitelisted devices
	PairingModeBlacklist                 // Auto-approve except blacklisted devices
	PairingModeAutoFirst                 // Auto-approve first N devices, then manual
)

// DeviceWhitelist represents a list of trusted device identifiers
type DeviceWhitelist struct {
	TrustedDevices []TrustedDevice `json:"trusted_devices"`
	CreatedAt      time.Time       `json:"created_at"`
	UpdatedAt      time.Time       `json:"updated_at"`
}

// TrustedDevice represents a device that has been paired and trusted
type TrustedDevice struct {
	SKI            string    `json:"ski"`
	Brand          string    `json:"brand"`
	Model          string    `json:"model"`
	DeviceType     string    `json:"device_type"`
	PairedAt       time.Time `json:"paired_at"`
	LastSeen       time.Time `json:"last_seen"`
	Notes          string    `json:"notes,omitempty"`
	AutoApproved   bool      `json:"auto_approved"`
}

// PairingHubReader implements various pairing strategies
type PairingHubReader struct {
	mode              PairingMode
	whitelist         *DeviceWhitelist
	whitelistFile     string
	discoveredDevices map[string]api.RemoteService
	pendingApprovals  map[string]chan bool
	autoApproveCount  int
	maxAutoApprove    int
	mutex             sync.RWMutex
}

// NewPairingHubReader creates a new pairing hub reader
func NewPairingHubReader(mode PairingMode, whitelistFile string) *PairingHubReader {
	reader := &PairingHubReader{
		mode:              mode,
		whitelistFile:     whitelistFile,
		discoveredDevices: make(map[string]api.RemoteService),
		pendingApprovals:  make(map[string]chan bool),
		autoApproveCount:  0,
		maxAutoApprove:    3, // Auto-approve first 3 devices
	}

	// Load whitelist if file exists
	if whitelistFile != "" {
		reader.loadWhitelist()
	}

	return reader
}

// HubReaderInterface implementation

func (p *PairingHubReader) RemoteSKIConnected(ski string) {
	p.mutex.Lock()
	defer p.mutex.Unlock()

	// Update last seen time for trusted devices
	for i, device := range p.whitelist.TrustedDevices {
		if device.SKI == ski {
			p.whitelist.TrustedDevices[i].LastSeen = time.Now()
			p.saveWhitelist()
			break
		}
	}

	if service, exists := p.discoveredDevices[ski]; exists {
		fmt.Printf("✅ Connected to %s %s (SKI: %s)\n", 
			service.Brand, service.Model, ski)
		
		// Show connection success message based on how it was approved
		if p.isInWhitelist(ski) {
			fmt.Printf("   📋 Device was pre-approved (whitelist)\n")
		} else {
			fmt.Printf("   🤝 Device was manually approved\n")
		}
	} else {
		fmt.Printf("✅ Connected to device: %s\n", ski)
	}
	
	log.Printf("Device connected: %s", ski)
}

func (p *PairingHubReader) RemoteSKIDisconnected(ski string) {
	if service, exists := p.discoveredDevices[ski]; exists {
		fmt.Printf("👋 Disconnected from %s %s (SKI: %s)\n", 
			service.Brand, service.Model, ski)
	} else {
		fmt.Printf("👋 Disconnected from device: %s\n", ski)
	}
	
	log.Printf("Device disconnected: %s", ski)
}

func (p *PairingHubReader) SetupRemoteDevice(
	ski string,
	writer api.ShipConnectionDataWriterInterface,
) api.ShipConnectionDataReaderInterface {
	log.Printf("Setting up SPINE layer for device: %s", ski)
	
	// In a real implementation, you would:
	// 1. Create a SPINE device handler
	// 2. Configure it based on the device type
	// 3. Return the handler for message processing
	
	return nil
}

func (p *PairingHubReader) VisibleRemoteServicesUpdated(services []api.RemoteService) {
	p.mutex.Lock()
	defer p.mutex.Unlock()

	fmt.Printf("\n📡 Device Discovery Update (%d devices):\n", len(services))
	fmt.Println("────────────────────────────────────────────────────────")
	
	for _, service := range services {
		p.discoveredDevices[service.Ski] = service
		
		trustStatus := "🔒 Unknown"
		if p.isInWhitelist(service.Ski) {
			trustStatus = "✅ Trusted"
		}
		
		fmt.Printf("📱 %s %s (%s)\n", service.Brand, service.Model, service.Type)
		fmt.Printf("   SKI: %s\n", service.Ski)
		fmt.Printf("   Status: %s\n", trustStatus)
		fmt.Println()
	}
}

func (p *PairingHubReader) ServiceShipIDUpdate(ski string, shipID string) {
	log.Printf("Device %s has SHIP ID: %s", ski, shipID)
}

func (p *PairingHubReader) ServicePairingDetailUpdate(ski string, detail *api.ConnectionStateDetail) {
	log.Printf("Pairing detail update for %s: state=%d", ski, detail.State())
}

func (p *PairingHubReader) AllowWaitingForTrust(ski string) bool {
	p.mutex.Lock()
	defer p.mutex.Unlock()

	service, exists := p.discoveredDevices[ski]
	if !exists {
		service = api.RemoteService{
			Ski:   ski,
			Brand: "Unknown",
			Model: "Unknown",
			Type: "Unknown",
		}
	}

	fmt.Printf("\n🔒 Pairing Request from %s %s\n", service.Brand, service.Model)
	fmt.Printf("   SKI: %s\n", ski)
	fmt.Printf("   Mode: %s\n", p.getPairingModeString())

	// Apply pairing strategy
	switch p.mode {
	case PairingModeManual:
		return p.handleManualPairing(ski, service)
		
	case PairingModeWhitelist:
		return p.handleWhitelistPairing(ski, service)
		
	case PairingModeBlacklist:
		return p.handleBlacklistPairing(ski, service)
		
	case PairingModeAutoFirst:
		return p.handleAutoFirstPairing(ski, service)
		
	default:
		return p.handleManualPairing(ski, service)
	}
}

func (p *PairingHubReader) ServiceConnectionStateChanged(ski string, state api.ConnectionState) {
	timestamp := time.Now().Format("15:04:05")
	log.Printf("[%s] Connection state changed %s: %v", timestamp, ski, state)

	// Show user-friendly status updates
	switch state {
	case api.ConnectionStateReceivedPairingRequest:
		fmt.Printf("🤝 Received pairing request from %s\n", ski)
		
	case api.ConnectionStateInProgress:
		fmt.Printf("🔄 Pairing in progress with %s\n", ski)
		
	case api.ConnectionStateCompleted:
		fmt.Printf("✅ Pairing completed with %s\n", ski)
		
	case api.ConnectionStateError:
		fmt.Printf("❌ Pairing failed with %s\n", ski)
		
	case api.ConnectionStateRemoteDeniedTrust:
		fmt.Printf("🚫 Device %s rejected our pairing request\n", ski)
	}
}

// Pairing strategy implementations

func (p *PairingHubReader) handleManualPairing(ski string, service api.RemoteService) bool {
	fmt.Printf("📝 Manual Pairing Mode - User approval required\n")
	
	return p.promptUserForApproval(ski, service)
}

func (p *PairingHubReader) handleWhitelistPairing(ski string, service api.RemoteService) bool {
	if p.isInWhitelist(ski) {
		fmt.Printf("✅ Auto-approved (device is whitelisted)\n")
		return true
	}
	
	fmt.Printf("❌ Auto-rejected (device not in whitelist)\n")
	fmt.Printf("   Add to whitelist with: add-whitelist %s\n", ski)
	return false
}

func (p *PairingHubReader) handleBlacklistPairing(ski string, service api.RemoteService) bool {
	// In a real implementation, you'd have a blacklist check here
	// For this example, we'll just approve everything
	fmt.Printf("✅ Auto-approved (device not blacklisted)\n")
	
	// Still add to trusted devices for tracking
	p.addToWhitelist(ski, service, true)
	return true
}

func (p *PairingHubReader) handleAutoFirstPairing(ski string, service api.RemoteService) bool {
	if p.autoApproveCount < p.maxAutoApprove {
		p.autoApproveCount++
		fmt.Printf("✅ Auto-approved (%d/%d auto-approvals used)\n", 
			p.autoApproveCount, p.maxAutoApprove)
		
		// Add to trusted devices
		p.addToWhitelist(ski, service, true)
		return true
	}
	
	fmt.Printf("📝 Auto-approve limit reached - manual approval required\n")
	return p.promptUserForApproval(ski, service)
}

func (p *PairingHubReader) promptUserForApproval(ski string, service api.RemoteService) bool {
	fmt.Printf("Do you want to pair with this device? (yes/no/whitelist): ")
	
	reader := bufio.NewReader(os.Stdin)
	response, _ := reader.ReadString('\n')
	response = strings.TrimSpace(strings.ToLower(response))
	
	switch response {
	case "yes", "y":
		fmt.Printf("✅ Approved pairing with %s\n", ski)
		p.addToWhitelist(ski, service, false)
		return true
		
	case "whitelist", "w":
		fmt.Printf("✅ Approved and added to whitelist: %s\n", ski)
		p.addToWhitelist(ski, service, false)
		return true
		
	default:
		fmt.Printf("❌ Rejected pairing with %s\n", ski)
		return false
	}
}

// Whitelist management

func (p *PairingHubReader) loadWhitelist() {
	if p.whitelistFile == "" {
		p.whitelist = &DeviceWhitelist{
			TrustedDevices: []TrustedDevice{},
			CreatedAt:      time.Now(),
			UpdatedAt:      time.Now(),
		}
		return
	}

	data, err := os.ReadFile(p.whitelistFile)
	if err != nil {
		if os.IsNotExist(err) {
			p.whitelist = &DeviceWhitelist{
				TrustedDevices: []TrustedDevice{},
				CreatedAt:      time.Now(),
				UpdatedAt:      time.Now(),
			}
			return
		}
		log.Printf("Warning: Could not load whitelist: %v", err)
		return
	}

	if err := json.Unmarshal(data, &p.whitelist); err != nil {
		log.Printf("Warning: Could not parse whitelist: %v", err)
		return
	}

	fmt.Printf("📋 Loaded whitelist with %d trusted devices\n", len(p.whitelist.TrustedDevices))
}

func (p *PairingHubReader) saveWhitelist() {
	if p.whitelistFile == "" || p.whitelist == nil {
		return
	}

	p.whitelist.UpdatedAt = time.Now()
	
	data, err := json.MarshalIndent(p.whitelist, "", "  ")
	if err != nil {
		log.Printf("Warning: Could not marshal whitelist: %v", err)
		return
	}

	// Ensure directory exists
	if err := os.MkdirAll(filepath.Dir(p.whitelistFile), 0755); err != nil {
		log.Printf("Warning: Could not create directory: %v", err)
		return
	}

	if err := os.WriteFile(p.whitelistFile, data, 0600); err != nil {
		log.Printf("Warning: Could not save whitelist: %v", err)
		return
	}
}

func (p *PairingHubReader) isInWhitelist(ski string) bool {
	if p.whitelist == nil {
		return false
	}

	for _, device := range p.whitelist.TrustedDevices {
		if device.SKI == ski {
			return true
		}
	}
	return false
}

func (p *PairingHubReader) addToWhitelist(ski string, service api.RemoteService, autoApproved bool) {
	if p.whitelist == nil {
		p.whitelist = &DeviceWhitelist{
			TrustedDevices: []TrustedDevice{},
			CreatedAt:      time.Now(),
			UpdatedAt:      time.Now(),
		}
	}

	// Check if already exists
	for i, device := range p.whitelist.TrustedDevices {
		if device.SKI == ski {
			// Update existing device
			p.whitelist.TrustedDevices[i].LastSeen = time.Now()
			p.saveWhitelist()
			return
		}
	}

	// Add new device
	device := TrustedDevice{
		SKI:          ski,
		Brand:        service.Brand,
		Model:        service.Model,
		DeviceType:   service.Type,
		PairedAt:     time.Now(),
		LastSeen:     time.Now(),
		AutoApproved: autoApproved,
	}

	p.whitelist.TrustedDevices = append(p.whitelist.TrustedDevices, device)
	p.saveWhitelist()
}

func (p *PairingHubReader) getPairingModeString() string {
	switch p.mode {
	case PairingModeManual:
		return "Manual approval required"
	case PairingModeWhitelist:
		return "Auto-approve whitelisted devices only"
	case PairingModeBlacklist:
		return "Auto-approve except blacklisted devices"
	case PairingModeAutoFirst:
		return fmt.Sprintf("Auto-approve first %d devices", p.maxAutoApprove)
	default:
		return "Unknown"
	}
}

func (p *PairingHubReader) showWhitelist() {
	p.mutex.RLock()
	defer p.mutex.RUnlock()

	if p.whitelist == nil || len(p.whitelist.TrustedDevices) == 0 {
		fmt.Println("📋 No trusted devices in whitelist")
		return
	}

	fmt.Printf("📋 Trusted Devices (%d):\n", len(p.whitelist.TrustedDevices))
	fmt.Println("────────────────────────────────────────────────────────")
	
	for i, device := range p.whitelist.TrustedDevices {
		fmt.Printf("%d. %s %s (%s)\n", i+1, device.Brand, device.Model, device.DeviceType)
		fmt.Printf("   SKI: %s\n", device.SKI)
		fmt.Printf("   Paired: %s\n", device.PairedAt.Format("2006-01-02 15:04:05"))
		fmt.Printf("   Last seen: %s\n", device.LastSeen.Format("2006-01-02 15:04:05"))
		if device.AutoApproved {
			fmt.Printf("   Auto-approved: Yes\n")
		}
		if device.Notes != "" {
			fmt.Printf("   Notes: %s\n", device.Notes)
		}
		fmt.Println()
	}
}

// Interactive commands

func (p *PairingHubReader) handleInteractiveCommands() {
	fmt.Println("\n🎮 Pairing Commands:")
	fmt.Println("  show-whitelist - Show trusted devices")
	fmt.Println("  set-mode <mode> - Set pairing mode (manual/whitelist/blacklist/auto-first)")
	fmt.Println("  help - Show this help")
	fmt.Println("  quit - Exit")
	fmt.Println()

	scanner := bufio.NewScanner(os.Stdin)
	
	for {
		fmt.Print("pairing> ")
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

		case "show-whitelist", "list":
			p.showWhitelist()

		case "set-mode":
			if len(parts) < 2 {
				fmt.Println("Usage: set-mode <manual|whitelist|blacklist|auto-first>")
				continue
			}
			
			switch strings.ToLower(parts[1]) {
			case "manual":
				p.mode = PairingModeManual
				fmt.Println("✅ Pairing mode set to: Manual")
			case "whitelist":
				p.mode = PairingModeWhitelist
				fmt.Println("✅ Pairing mode set to: Whitelist only")
			case "blacklist":
				p.mode = PairingModeBlacklist
				fmt.Println("✅ Pairing mode set to: Blacklist (auto-approve others)")
			case "auto-first":
				p.mode = PairingModeAutoFirst
				fmt.Println("✅ Pairing mode set to: Auto-approve first devices")
			default:
				fmt.Println("Invalid mode. Use: manual, whitelist, blacklist, or auto-first")
			}

		case "help", "h":
			fmt.Println("Available commands:")
			fmt.Println("  show-whitelist - Show trusted devices")
			fmt.Println("  set-mode <mode> - Set pairing mode")
			fmt.Println("  help - Show this help")
			fmt.Println("  quit - Exit")

		default:
			fmt.Printf("Unknown command: %s. Type 'help' for available commands.\n", cmd)
		}
	}
}

func main() {
	fmt.Println("🤝 SHIP Pairing Example")
	fmt.Println("=======================")
	fmt.Println("This example demonstrates different SHIP pairing strategies.")
	fmt.Println()
	
	// Ensure tls import is used
	_ = tls.Certificate{}

	// Parse command line arguments for mode
	mode := PairingModeManual
	if len(os.Args) > 1 {
		switch strings.ToLower(os.Args[1]) {
		case "manual":
			mode = PairingModeManual
		case "whitelist":
			mode = PairingModeWhitelist
		case "blacklist":
			mode = PairingModeBlacklist
		case "auto-first":
			mode = PairingModeAutoFirst
		default:
			fmt.Printf("Unknown mode: %s. Using manual mode.\n", os.Args[1])
		}
	}

	// Create certificate
	fmt.Println("🔐 Creating certificate...")
	certificate, err := cert.CreateCertificate(
		"PairingExample",    // OrganizationalUnit
		"SHIP Examples",     // Organization
		"DE",                // Country
		"PairingDemo",       // CommonName
	)
	if err != nil {
		log.Fatal("Certificate error:", err)
	}

	// Extract SKI
	x509Cert, err := x509.ParseCertificate(certificate.Certificate[0])
	if err != nil {
		log.Fatal("Failed to parse certificate:", err)
	}

	ski, err := cert.SkiFromCertificate(x509Cert)
	if err != nil {
		log.Fatal("Failed to extract SKI:", err)
	}
	fmt.Printf("📜 Hub SKI: %s\n", ski)

	// Create service details
	serviceDetails := api.NewServiceDetails(ski)

	// Create pairing hub reader
	whitelistFile := "pairing_whitelist.json"
	pairingReader := NewPairingHubReader(mode, whitelistFile)

	fmt.Printf("🔒 Pairing mode: %s\n", pairingReader.getPairingModeString())

	// Create mDNS manager
	deviceCategories := []api.DeviceCategoryType{}
	mdnsManager := mdns.NewMDNS(
		ski,
		"PairingExample",
		"PairingDemo",
		"Demo",
		"PAIR-001",
		deviceCategories,
		"pairing-demo-id",
		"SHIP-PairingDemo",
		4712,
		[]string{},
		mdns.MdnsProviderSelectionAll,
	)

	// Create hub
	h := hub.NewHub(pairingReader, mdnsManager, 4712, certificate, serviceDetails)

	// Start hub
	if err := h.Start(); err != nil {
		log.Fatal("Failed to start hub:", err)
	}

	fmt.Printf("✅ Pairing hub started on port 4712\n")
	fmt.Println("🔍 Listening for device pairing requests...")
	fmt.Println()

	// Start interactive command interface
	go pairingReader.handleInteractiveCommands()

	// Setup graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	// Wait for shutdown signal
	<-sigChan

	fmt.Println("\n🛑 Shutting down pairing hub...")
	h.Shutdown()
	fmt.Println("👋 Pairing hub stopped")
}