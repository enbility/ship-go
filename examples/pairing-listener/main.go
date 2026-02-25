// Package main demonstrates a SHIP pairing listener that generates QR codes for device pairing.
// This example shows how to set up a listener that waits for pairing requests using a shared secret.
//
// Usage: go run main.go [--cert cert.pem --key key.pem] --secret <32-hex-chars>
// Example: go run main.go --secret 1234567890abcdef1234567890abcdef
// With files: go run main.go --cert cert.pem --key key.pem --secret 1234567890abcdef1234567890abcdef
package main

import (
	"crypto/ecdsa"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"flag"
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
	"github.com/enbility/ship-go/logging"
	"github.com/enbility/ship-go/mdns"
)

// DebugLogger implements logging.LoggingInterface to show debug output
type DebugLogger struct{}

func (d *DebugLogger) Trace(args ...interface{}) {
	d.print("TRACE", args...)
}

func (d *DebugLogger) Tracef(format string, args ...interface{}) {
	d.printFormat("TRACE", format, args...)
}

func (d *DebugLogger) Debug(args ...interface{}) {
	d.print("DEBUG", args...)
}

func (d *DebugLogger) Debugf(format string, args ...interface{}) {
	d.printFormat("DEBUG", format, args...)
}

func (d *DebugLogger) Info(args ...interface{}) {
	d.print("INFO", args...)
}

func (d *DebugLogger) Infof(format string, args ...interface{}) {
	d.printFormat("INFO", format, args...)
}

func (d *DebugLogger) Error(args ...interface{}) {
	d.print("ERROR", args...)
}

func (d *DebugLogger) Errorf(format string, args ...interface{}) {
	d.printFormat("ERROR", format, args...)
}

func (d *DebugLogger) currentTimestamp() string {
	return time.Now().Format("2006-01-02 15:04:05")
}

func (d *DebugLogger) print(msgType string, args ...interface{}) {
	value := fmt.Sprintln(args...)
	fmt.Printf("%s %s %s", d.currentTimestamp(), msgType, value)
}

func (d *DebugLogger) printFormat(msgType, format string, args ...interface{}) {
	value := fmt.Sprintf(format, args...)
	fmt.Println(d.currentTimestamp(), msgType, value)
}

/* Unified Persistence Implementation */

// UnifiedPersistence handles both trust data and ring buffer persistence in a single file.
// This provides a complete solution for SHIP Pairing Service persistence requirements.
type UnifiedPersistence struct {
	filename       string
	trustedDevices map[string]api.ServiceIdentity // SKI -> ServiceIdentity
	ringEntries    []api.DigestEntry
	nextIndex      int
	mutex          sync.RWMutex
}

// TrustedDeviceEntry represents a persisted trusted device with metadata
type TrustedDeviceEntry struct {
	api.ServiceIdentity
}

// PersistenceData represents the complete structure saved to disk
type PersistenceData struct {
	TrustedDevices []TrustedDeviceEntry `json:"trustedDevices"`
	RingBuffer     RingBufferData       `json:"ringBuffer"`
}

// RingBufferData represents the ring buffer state
type RingBufferData struct {
	Entries   []api.DigestEntry `json:"entries"`
	NextIndex int               `json:"nextIndex"`
}

// NewUnifiedPersistence creates a new unified persistence manager
func NewUnifiedPersistence(filename string) (*UnifiedPersistence, error) {
	if filename == "" {
		return nil, fmt.Errorf("persistence filename cannot be empty")
	}

	p := &UnifiedPersistence{
		filename:       filename,
		trustedDevices: make(map[string]api.ServiceIdentity),
		ringEntries:    nil, // Start empty - library will provide state via SaveRingBuffer()
		nextIndex:      0,
	}

	// Load existing data if file exists
	if err := p.loadFromFile(); err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("failed to load persistence data: %w", err)
	}

	return p, nil
}

