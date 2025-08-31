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
	"encoding/pem"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
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
}

// SetupRemoteDevice provides the SPINE layer interface for message handling
func (p *PairingHubReader) SetupRemoteDevice(
	ski string,
	writeI api.ShipConnectionDataWriterInterface,
) api.ShipConnectionDataReaderInterface {
	// we would setup the SPINE layer in here

	return nil
}

// VisibleRemoteServicesUpdated is called when mDNS discovers or loses devices
func (p *PairingHubReader) VisibleRemoteServicesUpdated(entries []api.RemoteService) {
	// we could show the visible mDNS entries
}

// ServiceShipIDUpdate is called when service shipID is known
func (p *PairingHubReader) ServiceShipIDUpdate(ski, shipID string) {
	fmt.Printf("📋 Device %s has SHIP ID: %s\n", ski, shipID)
}

// ServicePairingDetailUpdate provides pairing process updates
func (p *PairingHubReader) ServicePairingDetailUpdate(ski string, detail *api.ConnectionStateDetail) {
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
func (p *PairingHubReader) AllowWaitingForTrust(ski string) bool {
	return false
}

// RemoteSKIConnected is called when a new service connects
func (p *PairingHubReader) RemoteSKIConnected(ski string) {
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

// RemoteSKIDisconnected is called when a service disconnects
func (p *PairingHubReader) RemoteSKIDisconnected(ski string) {
	fmt.Printf("👋 Device disconnected: %s\n", ski)
}

/* api.PairingServiceReaderInterface implementation */

/* ServiceDetails-based methods */

// DeviceAutoTrustedViaServiceDetails is called when device is automatically trusted via pairing service
func (p *PairingHubReader) DeviceAutoTrustedViaServiceDetails(service *api.ServiceDetails) {
	fmt.Printf("\n🔐 *** TRUST ESTABLISHED! ***\n")
	fmt.Printf("   Device %s has been automatically trusted via SHIP Pairing Service\n", service.SKI())
	if service.ShipID() != "" {
		fmt.Printf("   SHIP ID: %s\n", service.ShipID())
	}
	if service.Fingerprint() != "" {
		fmt.Printf("   Certificate Fingerprint: %s\n", service.Fingerprint())
	}
	fmt.Printf("   ⏳ Waiting for paired device to establish connection...\n")
	fmt.Printf("   Trust established - device can now connect when ready\n\n")
	p.pairedDeviceSKI = service.SKI()
}

// DeviceAutoTrustRemovedViaReplacementLogic is called when device trust is removed via replacement logic
func (p *PairingHubReader) DeviceAutoTrustRemovedViaReplacementLogic(service *api.ServiceDetails, reason string) {
	fmt.Printf("\n🔒 *** TRUST REMOVED! ***\n")
	fmt.Printf("   Device %s has been automatically removed from trusted via SHIP Pairing Service Replacement Logic: %s\n", service.SKI(), reason)
	if service.ShipID() != "" {
		fmt.Printf("   SHIP ID: %s\n", service.ShipID())
	}
	if service.Fingerprint() != "" {
		fmt.Printf("   Certificate Fingerprint: %s\n", service.Fingerprint())
	}
	fmt.Printf("   Device must be re-paired to regain trust\n\n")
	p.pairingCompleted = false
}

// PairingServiceFailedForServiceDetails is called when pairing service fails for a service
func (p *PairingHubReader) PairingServiceFailedForServiceDetails(service *api.ServiceDetails, reason error) {
	fmt.Printf("\n❌ Pairing failed for device %s: %v\n", service.SKI(), reason)
	if service.Fingerprint() != "" {
		fmt.Printf("   Certificate Fingerprint: %s\n", service.Fingerprint())
	}
	if service.ShipID() != "" {
		fmt.Printf("   SHIP ID: %s\n", service.ShipID())
	}
	p.pairingError = reason
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
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: %s [--cert cert.pem --key key.pem] --secret <32-hex-chars>\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "\nExample:\n")
		fmt.Fprintf(os.Stderr, "  %s --secret 1234567890abcdef1234567890abcdef\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "  %s --cert cert.pem --key key.pem --secret 1234567890abcdef1234567890abcdef\n", os.Args[0])
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

	// Step 4: Create the hub
	hubReader := &PairingHubReader{}
	// Create pairing configuration for listener
	pairingConfig := api.NewPairingConfig(api.PairingModeListener, secret)

	// Create example ring buffer persistence (shows implementation pattern)
	// Real applications should persist data per SHIP specification
	ringBufferPersistence := NewExampleRingBufferPersistence()

	h, err := hub.NewHub(hubReader, mdnsManager, port, certificate, serviceDetails, pairingConfig, ringBufferPersistence)
	if err != nil {
		log.Fatal("Failed to create hub:", err)
	}

	// Store hub reference so callbacks can use it
	hubReader.hub = h

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
	} else {
		os.Exit(1)
	}
}
