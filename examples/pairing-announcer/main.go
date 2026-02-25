// Package main demonstrates a SHIP pairing announcer that parses QR codes and announces pairing.
// This example shows how to parse SHIP pairing QR codes and initiate pairing with target devices.
//
// Usage: go run main.go [--cert cert.pem --key key.pem] "<QR-string>" [<QR-string>...]
// Single: go run main.go "SHIP;SKI:targetski;ID:target-ship-id;FPH256:1234567890ABCDEF;SPSEC:1234567890abcdef1234567890abcdef;ENDSHIP;"
// Multiple: go run main.go "SHIP;SKI:device1;..." "SHIP;SKI:device2;..." "SHIP;SKI:device3;..."
// With files: go run main.go --cert cert.pem --key key.pem "<QR-string>" [<QR-string>...]
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

// PairingHubReader implements api.HubReaderInterface and api.PairingServiceReaderInterface
// to handle SHIP events during pairing
type PairingHubReader struct {
	hub            api.HubInterface
	targetSKIs     map[string]bool // Track multiple target SKIs
	completedCount int
	totalTargets   int
	pairingError   error
	mu             sync.RWMutex // Use RWMutex for better read performance
}

// Thread-safe helper methods for PairingHubReader

// getStatus returns the current pairing status in a thread-safe manner
func (p *PairingHubReader) getStatus() (completed, total int, err error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.completedCount, p.totalTargets, p.pairingError
}

// setError sets the pairing error in a thread-safe manner
func (p *PairingHubReader) setError(ski string, err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, isTarget := p.targetSKIs[ski]; isTarget {
		p.pairingError = err
	}
}

// clearError clears the pairing error in a thread-safe manner
func (p *PairingHubReader) clearError(ski string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, isTarget := p.targetSKIs[ski]; isTarget {
		p.pairingError = nil
	}
}

// markCompleted marks a device as successfully paired in a thread-safe manner
func (p *PairingHubReader) markCompleted(ski string) (completed, total int, allComplete bool) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if _, isTarget := p.targetSKIs[ski]; isTarget && !p.targetSKIs[ski] {
		p.targetSKIs[ski] = true
		p.completedCount++
	}

	return p.completedCount, p.totalTargets, p.completedCount >= p.totalTargets
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
		fmt.Printf("[%s] 🤝 Received pairing response from %s\n", timestamp, ski)

	case api.ConnectionStateInProgress:
		fmt.Printf("[%s] 🔄 Connection in progress with %s\n", timestamp, ski)

	case api.ConnectionStateCompleted:
		fmt.Printf("[%s] ✅ Connection completed with %s\n", timestamp, ski)

		// Clear any previous pairing error since this succeeded
		p.clearError(ski)

	case api.ConnectionStateRemoteDeniedTrust:
		fmt.Printf("[%s] 🚫 Device %s rejected our pairing request\n", timestamp, ski)

		// Set pairing error for this rejection (thread-safe)
		p.setError(ski, fmt.Errorf("pairing request denied by device %s", ski))

	case api.ConnectionStateError:
		fmt.Printf("[%s] ❌ Connection error with %s: %v\n", timestamp, ski, detail.Error())

		// Set pairing error for connection failures (thread-safe)
		p.setError(ski, fmt.Errorf("connection error with %s: %w", ski, detail.Error()))
	}
}

// AllowWaitingForTrust is for manual trust only
func (p *PairingHubReader) AllowWaitingForTrust(identity api.ServiceIdentity) bool {
	return false
}