// Trust data methods
func (p *UnifiedPersistence) SaveTrustedDevice(identity api.ServiceIdentity) error {
	p.mutex.Lock()
	defer p.mutex.Unlock()

	p.trustedDevices[identity.SKI] = identity
	return p.saveToFile()
}

func (p *UnifiedPersistence) RemoveTrustedDevice(ski string) error {
	p.mutex.Lock()
	defer p.mutex.Unlock()

	delete(p.trustedDevices, ski)
	return p.saveToFile()
}

func (p *UnifiedPersistence) GetTrustedDevices() []api.ServiceIdentity {
	p.mutex.RLock()
	defer p.mutex.RUnlock()

	devices := make([]api.ServiceIdentity, 0, len(p.trustedDevices))
	for _, device := range p.trustedDevices {
		devices = append(devices, device)
	}
	return devices
}

// RingBufferPersistence interface implementation
func (p *UnifiedPersistence) LoadRingBuffer() ([]api.DigestEntry, int, error) {
	p.mutex.RLock()
	defer p.mutex.RUnlock()

	// If no previous ring buffer data exists, return empty buffer
	if len(p.ringEntries) == 0 {
		// Return empty 100-entry buffer as per SHIP protocol requirement
		return make([]api.DigestEntry, 100), 0, nil
	}

	// Return copies to avoid data races
	entries := make([]api.DigestEntry, len(p.ringEntries))
	copy(entries, p.ringEntries)
	return entries, p.nextIndex, nil
}

func (p *UnifiedPersistence) SaveRingBuffer(entries []api.DigestEntry, nextIndex int) error {
	p.mutex.Lock()
	defer p.mutex.Unlock()

	p.ringEntries = make([]api.DigestEntry, len(entries))
	copy(p.ringEntries, entries)
	p.nextIndex = nextIndex
	return p.saveToFile()
}

// File operations
func (p *UnifiedPersistence) loadFromFile() error {
	data, err := os.ReadFile(p.filename)
	if err != nil {
		return err
	}

	var persistenceData PersistenceData
	if err := json.Unmarshal(data, &persistenceData); err != nil {
		return fmt.Errorf("failed to parse persistence file: %w", err)
	}

	// Load trusted devices
	p.trustedDevices = make(map[string]api.ServiceIdentity)
	for _, entry := range persistenceData.TrustedDevices {
		p.trustedDevices[entry.SKI] = entry.ServiceIdentity
	}

	// Load complete ring buffer as saved by library
	if len(persistenceData.RingBuffer.Entries) > 0 {
		p.ringEntries = make([]api.DigestEntry, len(persistenceData.RingBuffer.Entries))
		copy(p.ringEntries, persistenceData.RingBuffer.Entries)
		p.nextIndex = persistenceData.RingBuffer.NextIndex
	} else {
		// No previous ring buffer data - start empty
		p.ringEntries = nil
		p.nextIndex = 0
	}

	return nil
}

func (p *UnifiedPersistence) saveToFile() error {
	// Create trusted devices entries with metadata
	trustedEntries := make([]TrustedDeviceEntry, 0, len(p.trustedDevices))
	for _, identity := range p.trustedDevices {
		entry := TrustedDeviceEntry{
			ServiceIdentity: identity,
		}
		trustedEntries = append(trustedEntries, entry)
	}

	// Save complete ring buffer as provided by library
	// Note: Ring buffer contains 100 entries (mostly empty) as per SHIP protocol
	// The library manages ring buffer algorithm - we just store what it provides
	persistenceData := PersistenceData{
		TrustedDevices: trustedEntries,
		RingBuffer: RingBufferData{
			Entries:   p.ringEntries, // Complete buffer from library
			NextIndex: p.nextIndex,
		},
	}

	// Marshal to JSON with pretty printing
	data, err := json.MarshalIndent(persistenceData, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal persistence data: %w", err)
	}

	// Ensure directory exists
	if err := os.MkdirAll(filepath.Dir(p.filename), 0700); err != nil {
		return fmt.Errorf("failed to create persistence directory: %w", err)
	}

	// Atomic write pattern
	tempFile := p.filename + ".tmp"
	if err := os.WriteFile(tempFile, data, 0600); err != nil {
		return fmt.Errorf("failed to write persistence file: %w", err)
	}

	return os.Rename(tempFile, p.filename)
}

/* Example Ring Buffer Persistence Implementation */

// ExampleRingBufferPersistence implements RingBufferPersistence for demonstration.
// This shows how to implement simple persistence for the SHIP pairing ring buffer.
//
// IMPORTANT: This is an in-memory implementation for demonstration only!
// Real applications MUST persist to disk/database to comply with SHIP specification
// requirement: "devA SHALL store 'digest-ring-buffer' and 'next' persistently"
//
// The new approach separates concerns:
// - Library handles ring buffer algorithm (RingBufferHistoryProvider)
// - Application handles storage (RingBufferPersistence interface)
type ExampleRingBufferPersistence struct {
	filename string // Just storage location - NO ring buffer logic!
}

// NewExampleRingBufferPersistence creates storage handler for demonstration (not persistent!)
func NewExampleRingBufferPersistence() *ExampleRingBufferPersistence {
	return &ExampleRingBufferPersistence{
		filename: "pairing_history.json", // Storage location
		// TODO: Real applications should use actual database/file path
	}
}

// LoadRingBuffer loads ring buffer state from storage (implements RingBufferPersistence)
// Called once during Hub initialization - library manages all ring buffer logic
func (r *ExampleRingBufferPersistence) LoadRingBuffer() ([]api.DigestEntry, int, error) {
	fmt.Printf("   📂 Loading ring buffer from %s...\n", r.filename)

	// For demonstration: return empty state (no previous storage)
	// Real applications should load from persistent storage:
	//
	//   data, err := os.ReadFile(r.filename)
	//   if os.IsNotExist(err) {
	//       // No previous data - return empty buffer (library will initialize)
	//       return make([]api.DigestEntry, 100), 0, nil
	//   }
	//   if err != nil {
	//       return nil, 0, fmt.Errorf("failed to read ring buffer: %w", err)
	//   }
	//
	//   var state struct {
	//       Entries   []api.DigestEntry `json:"entries"`
	//       NextIndex int              `json:"nextIndex"`
	//   }
	//   if err := json.Unmarshal(data, &state); err != nil {
	//       return nil, 0, fmt.Errorf("failed to parse ring buffer: %w", err)
	//   }
	//
	//   return state.Entries, state.NextIndex, nil

	// Demo: return empty buffer (library will manage the ring buffer logic)
	fmt.Printf("   📂 No previous data, library will create fresh ring buffer\n")
	return make([]api.DigestEntry, 100), 0, nil
}

// SaveRingBuffer saves ring buffer state to storage (implements RingBufferPersistence)
// Called by library after each successful pairing - library provides ALL the data
func (r *ExampleRingBufferPersistence) SaveRingBuffer(entries []api.DigestEntry, nextIndex int) error {
	fmt.Printf("   💾 Library requests save: %d entries, nextIndex=%d to %s\n", len(entries), nextIndex, r.filename)

	// For demonstration: just log what the library is providing
	// Real applications should save the library's data to persistent storage:
	//
	//   state := struct {
	//       Entries   []api.DigestEntry `json:"entries"`
	//       NextIndex int              `json:"nextIndex"`
	//   }{
	//       Entries:   entries,    // Complete ring buffer from library
	//       NextIndex: nextIndex,  // Current position from library
	//   }
	//
	//   data, err := json.Marshal(state)
	//   if err != nil {
	//       return fmt.Errorf("failed to marshal ring buffer: %w", err)
	//   }
	//
	//   // Atomic write pattern for safety
	//   tempFile := r.filename + ".tmp"
	//   if err := os.WriteFile(tempFile, data, 0600); err != nil {
	//       return fmt.Errorf("failed to write ring buffer: %w", err)
	//   }
	//
	//   return os.Rename(tempFile, r.filename)

	fmt.Printf("   💾 Demo: Would save library's ring buffer state to %s\n", r.filename)
	return nil
}