// RemoteServiceConnected is called when a new service connects
func (p *PairingHubReader) RemoteServiceConnected(identity api.ServiceIdentity) {
	ski := identity.SKI
	fmt.Printf("\n✅ Device connected: %s\n", ski)

	completed, total, allComplete := p.markCompleted(ski)

	if completed > 0 {
		fmt.Printf("🎉 Target device %s paired successfully! (%d/%d completed)\n",
			ski, completed, total)

		if allComplete {
			fmt.Printf("\n🎊 *** ALL DEVICES PAIRED SUCCESSFULLY! ***\n")
			fmt.Printf("   All %d target devices have been successfully paired and connected\n", total)
			fmt.Printf("   The devices can now communicate securely via SHIP/SPINE\n\n")
		}
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
	fmt.Printf("   Waiting for paired device to establish connection...\n")
	fmt.Printf("   Trust established - device can now connect when ready\n\n")
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

// ServiceAutoTrustRemoved is called when device trust is removed via replacement logic
func (p *PairingHubReader) ServiceAutoTrustRemoved(identity api.ServiceIdentity, reason string) {
	fmt.Printf("\n🔒 *** TRUST REMOVED! ***\n")
	fmt.Printf("   Device %s trust removed: %s\n", identity.SKI, reason)
	if identity.ShipID != "" {
		fmt.Printf("   SHIP ID: %s\n", identity.ShipID)
	}
	fmt.Printf("   Device must be re-paired to regain trust\n\n")
}

// ShipPairingData represents parsed SHIP pairing QR data
type ShipPairingData struct {
	SKI         string            // Device SKI
	ShipID      string            // Target device SHIP ID
	Brand       string            // Device brand (optional)
	Model       string            // Device model (optional)
	Serial      string            // Device serial (optional)
	Type        string            // Device type (optional)
	Categories  string            // Device categories (optional)
	Fingerprint string            // Target device certificate fingerprint (FPH256)
	Secret      api.PairingSecret // Pairing secret (SPSEC)
}

// parseShipPairingQR parses a SHIP pairing QR code and extracts pairing information
// Expected format: SHIP;SKI:xxx;ID:xxx;BRAND:xxx;TYPE:xxx;MODEL:xxx;SERIAL:xxx;CAT:xxx;FPH256:xxx;SPSEC:xxx;ENDSHIP;
func parseShipPairingQR(qrString string) (*ShipPairingData, error) {
	// Clean up the input
	qrString = strings.TrimSpace(qrString)

	// Validate SHIP format
	if !strings.HasPrefix(qrString, "SHIP;") {
		return nil, fmt.Errorf("QR string must start with 'SHIP;', got: %s", qrString)
	}

	if !strings.HasSuffix(qrString, "ENDSHIP;") {
		return nil, fmt.Errorf("QR string must end with 'ENDSHIP;', got: %s", qrString)
	}

	// Remove SHIP; prefix and ENDSHIP; suffix
	content := qrString[5 : len(qrString)-8] // Remove "SHIP;" and "ENDSHIP;"

	// Split into key-value pairs
	fields := strings.Split(content, ";")
	data := &ShipPairingData{}

	for _, field := range fields {
		if field == "" {
			continue
		}

		parts := strings.SplitN(field, ":", 2)
		if len(parts) != 2 {
			continue // Skip malformed fields
		}

		key := strings.ToUpper(strings.TrimSpace(parts[0]))
		value := strings.TrimSpace(parts[1])

		switch key {
		case "SKI":
			data.SKI = value
		case "ID":
			data.ShipID = value
		case "BRAND":
			data.Brand = value
		case "MODEL":
			data.Model = value
		case "SERIAL":
			data.Serial = value
		case "TYPE":
			data.Type = value
		case "CAT":
			data.Categories = value
		case "FPH256":
			data.Fingerprint = value
		case "SPSEC":
			// Validate and decode secret
			secret, err := parseSecret(value)
			if err != nil {
				return nil, fmt.Errorf("invalid secret: %w", err)
			}
			data.Secret = secret
		default:
			// ignore unknown keys
			continue
		}
	}

	// Validate required fields
	if data.SKI == "" {
		return nil, fmt.Errorf("missing required 'SKI' field")
	}
	if data.ShipID == "" {
		return nil, fmt.Errorf("missing required 'ID' field (SHIP ID)")
	}
	if data.Fingerprint == "" {
		return nil, fmt.Errorf("missing required 'FPH256' field (certificate fingerprint)")
	}
	if len(data.Secret) == 0 {
		return nil, fmt.Errorf("missing required 'SPSEC' field (pairing secret)")
	}

	return data, nil
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
		"PairingAnnouncer",         // OrganizationalUnit
		"ship-go PairingAnnouncer", // Organization
		"DE",                       // Country
		"PairingAnnouncerDemo",     // CommonName
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

	fmt.Println("SHIP Pairing Announcer with QR Parsing")
	fmt.Println("==========================================")
	fmt.Println("This example demonstrates the simplified SHIP pairing announcer API.")
	fmt.Println("It parses a QR code and uses hub.StartAnnouncementTo() to initiate pairing.")
	fmt.Println("Debug logging is ENABLED - you will see detailed trace output.")
	fmt.Println()

	// Parse command line flags
	var certFile = flag.String("cert", "", "Path to certificate file (PEM format)")
	var keyFile = flag.String("key", "", "Path to private key file (PEM format)")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: %s [--cert cert.pem --key key.pem] \"<QR-string>\" [\"<QR-string>\"...]\n", os.Args[0]) //nolint:gosec // G705: example binary, stderr output with controlled args
		fmt.Fprintf(os.Stderr, "\nExamples:\n")
		fmt.Fprintf(os.Stderr, "  Single: %s \"SHIP;SKI:device1;ID:id1;FPH256:fp1;SPSEC:secret1;ENDSHIP;\"\n", os.Args[0])       //nolint:gosec // G705: example binary, stderr output with controlled args
		fmt.Fprintf(os.Stderr, "  Multiple: %s \"SHIP;SKI:device1;...\" \"SHIP;SKI:device2;...\" \"SHIP;SKI:device3;...\"\n", os.Args[0]) //nolint:gosec // G705: example binary, stderr output with controlled args
		fmt.Fprintf(os.Stderr, "  With certs: %s --cert cert.pem --key key.pem \"SHIP;SKI:device1;...\" \"SHIP;SKI:device2;...\"\n", os.Args[0]) //nolint:gosec // G705: example binary, stderr output with controlled args
		fmt.Fprintf(os.Stderr, "\nThe QR string should be in SHIP pairing format:\n")
		fmt.Fprintf(os.Stderr, "  SHIP;SKI:<ski>;ID:<shipID>;FPH256:<fingerprint>;SPSEC:<secret>;ENDSHIP;\n")
		fmt.Fprintf(os.Stderr, "\nOptions:\n")
		flag.PrintDefaults()
	}
	flag.Parse()

	// Check for QR string arguments
	if flag.NArg() < 1 {
		fmt.Fprintf(os.Stderr, "Error: At least one QR string argument is required\n\n")
		flag.Usage()
		os.Exit(1)
	}

	// Validate cert and key flags
	if (*certFile != "" && *keyFile == "") || (*certFile == "" && *keyFile != "") {
		fmt.Fprintf(os.Stderr, "Error: Both --cert and --key must be provided together\n\n")
		flag.Usage()
		os.Exit(1)
	}

	qrStrings := flag.Args()
	var targets []*api.PairingTarget

	fmt.Printf("🔍 Parsing %d QR string(s)...\n", len(qrStrings))

	for i, qrString := range qrStrings {
		fmt.Printf("\n📋 [%d/%d] Processing QR code...\n", i+1, len(qrStrings))

		shipData, err := parseShipPairingQR(qrString)
		if err != nil {
			fmt.Printf("❌ [%d] Failed to parse QR: %v\n", i+1, err)
			fmt.Fprintf(os.Stderr, "\nExpected format: SHIP;SKI:xxx;ID:xxx;BRAND:xxx;TYPE:xxx;MODEL:xxx;SERIAL:xxx;CAT:xxx;FPH256:xxx;SPSEC:xxx;ENDSHIP;\n")
			continue
		}

		fmt.Printf("✅ [%d] Parsed device: %s (SHIP ID: %s)\n", i+1, shipData.SKI, shipData.ShipID)

		// Show device details
		if shipData.Brand != "" {
			fmt.Printf("  - Brand: %s\n", shipData.Brand)
		}
		if shipData.Model != "" {
			fmt.Printf("  - Model: %s\n", shipData.Model)
		}
		if shipData.Serial != "" {
			fmt.Printf("  - Serial: %s\n", shipData.Serial)
		}
		if shipData.Type != "" {
			fmt.Printf("  - Type: %s\n", shipData.Type)
		}
		if shipData.Categories != "" {
			fmt.Printf("  - Categories: %s\n", shipData.Categories)
		}
		fmt.Printf("  - Certificate Fingerprint: %s\n", shipData.Fingerprint)
		fmt.Printf("  - Pairing Secret: %s\n", hex.EncodeToString(shipData.Secret))

		target := &api.PairingTarget{
			SKI:         shipData.SKI,
			Fingerprint: shipData.Fingerprint,
			ShipID:      shipData.ShipID,
			Secret:      shipData.Secret,
		}
		targets = append(targets, target)
	}

	if len(targets) == 0 {
		fmt.Fprintf(os.Stderr, "❌ No valid QR codes found\n")
		os.Exit(1)
	}

	fmt.Printf("\n✅ Successfully parsed %d device(s)\n", len(targets))

	// Step 2: Create or load certificate for this announcer
	var certificate tls.Certificate
	var ski string

	var certErr error
	if *certFile != "" && *keyFile != "" {
		certificate, ski, certErr = loadCertificate(*certFile, *keyFile)
		if certErr != nil {
			log.Fatal("Certificate loading error:", certErr)
		}
	} else {
		certificate, ski, certErr = createCertificate()
		if certErr != nil {
			log.Fatal("Certificate creation error:", certErr)
		}
		// Output certificate and key in text format
		outputCertificateAndKey(certificate)
	}
	fmt.Printf("Local Device SKI (identifier): %s\n", ski)

	// Step 3: Create service details for mDNS announcement
	// Use a simple, consistent SHIP ID (in production, this could be generated from device serial/MAC/etc)
	shipID := "announcer-" + ski[:12] // Use first 12 chars of SKI for uniqueness
	serviceDetails := api.NewServiceDetails(ski, "", "")
	serviceDetails.SetShipID(shipID) // Set the SHIP ID for consistency

	// Step 4: Create mDNS manager for device discovery
	deviceCategories := []api.DeviceCategoryType{} // Empty for this example
	interfaces := []string{}                       // Empty = use all interfaces

	// Force Zeroconf provider to avoid Avahi issues (for debugging)
	// Change to mdns.MdnsProviderSelectionAll for production
	providerSelection := mdns.MdnsProviderSelectionGoZeroConfOnly

	port := 4718 // Using different port to avoid conflicts

	mdnsManager := mdns.NewMDNS(
		ski,                     // Device SKI
		"ship-go",               // Device brand
		"PairingAnnouncer",      // Device model
		"PairingDemo",           // Device type
		"PAIR-ANNOUNCE-001",     // Device serial
		deviceCategories,        // Device categories
		shipID,                  // SHIP identifier (same as in serviceDetails)
		"SHIP-PairingAnnouncer", // Service name
		port,                    // Port (different from listener)
		interfaces,              // Network interfaces (empty = all)
		providerSelection,       // Provider selection (forced Zeroconf for debugging)
	)

	// Step 5: Create the hub
	hubReader := &PairingHubReader{}
	// Create pairing configuration for announcer
	// Use nil for secret since announcer gets the secret from parsed QR code
	pairingConfig := api.NewPairingConfig(api.PairingModeAnnouncer, nil)

	// Announcer mode doesn't need history provider (no replay protection needed)
	h, err := hub.NewHub(hubReader, mdnsManager, port, certificate, serviceDetails, pairingConfig, nil)
	if err != nil {
		log.Fatal("Failed to create hub:", err)
	}

	// Store hub reference so callbacks can use it
	hubReader.hub = h

	// Step 6: Hub automatically creates pairing service for announcements
	// With PairingModeAnnouncer, the hub is ready to announce to specific targets
	// using the StartAnnouncementTo() method

	// Step 7: Start the hub (begins mDNS announcement and WebSocket server)
	if err := h.Start(); err != nil {
		log.Fatal("Failed to start hub:", err)
	}

	fmt.Printf("✅ SHIP hub running on port %d\n", port)

	fmt.Printf("🔐 Registering %d target device(s)...\n", len(targets))

	// Initialize hub reader for multiple targets
	hubReader.targetSKIs = make(map[string]bool)
	hubReader.totalTargets = len(targets)

	for i, target := range targets {
		fmt.Printf("  [%d] Registering %s (SHIP ID: %s)\n", i+1, target.SKI, target.ShipID)
		targetIdentity := api.NewServiceIdentity(target.SKI, target.Fingerprint, target.ShipID)
		h.RegisterRemoteService(targetIdentity)
		hubReader.targetSKIs[target.SKI] = false // not completed yet
	}

	fmt.Println("\n🎯 Pairing Status:")
	fmt.Println("  - Hub is announcing itself via mDNS")
	fmt.Printf("  - %d target devices registered and trusted\n", len(targets))
	fmt.Println("  - Ready to announce pairing to target devices")

	// Step 8: Wait a moment for hub to initialize
	fmt.Println()
	time.Sleep(1 * time.Second)

	fmt.Printf("🚀 Starting %d pairing announcements...\n", len(targets))

	for i, target := range targets {
		err := h.StartAnnouncementTo(target)
		if err != nil {
			fmt.Printf("❌ [%d] Failed to start announcement for %s: %v\n", i+1, target.SKI, err)
		} else {
			fmt.Printf("✅ [%d] Announcement started for %s\n", i+1, target.SKI)
		}
	}

	fmt.Printf("⏳ Waiting for %d target devices to respond to pairing announcements...\n", len(targets))
	fmt.Println("Press Ctrl+C to stop...")

	// Wait for shutdown signal or pairing completion
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	// Monitor for pairing completion or timeout
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-sigChan:
			fmt.Println("\n🛑 Received shutdown signal...")
			goto shutdown

		case <-ticker.C:
			// Check pairing status using thread-safe helper
			completed, total, err := hubReader.getStatus()

			if completed >= total && total > 0 {
				fmt.Printf("\n🎉 All %d devices paired successfully!\n", total)
				fmt.Println("All target devices have been paired and are ready for communication.")
				goto shutdown
			}

			if completed > 0 {
				fmt.Printf("📊 Progress: %d/%d devices paired\n", completed, total)
			}

			if err != nil {
				fmt.Printf("❌ Pairing error: %v\n", err)
			}
		}
	}

shutdown:
	fmt.Println("\n🔄 Shutting down pairing announcer...")

	// Create a channel to coordinate shutdown
	shutdownComplete := make(chan struct{})

	// Shutdown hub in a goroutine with timeout
	go func() {
		// Note: The announcer doesn't have a stop method, it runs until timeout
		// The mDNS announcement will be cleaned up when the hub shuts down

		// Shutdown hub (this will clean up mDNS and connections)
		h.Shutdown()
		close(shutdownComplete)
	}()

	// Wait for shutdown to complete with a timeout
	select {
	case <-shutdownComplete:
		fmt.Println("👋 Pairing announcer stopped gracefully")
	case <-time.After(5 * time.Second):
		fmt.Println("⚠️  Shutdown timeout - forcing exit")
	}

	completed, total, _ := hubReader.getStatus()

	if completed >= total && total > 0 {
		fmt.Printf("✅ All %d devices were successfully paired\n", total)
		os.Exit(0)
	} else if completed > 0 {
		fmt.Printf("⚠️ Partial success: %d/%d devices paired\n", completed, total)
		os.Exit(2)
	}
	fmt.Println("❌ No devices were paired")
	os.Exit(1)
}