/* PairingHubReader - handles pairing events and device connections */

// PairingHubReader implements api.HubReaderInterface and api.PairingServiceReaderInterface
// to handle SHIP events during pairing
type PairingHubReader struct {
	hub              api.HubInterface
	pairingCompleted bool
	pairingError     error
	pairedDeviceSKI  string
	persistence      *UnifiedPersistence // Optional persistence for trust data
}

// SetupRemoteService provides the SPINE layer interface for message handling
func (p *PairingHubReader) SetupRemoteService(
	identity api.ServiceIdentity,
	writeI api.ShipConnectionDataWriterInterface,
) api.ShipConnectionDataReaderInterface {
	// we would setup the SPINE layer in here

	return nil
}

// VisibleRemoteMdnsServicesUpdated is called when mDNS discovers or loses devices
func (p *PairingHubReader) VisibleRemoteMdnsServicesUpdated(entries []api.RemoteMdnsService) {
	// we could show the visible mDNS entries
}

// ServiceUpdated is called when service information is updated
func (p *PairingHubReader) ServiceUpdated(identity api.ServiceIdentity) {
	fmt.Printf("📋 Device %s updated - SHIP ID: %s\n", identity.SKI, identity.ShipID)
}

// ServicePairingDetailUpdate provides pairing process updates
func (p *PairingHubReader) ServicePairingDetailUpdate(identity api.ServiceIdentity, detail *api.ConnectionStateDetail) {
	ski := identity.SKI
	state := detail.State()
	timestamp := time.Now().Format("15:04:05")

	switch state {
	case api.ConnectionStateReceivedPairingRequest:
		fmt.Printf("[%s] 🤝 Received pairing request from %s\n", timestamp, ski)

	case api.ConnectionStateInProgress:
		fmt.Printf("[%s] 🔄 Connection in progress with %s\n", timestamp, ski)

	case api.ConnectionStateCompleted:
		fmt.Printf("[%s] ✅ Connection completed with %s\n", timestamp, ski)

	case api.ConnectionStateRemoteDeniedTrust:
		fmt.Printf("[%s] 🚫 Device %s rejected our pairing request\n", timestamp, ski)
	}
}

// We can't manually trust a service, so this has to return false
func (p *PairingHubReader) AllowWaitingForTrust(identity api.ServiceIdentity) bool {
	return false
}

// RemoteServiceConnected is called when a new service connects
func (p *PairingHubReader) RemoteServiceConnected(identity api.ServiceIdentity) {
	ski := identity.SKI
	fmt.Printf("✅ Device connected: %s\n", ski)

	// Check if this is the device we just paired with
	if ski == p.pairedDeviceSKI {
		fmt.Printf("\n🎉 *** PAIRING AND CONNECTION SUCCESSFUL! ***\n")
		fmt.Printf("   Device has been paired and connected successfully\n")
		fmt.Printf("   Ready for secure SHIP/SPINE communication\n\n")
		p.pairingCompleted = true
	}

	fmt.Printf("✅ Device connected and ready: %s\n", ski)
}

// RemoteServiceDisconnected is called when a service disconnects
func (p *PairingHubReader) RemoteServiceDisconnected(identity api.ServiceIdentity) {
	fmt.Printf("👋 Device disconnected: %s\n", identity.SKI)
}

/* api.PairingServiceReaderInterface implementation */

/* ServiceDetails-based methods */

// ServiceAutoTrusted is called when device is automatically trusted via pairing service
func (p *PairingHubReader) ServiceAutoTrusted(identity api.ServiceIdentity) {
	fmt.Printf("\n🔐 *** TRUST ESTABLISHED! ***\n")
	fmt.Printf("   Device %s has been automatically trusted via SHIP Pairing Service\n", identity.SKI)
	if identity.ShipID != "" {
		fmt.Printf("   SHIP ID: %s\n", identity.ShipID)
	}
	if identity.Fingerprint != "" {
		fmt.Printf("   Certificate Fingerprint: %s\n", identity.Fingerprint)
	}
	fmt.Printf("   ⏳ Waiting for paired device to establish connection...\n")

	// Persist trust data if persistence is enabled
	if p.persistence != nil {
		if err := p.persistence.SaveTrustedDevice(identity); err != nil {
			fmt.Printf("   ⚠️  Failed to save trust data: %v\n", err)
		} else {
			fmt.Printf("   💾 Trust data saved to persistent storage\n")
		}
	}

	fmt.Printf("   Trust established - device can now connect when ready\n\n")
	p.pairedDeviceSKI = identity.SKI
}

// ServiceAutoTrustRemoved is called when device trust is removed via replacement logic
func (p *PairingHubReader) ServiceAutoTrustRemoved(identity api.ServiceIdentity, reason string) {
	fmt.Printf("\n🔒 *** TRUST REMOVED! ***\n")
	fmt.Printf("   Device %s has been automatically removed from trusted via SHIP Pairing Service Replacement Logic: %s\n", identity.SKI, reason)
	if identity.ShipID != "" {
		fmt.Printf("   SHIP ID: %s\n", identity.ShipID)
	}
	if identity.Fingerprint != "" {
		fmt.Printf("   Certificate Fingerprint: %s\n", identity.Fingerprint)
	}

	// Remove from persistent storage if persistence is enabled
	if p.persistence != nil {
		if err := p.persistence.RemoveTrustedDevice(identity.SKI); err != nil {
			fmt.Printf("   ⚠️  Failed to remove trust data: %v\n", err)
		} else {
			fmt.Printf("   🗑️  Trust removed from persistent storage\n")
		}
	}

	fmt.Printf("   Device must be re-paired to regain trust\n\n")
	p.pairingCompleted = false
}

// ServiceAutoTrustFailed is called when SHIP pairing fails for a service
func (p *PairingHubReader) ServiceAutoTrustFailed(identity api.ServiceIdentity, reason error) {
	fmt.Printf("\n❌ Pairing failed for device %s: %v\n", identity.SKI, reason)
	if identity.Fingerprint != "" {
		fmt.Printf("   Certificate Fingerprint: %s\n", identity.Fingerprint)
	}
	if identity.ShipID != "" {
		fmt.Printf("   SHIP ID: %s\n", identity.ShipID)
	}
	p.pairingError = reason
}

// LoadAndRegisterTrustedDevices loads previously trusted devices from persistence
// and registers them with the hub for automatic reconnection
func (p *PairingHubReader) LoadAndRegisterTrustedDevices() error {
	if p.persistence == nil {
		return nil // No persistence configured
	}

	devices := p.persistence.GetTrustedDevices()
	if len(devices) == 0 {
		fmt.Printf("📋 No previously trusted devices found\n")
		return nil
	}

	fmt.Printf("📋 Loading %d previously trusted devices from storage:\n", len(devices))

	for _, identity := range devices {
		// Register with hub - hub expects ServiceDetails for registration
		p.hub.RegisterRemoteService(identity)

		fmt.Printf("   ✅ Restored: %s", identity.SKI)
		if identity.ShipID != "" {
			fmt.Printf(" (SHIP ID: %s)", identity.ShipID)
		}
		if identity.PairingType == api.PairingTypeAddCu {
			fmt.Printf(" [AddCu device]")
		}
		fmt.Printf("\n")
	}

	fmt.Printf("📋 All trusted devices restored and registered with hub\n\n")
	return nil
}

// parseSecret parses and validates a hex-encoded secret
func parseSecret(secretHex string) (api.PairingSecret, error) {
	// Remove any whitespace and convert to lowercase
	secretHex = strings.TrimSpace(strings.ToLower(secretHex))

	// Validate hex format
	if len(secretHex) != 32 {
		return nil, fmt.Errorf("secret must be exactly 32 hex characters (16 bytes), got %d characters", len(secretHex))
	}

	// Decode hex string
	secretBytes, err := hex.DecodeString(secretHex)
	if err != nil {
		return nil, fmt.Errorf("invalid hex string: %w", err)
	}

	if len(secretBytes) != 16 {
		return nil, fmt.Errorf("secret must be exactly 16 bytes, got %d bytes", len(secretBytes))
	}

	return api.PairingSecret(secretBytes), nil
}

// loadCertificate loads certificate and key from files
func loadCertificate(certFile, keyFile string) (tls.Certificate, string, error) {
	fmt.Printf("Loading certificate from %s and key from %s...\n", certFile, keyFile)

	certificate, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return tls.Certificate{}, "", fmt.Errorf("failed to load certificate: %w", err)
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

	return certificate, ski, nil
}

// createCertificate creates a new SHIP-compliant certificate
func createCertificate() (tls.Certificate, string, error) {
	fmt.Println("Creating new certificate...")

	// Create certificate with EEBUS/SHIP required fields
	certificate, err := cert.CreateCertificate(
		"PairingListener",         // OrganizationalUnit
		"ship-go PairingListener", // Organization
		"DE",                      // Country
		"PairingListenerDemo",     // CommonName
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

	return certificate, ski, nil
}

// outputCertificateAndKey outputs certificate and key in text format
func outputCertificateAndKey(certificate tls.Certificate) {
	fmt.Println("\nCertificate and Key (text format):")
	fmt.Println("═══════════════════════════════════")

	// Output certificate
	certPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: certificate.Certificate[0],
	})
	fmt.Println("Certificate:")
	fmt.Print(string(certPEM))

	// Output private key
	if rsaKey, ok := certificate.PrivateKey.(*rsa.PrivateKey); ok {
		keyBytes := x509.MarshalPKCS1PrivateKey(rsaKey)
		keyPEM := pem.EncodeToMemory(&pem.Block{
			Type:  "RSA PRIVATE KEY",
			Bytes: keyBytes,
		})
		fmt.Println("Private Key:")
		fmt.Print(string(keyPEM))
	} else if ecKey, ok := certificate.PrivateKey.(*ecdsa.PrivateKey); ok {
		keyBytes, err := x509.MarshalECPrivateKey(ecKey)
		if err == nil {
			keyPEM := pem.EncodeToMemory(&pem.Block{
				Type:  "EC PRIVATE KEY",
				Bytes: keyBytes,
			})
			fmt.Println("Private Key:")
			fmt.Print(string(keyPEM))
		} else {
			fmt.Println("Private Key: [Cannot output - failed to marshal EC key]")
		}
	} else {
		fmt.Println("Private Key: [Cannot output - unsupported key type]")
	}
	fmt.Println()
}

func main() {
	// Enable debug logging to trace mDNS issues
	logging.SetLogging(&DebugLogger{})

	fmt.Println("SHIP Pairing Listener with QR Code")
	fmt.Println("=====================================")
	fmt.Println("This example demonstrates the simplified SHIP pairing listener API.")
	fmt.Println("The hub automatically listens for pairing requests using the shared secret.")
	fmt.Println("Debug logging is ENABLED - you will see detailed trace output.")
	fmt.Println()

	// Parse command line flags
	var secretFlag = flag.String("secret", "", "32-character hex string (16 bytes) for pairing secret")
	var certFile = flag.String("cert", "", "Path to certificate file (PEM format)")
	var keyFile = flag.String("key", "", "Path to private key file (PEM format)")
	var persistFile = flag.String("persist", "", "Path to persistence file for trust data and ring buffer (optional)")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: %s [--cert cert.pem --key key.pem] [--persist persist.json] --secret <32-hex-chars>\n", os.Args[0])                //nolint:gosec // G705: example binary, stderr output with controlled args
		fmt.Fprintf(os.Stderr, "\nExample:\n")
		fmt.Fprintf(os.Stderr, "  %s --secret 1234567890abcdef1234567890abcdef\n", os.Args[0])                                                     //nolint:gosec // G705: example binary, stderr output with controlled args
		fmt.Fprintf(os.Stderr, "  %s --cert cert.pem --key key.pem --secret 1234567890abcdef1234567890abcdef\n", os.Args[0])                        //nolint:gosec // G705: example binary, stderr output with controlled args
		fmt.Fprintf(os.Stderr, "  %s --persist ./devices.json --secret 1234567890abcdef1234567890abcdef\n", os.Args[0]) //nolint:gosec // G705: example binary, stderr output with controlled args
		fmt.Fprintf(os.Stderr, "\nOptions:\n")
		flag.PrintDefaults()
	}
	flag.Parse()

	// Validate flags
	if *secretFlag == "" {
		fmt.Fprintf(os.Stderr, "Error: --secret flag is required\n\n")
		flag.Usage()
		os.Exit(1)
	}

	// Validate cert and key flags
	if (*certFile != "" && *keyFile == "") || (*certFile == "" && *keyFile != "") {
		fmt.Fprintf(os.Stderr, "Error: Both --cert and --key must be provided together\n\n")
		flag.Usage()
		os.Exit(1)
	}

	// Parse and validate the secret
	secret, err := parseSecret(*secretFlag)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n\n", err)
		fmt.Fprintf(os.Stderr, "The secret must be a 32-character hexadecimal string (16 bytes).\n")
		fmt.Fprintf(os.Stderr, "Example: 1234567890abcdef1234567890abcdef\n")
		os.Exit(1)
	}

	fmt.Printf("Using pairing secret: %s\n", hex.EncodeToString(secret))

	// Step 1: Create or load certificate for this device
	var certificate tls.Certificate
	var ski string

	if *certFile != "" && *keyFile != "" {
		certificate, ski, err = loadCertificate(*certFile, *keyFile)
		if err != nil {
			log.Fatal("Certificate loading error:", err)
		}
	} else {
		certificate, ski, err = createCertificate()
		if err != nil {
			log.Fatal("Certificate creation error:", err)
		}
		// Output certificate and key in text format
		outputCertificateAndKey(certificate)
	}
	fmt.Printf("Device SKI (identifier): %s\n", ski)

	// Step 2: Create service details for mDNS announcement
	// Use a simple, consistent SHIP ID (in production, this could be generated from device serial/MAC/etc)
	shipID := "listener-" + ski[:12] // Use first 12 chars of SKI for uniqueness
	serviceDetails := api.NewServiceDetails(ski, "", "")
	serviceDetails.SetShipID(shipID) // Set the SHIP ID for QR code generation

	// Step 3: Create mDNS manager for device discovery
	deviceCategories := []api.DeviceCategoryType{} // Empty for this example
	interfaces := []string{}                       // Empty = use all interfaces

	// Force Zeroconf provider to avoid Avahi issues (for debugging)
	// Change to mdns.MdnsProviderSelectionAll for production
	providerSelection := mdns.MdnsProviderSelectionGoZeroConfOnly

	port := 4712 // SHIP standard port

	mdnsManager := mdns.NewMDNS(
		ski,                    // Device SKI
		"ship-go",              // Device brand
		"PairingListener",      // Device model
		"PairingDemo",          // Device type
		"PAIR-LISTEN-001",      // Device serial
		deviceCategories,       // Device categories
		shipID,                 // SHIP identifier (same as in serviceDetails)
		"SHIP-PairingListener", // Service name
		port,                   // Port
		interfaces,             // Network interfaces (empty = all)
		providerSelection,      // Provider selection (forced Zeroconf for debugging)
	)

	// Step 4: Create the hub with optional persistence
	hubReader := &PairingHubReader{}

	// Setup persistence if --persist flag was provided
	var ringBufferPersistence api.RingBufferPersistence
	if *persistFile != "" {
		fmt.Printf("🗄️  Setting up persistence file: %s\n", *persistFile)

		// Create unified persistence for both trust data and ring buffer
		unifiedPersistence, err := NewUnifiedPersistence(*persistFile)
		if err != nil {
			log.Fatalf("Failed to create persistence: %v", err)
		}

		hubReader.persistence = unifiedPersistence
		ringBufferPersistence = unifiedPersistence // Same instance handles ring buffer

		fmt.Printf("💾 Unified persistence enabled for trust data and ring buffer\n")
	} else {
		// No persistence - use example ring buffer (in-memory only)
		fmt.Printf("📝 No persistence file specified - using in-memory storage only\n")
		fmt.Printf("   Note: Trust data will be lost when application restarts\n")
		ringBufferPersistence = NewExampleRingBufferPersistence()
	}

	// Create pairing configuration for listener
	pairingConfig := api.NewPairingConfig(api.PairingModeListener, secret)

	h, err := hub.NewHub(hubReader, mdnsManager, port, certificate, serviceDetails, pairingConfig, ringBufferPersistence)
	if err != nil {
		log.Fatal("Failed to create hub:", err)
	}

	// Store hub reference so callbacks can use it
	hubReader.hub = h

	// Load and register previously trusted devices if persistence is enabled
	if err := hubReader.LoadAndRegisterTrustedDevices(); err != nil {
		fmt.Printf("⚠️  Warning: Failed to load trusted devices: %v\n", err)
	}

	// Step 5: Hub automatically creates pairing service and starts listening
	// With PairingModeListener, the hub automatically:
	// - Starts listening for pairing announcements indefinitely
	// - Validates incoming HMAC digests using the shared secret
	// - Accepts pairing requests that pass validation
	// No additional setup needed!

	// Step 6: Generate QR code for pairing
	qrData, err := h.GeneratePairingQR(secret)
	if err != nil {
		log.Fatal("Failed to generate QR code:", err)
	}

	// Step 7: Display QR code
	fmt.Println("\n📱 QR Code for Pairing:")
	fmt.Println("═══════════════════════")
	fmt.Printf("QR Data: %s\n", qrData)
	fmt.Println()
	fmt.Println("Instructions:")
	fmt.Println("1. Use a QR code generator to create a visual QR code from the data above")
	fmt.Println("2. Scan the QR code with a SHIP-compatible device")
	fmt.Println("3. The device will connect and pair automatically using the shared secret")
	fmt.Println()

	// Step 9: Start the hub (begins mDNS announcement and WebSocket server)
	if err := h.Start(); err != nil {
		log.Fatal("Failed to start hub:", err)
	}

	fmt.Println("⏳ Waiting for device to scan QR code and initiate pairing...")
	fmt.Println("Press Ctrl+C to stop...")

	// Wait for shutdown signal or pairing completion
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	// Monitor for pairing completion
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	pairingCompletedMsgShown := false

	for {
		select {
		case <-sigChan:
			fmt.Println("\n🛑 Received shutdown signal...")
			goto shutdown

		case <-ticker.C:
			// Check pairing status
			if hubReader.pairingCompleted && !pairingCompletedMsgShown {
				fmt.Println("\n🎉 Pairing completed successfully!")
				fmt.Println("The device has been paired and is ready for communication.")
				pairingCompletedMsgShown = true
			}

			if !hubReader.pairingCompleted {
				pairingCompletedMsgShown = false
			}

			if hubReader.pairingError != nil {
				fmt.Printf("\n❌ Pairing failed: %v\n", hubReader.pairingError)
			}
		}
	}

shutdown:
	fmt.Println("\n🔄 Shutting down hub...")

	// Shutdown hub
	h.Shutdown()

	fmt.Println("👋 Pairing listener stopped")

	if hubReader.pairingCompleted {
		os.Exit(0)
	}
	os.Exit(1)
}
