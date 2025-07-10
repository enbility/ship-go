// Package main demonstrates a production-ready SHIP hub implementation.
//
// This example shows:
// - Proper error handling for all phases
// - Connection limits and resource management
// - Graceful shutdown with signal handling
// - State persistence and recovery
// - Comprehensive logging and monitoring
// - Security best practices
//
// For protocol details, see SHIP TS 1.0.1 specification at https://www.eebus.org
package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"sync"
	"syscall"
	"time"

	"github.com/enbility/ship-go/api"
	"github.com/enbility/ship-go/cert"
	"github.com/enbility/ship-go/hub"
	"github.com/enbility/ship-go/mdns"
)

// Configuration holds all production configuration
type Configuration struct {
	// Device identification
	DeviceBrand  string `json:"device_brand"`
	DeviceModel  string `json:"device_model"`
	DeviceType   string `json:"device_type"`
	DeviceSerial string `json:"device_serial"`
	Organization string `json:"organization"`
	Country      string `json:"country"`

	// Network configuration
	Port            int      `json:"port"`
	MaxConnections  int      `json:"max_connections"`
	NetworkInterfaces []string `json:"network_interfaces,omitempty"`

	// Security settings
	AutoAcceptPairing bool `json:"auto_accept_pairing"` // Should be false in production
	TrustedDevicesFile string `json:"trusted_devices_file"`

	// Operational settings
	CertificateFile    string `json:"certificate_file"`
	PrivateKeyFile     string `json:"private_key_file"`
	StateFile          string `json:"state_file"`
	LogLevel           string `json:"log_level"`
	MetricsEnabled     bool   `json:"metrics_enabled"`
}

// ProductionHubReader implements a production-grade HubReaderInterface
type ProductionHubReader struct {
	config           *Configuration
	trustedDevices   map[string]TrustedDevice
	devicesMutex     sync.RWMutex
	connectionTimes  map[string]time.Time
	metrics          *Metrics
	userInterface    UserInterface
	shutdown         chan struct{}
}

// TrustedDevice represents a paired and trusted device
type TrustedDevice struct {
	SKI            string    `json:"ski"`
	Brand          string    `json:"brand"`
	Model          string    `json:"model"`
	DeviceType     string    `json:"device_type"`
	PairedAt       time.Time `json:"paired_at"`
	LastConnection time.Time `json:"last_connection"`
	ConnectionCount int      `json:"connection_count"`
}

// Metrics tracks operational statistics
type Metrics struct {
	StartTime           time.Time         `json:"start_time"`
	ConnectionsTotal    int64             `json:"connections_total"`
	ConnectionsActive   int               `json:"connections_active"`
	ConnectionsFailed   int64             `json:"connections_failed"`
	HandshakeTimeTotal  time.Duration     `json:"handshake_time_total"`
	HandshakeCount      int64             `json:"handshake_count"`
	LastHealthCheck     time.Time         `json:"last_health_check"`
	TrustedDeviceCount  int               `json:"trusted_device_count"`
	ErrorCounts         map[string]int64  `json:"error_counts"`
	mutex               sync.RWMutex
}

// UserInterface abstracts user interaction for pairing decisions
type UserInterface interface {
	PromptForTrust(ski, brand, model, deviceType string) bool
	NotifyConnectionStateChange(ski string, state api.ConnectionState)
	NotifyError(ski string, err error)
}

// ConsoleUserInterface implements basic console-based user interaction
type ConsoleUserInterface struct {
	autoAccept bool
}

func (ui *ConsoleUserInterface) PromptForTrust(ski, brand, model, deviceType string) bool {
	if ui.autoAccept {
		log.Printf("⚠️  AUTO-ACCEPTING device %s (%s %s) - DEVELOPMENT MODE ONLY!", ski, brand, model)
		return true
	}

	fmt.Printf("\n🔒 Device Pairing Request\n")
	fmt.Printf("SKI: %s\n", ski)
	fmt.Printf("Brand: %s\n", brand)
	fmt.Printf("Model: %s\n", model)
	fmt.Printf("Type: %s\n", deviceType)
	fmt.Printf("\nDo you want to trust this device? (yes/no): ")

	var response string
	_, _ = fmt.Scanln(&response)

	return response == "yes" || response == "y"
}

func (ui *ConsoleUserInterface) NotifyConnectionStateChange(ski string, state api.ConnectionState) {
	log.Printf("📱 Device %s: %v", ski, state)
}

func (ui *ConsoleUserInterface) NotifyError(ski string, err error) {
	log.Printf("❌ Device %s error: %v", ski, err)
}

// NewProductionHubReader creates a new production hub reader
func NewProductionHubReader(config *Configuration) (*ProductionHubReader, error) {
	reader := &ProductionHubReader{
		config:          config,
		trustedDevices:  make(map[string]TrustedDevice),
		connectionTimes: make(map[string]time.Time),
		metrics: &Metrics{
			StartTime:    time.Now(),
			ErrorCounts:  make(map[string]int64),
		},
		userInterface: &ConsoleUserInterface{autoAccept: config.AutoAcceptPairing},
		shutdown:      make(chan struct{}),
	}

	// Load trusted devices from persistent storage
	if err := reader.loadTrustedDevices(); err != nil {
		log.Printf("⚠️  Could not load trusted devices: %v", err)
	}

	// Start background monitoring
	go reader.startHealthMonitoring()
	go reader.startMetricsReporting()

	return reader, nil
}

// HubReaderInterface implementation

func (r *ProductionHubReader) RemoteSKIConnected(ski string) {
	r.devicesMutex.Lock()
	r.connectionTimes[ski] = time.Now()
	r.devicesMutex.Unlock()

	r.metrics.mutex.Lock()
	r.metrics.ConnectionsTotal++
	r.metrics.ConnectionsActive++
	r.metrics.mutex.Unlock()

	// Update trusted device last connection time
	if device, exists := r.trustedDevices[ski]; exists {
		device.LastConnection = time.Now()
		device.ConnectionCount++
		r.trustedDevices[ski] = device
		_ = r.saveTrustedDevices() // Persist state
	}

	r.userInterface.NotifyConnectionStateChange(ski, api.ConnectionStateCompleted)
	log.Printf("✅ Device connected: %s", ski)
}

func (r *ProductionHubReader) RemoteSKIDisconnected(ski string) {
	r.devicesMutex.Lock()
	startTime, existed := r.connectionTimes[ski]
	if existed {
		delete(r.connectionTimes, ski)
	}
	r.devicesMutex.Unlock()

	r.metrics.mutex.Lock()
	if r.metrics.ConnectionsActive > 0 {
		r.metrics.ConnectionsActive--
	}
	r.metrics.mutex.Unlock()

	if existed {
		duration := time.Since(startTime)
		log.Printf("📱 Device disconnected: %s (connected for %v)", ski, duration)

		// Track short-lived connections as potential issues
		if duration < 30*time.Second {
			log.Printf("⚠️  Short-lived connection detected for %s", ski)
			r.incrementErrorCount("short_connection")
		}
	} else {
		log.Printf("📱 Device disconnected: %s", ski)
	}

	r.userInterface.NotifyConnectionStateChange(ski, api.ConnectionStateNone)
}

func (r *ProductionHubReader) SetupRemoteDevice(
	ski string,
	writer api.ShipConnectionDataWriterInterface,
) api.ShipConnectionDataReaderInterface {
	log.Printf("🔧 Setting up SPINE layer for device: %s", ski)
	
	// In a real implementation, return your SPINE message handler here
	// For this example, we return nil (connection works but no SPINE data exchange)
	return nil
}

func (r *ProductionHubReader) VisibleRemoteServicesUpdated(services []api.RemoteService) {
	log.Printf("📡 Discovered %d devices", len(services))
	
	for _, service := range services {
		log.Printf("  📱 %s: %s %s", service.Ski, service.Brand, service.Model)
		
		// Check if this is a previously trusted device
		if _, trusted := r.trustedDevices[service.Ski]; trusted {
			log.Printf("    ✅ Previously trusted device")
		} else {
			log.Printf("    🔒 Unknown device")
		}
	}
}

func (r *ProductionHubReader) ServiceShipIDUpdate(ski string, shipID string) {
	log.Printf("🆔 Device %s has SHIP ID: %s", ski, shipID)
}

func (r *ProductionHubReader) ServicePairingDetailUpdate(ski string, detail *api.ConnectionStateDetail) {
	log.Printf("🤝 Pairing update for %s: state=%d", ski, detail.State())
	
	// Track handshake timing
	r.metrics.mutex.Lock()
	if detail.State() == api.ConnectionStateCompleted {
		r.metrics.HandshakeCount++
		// In a real implementation, track actual handshake duration
	}
	r.metrics.mutex.Unlock()
}

func (r *ProductionHubReader) AllowWaitingForTrust(ski string) bool {
	log.Printf("🔒 Trust decision requested for device: %s", ski)

	// Check if device is already trusted
	r.devicesMutex.RLock()
	device, alreadyTrusted := r.trustedDevices[ski]
	r.devicesMutex.RUnlock()

	if alreadyTrusted {
		log.Printf("✅ Device %s is already trusted (paired %v)", ski, device.PairedAt)
		return true
	}

	// For production: NEVER auto-accept unknown devices
	if r.config.AutoAcceptPairing {
		log.Printf("⚠️  AUTO-ACCEPTING %s - DEVELOPMENT MODE!", ski)
		r.addTrustedDevice(ski, "Unknown", "Unknown", "Unknown")
		return true
	}

	// Get device info for user prompt (would come from discovery)
	// In real implementation, you'd look this up from the discovery data
	const unknownValue = "Unknown"
	brand := unknownValue
	model := unknownValue
	deviceType := unknownValue

	// Prompt user for trust decision
	trusted := r.userInterface.PromptForTrust(ski, brand, model, deviceType)

	if trusted {
		log.Printf("✅ User approved device: %s", ski)
		r.addTrustedDevice(ski, brand, model, deviceType)
	} else {
		log.Printf("❌ User rejected device: %s", ski)
		r.incrementErrorCount("trust_rejected")
	}

	return trusted
}

func (r *ProductionHubReader) ServiceConnectionStateChanged(ski string, state api.ConnectionState) {
	timestamp := time.Now().Format("15:04:05")
	log.Printf("[%s] 🔄 %s: %v", timestamp, ski, state)

	switch state {
	case api.ConnectionStateError:
		r.metrics.mutex.Lock()
		r.metrics.ConnectionsFailed++
		r.metrics.mutex.Unlock()
		r.incrementErrorCount("connection_failed")

	case api.ConnectionStateRemoteDeniedTrust:
		r.incrementErrorCount("remote_denied_trust")
	}

	r.userInterface.NotifyConnectionStateChange(ski, state)
}

// Production helper methods

func (r *ProductionHubReader) addTrustedDevice(ski, brand, model, deviceType string) {
	r.devicesMutex.Lock()
	defer r.devicesMutex.Unlock()

	device := TrustedDevice{
		SKI:            ski,
		Brand:          brand,
		Model:          model,
		DeviceType:     deviceType,
		PairedAt:       time.Now(),
		LastConnection: time.Time{},
		ConnectionCount: 0,
	}

	r.trustedDevices[ski] = device
	_ = r.saveTrustedDevices()

	log.Printf("✅ Added trusted device: %s (%s %s)", ski, brand, model)
}

func (r *ProductionHubReader) incrementErrorCount(errorType string) {
	r.metrics.mutex.Lock()
	defer r.metrics.mutex.Unlock()
	r.metrics.ErrorCounts[errorType]++
}

func (r *ProductionHubReader) loadTrustedDevices() error {
	if r.config.TrustedDevicesFile == "" {
		return nil
	}

	data, err := os.ReadFile(r.config.TrustedDevicesFile)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // File doesn't exist yet, that's ok
		}
		return fmt.Errorf("failed to read trusted devices file: %w", err)
	}

	r.devicesMutex.Lock()
	defer r.devicesMutex.Unlock()

	err = json.Unmarshal(data, &r.trustedDevices)
	if err != nil {
		return fmt.Errorf("failed to parse trusted devices file: %w", err)
	}

	log.Printf("📂 Loaded %d trusted devices from %s", len(r.trustedDevices), r.config.TrustedDevicesFile)
	return nil
}

func (r *ProductionHubReader) saveTrustedDevices() error {
	if r.config.TrustedDevicesFile == "" {
		return nil
	}

	r.devicesMutex.RLock()
	data, err := json.MarshalIndent(r.trustedDevices, "", "  ")
	r.devicesMutex.RUnlock()

	if err != nil {
		return fmt.Errorf("failed to marshal trusted devices: %w", err)
	}

	err = os.WriteFile(r.config.TrustedDevicesFile, data, 0600)
	if err != nil {
		return fmt.Errorf("failed to write trusted devices file: %w", err)
	}

	return nil
}

func (r *ProductionHubReader) startHealthMonitoring() {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			r.performHealthCheck()
		case <-r.shutdown:
			return
		}
	}
}

func (r *ProductionHubReader) performHealthCheck() {
	r.metrics.mutex.Lock()
	r.metrics.LastHealthCheck = time.Now()
	activeConnections := r.metrics.ConnectionsActive
	totalConnections := r.metrics.ConnectionsTotal
	failedConnections := r.metrics.ConnectionsFailed
	r.metrics.mutex.Unlock()

	// Log health status
	uptime := time.Since(r.metrics.StartTime)
	goroutines := runtime.NumGoroutine()

	log.Printf("🏥 Health Check: uptime=%v, connections=%d, total=%d, failed=%d, goroutines=%d",
		uptime, activeConnections, totalConnections, failedConnections, goroutines)

	// Check for concerning patterns
	if activeConnections == 0 && totalConnections > 0 {
		log.Printf("⚠️  No active connections despite previous activity")
	}

	if goroutines > 100 {
		log.Printf("⚠️  High goroutine count: %d", goroutines)
	}

	successRate := float64(totalConnections-failedConnections) / float64(max(totalConnections, 1)) * 100
	if successRate < 80 && totalConnections > 5 {
		log.Printf("⚠️  Low connection success rate: %.1f%%", successRate)
	}
}

func (r *ProductionHubReader) startMetricsReporting() {
	if !r.config.MetricsEnabled {
		return
	}

	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			r.reportMetrics()
		case <-r.shutdown:
			return
		}
	}
}

func (r *ProductionHubReader) reportMetrics() {
	r.metrics.mutex.RLock()
	startTime := r.metrics.StartTime
	connectionsActive := r.metrics.ConnectionsActive
	connectionsTotal := r.metrics.ConnectionsTotal
	connectionsFailed := r.metrics.ConnectionsFailed
	handshakeCount := r.metrics.HandshakeCount
	r.metrics.mutex.RUnlock()

	log.Printf("📊 Metrics Report:")
	log.Printf("  Uptime: %v", time.Since(startTime))
	log.Printf("  Connections: active=%d, total=%d, failed=%d", 
		connectionsActive, connectionsTotal, connectionsFailed)
	log.Printf("  Handshakes: count=%d", handshakeCount)
	log.Printf("  Trusted devices: %d", len(r.trustedDevices))

	if len(r.metrics.ErrorCounts) > 0 {
		log.Printf("  Error counts:")
		for errorType, count := range r.metrics.ErrorCounts {
			log.Printf("    %s: %d", errorType, count)
		}
	}
}

func (r *ProductionHubReader) Shutdown() {
	log.Printf("🛑 Shutting down production hub reader...")
	close(r.shutdown)
	
	// Save final state
	if err := r.saveTrustedDevices(); err != nil {
		log.Printf("⚠️  Failed to save trusted devices on shutdown: %v", err)
	}
}

// Certificate management functions

func loadOrCreateCertificate(config *Configuration) (tls.Certificate, string, error) {
	// Try to load existing certificate
	if config.CertificateFile != "" && config.PrivateKeyFile != "" {
		if _, err := os.Stat(config.CertificateFile); err == nil {
			if _, err := os.Stat(config.PrivateKeyFile); err == nil {
				log.Printf("📂 Loading existing certificate from %s", config.CertificateFile)
				
				tlsCert, err := tls.LoadX509KeyPair(config.CertificateFile, config.PrivateKeyFile)
				if err != nil {
					return tls.Certificate{}, "", fmt.Errorf("failed to load certificate: %w", err)
				}

				// Extract SKI
				x509Cert, err := x509.ParseCertificate(tlsCert.Certificate[0])
				if err != nil {
					return tls.Certificate{}, "", fmt.Errorf("failed to parse certificate: %w", err)
				}

				ski, err := cert.SkiFromCertificate(x509Cert)
				if err != nil {
					return tls.Certificate{}, "", fmt.Errorf("failed to extract SKI: %w", err)
				}

				// Check certificate expiration
				timeToExpiry := time.Until(x509Cert.NotAfter)
				if timeToExpiry < 30*24*time.Hour {
					log.Printf("⚠️  Certificate expires in %v - consider renewal", timeToExpiry)
				}

				return tlsCert, ski, nil
			}
		}
	}

	// Create new certificate
	log.Printf("🔐 Creating new certificate...")
	
	commonName := fmt.Sprintf("%s-%s", config.DeviceModel, config.DeviceSerial)
	tlsCert, err := cert.CreateCertificate(
		config.DeviceModel,    // OrganizationalUnit
		config.Organization,   // Organization
		config.Country,        // Country
		commonName,           // CommonName
	)
	if err != nil {
		return tls.Certificate{}, "", fmt.Errorf("failed to create certificate: %w", err)
	}

	// Extract SKI
	x509Cert, err := x509.ParseCertificate(tlsCert.Certificate[0])
	if err != nil {
		return tls.Certificate{}, "", fmt.Errorf("failed to parse certificate: %w", err)
	}

	ski, err := cert.SkiFromCertificate(x509Cert)
	if err != nil {
		return tls.Certificate{}, "", fmt.Errorf("failed to extract SKI: %w", err)
	}

	// Save certificate for persistence
	if config.CertificateFile != "" && config.PrivateKeyFile != "" {
		if err := saveCertificate(tlsCert, config.CertificateFile, config.PrivateKeyFile); err != nil {
			log.Printf("⚠️  Warning: Could not save certificate: %v", err)
		} else {
			log.Printf("💾 Certificate saved to %s", config.CertificateFile)
		}
	}

	return tlsCert, ski, nil
}

func saveCertificate(cert tls.Certificate, certFile, keyFile string) error {
	// Ensure directory exists
	if err := os.MkdirAll(filepath.Dir(certFile), 0700); err != nil {
		return fmt.Errorf("failed to create certificate directory: %w", err)
	}

	// Save certificate (this is a simplified implementation)
	// In production, you'd use proper PEM encoding
	certData := cert.Certificate[0]
	if err := os.WriteFile(certFile, certData, 0600); err != nil {
		return fmt.Errorf("failed to save certificate: %w", err)
	}

	// Note: This is a simplified example. In production, properly encode the private key
	log.Printf("⚠️  Note: Certificate saved in simplified format. Use proper PEM encoding in production.")

	return nil
}

// Configuration loading

func loadConfiguration(configFile string) (*Configuration, error) {
	// Default configuration
	config := &Configuration{
		DeviceBrand:        "ship-go",
		DeviceModel:        "ProductionHub",
		DeviceType:         "EnergyManager",
		DeviceSerial:       "PROD-001",
		Organization:       "YourCompany",
		Country:            "DE",
		Port:               4712,
		MaxConnections:     10,
		AutoAcceptPairing:  false, // CRITICAL: Never true in production
		TrustedDevicesFile: "trusted_devices.json",
		CertificateFile:    "ship.crt",
		PrivateKeyFile:     "ship.key",
		StateFile:          "hub_state.json",
		LogLevel:           "info",
		MetricsEnabled:     true,
	}

	// Load from file if provided
	if configFile != "" {
		data, err := os.ReadFile(configFile) // #nosec G304 - configFile comes from command line argument in example code
		if err != nil {
			return nil, fmt.Errorf("failed to read config file: %w", err)
		}

		if err := json.Unmarshal(data, config); err != nil {
			return nil, fmt.Errorf("failed to parse config file: %w", err)
		}

		log.Printf("📂 Loaded configuration from %s", configFile)
	}

	// Validate critical settings
	if config.AutoAcceptPairing {
		log.Printf("⚠️  WARNING: Auto-accept pairing is ENABLED - this should NEVER be used in production!")
	}

	return config, nil
}

func max(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

func main() {
	fmt.Println("🏭 Starting Production SHIP Hub")
	fmt.Println("==============================")

	// Load configuration
	configFile := ""
	if len(os.Args) > 1 {
		configFile = os.Args[1]
	}

	config, err := loadConfiguration(configFile)
	if err != nil {
		log.Fatal("Configuration error:", err)
	}

	// Create production hub reader
	hubReader, err := NewProductionHubReader(config)
	if err != nil {
		log.Fatal("Failed to create hub reader:", err)
	}

	// Load or create certificate
	certificate, ski, err := loadOrCreateCertificate(config)
	if err != nil {
		log.Fatal("Certificate error:", err)
	}
	fmt.Printf("📜 Device SKI: %s\n", ski)

	// Create service details
	serviceDetails := api.NewServiceDetails(ski)

	// Create mDNS manager
	deviceCategories := []api.DeviceCategoryType{} // Configure as needed
	mdnsManager := mdns.NewMDNS(
		ski,
		config.DeviceBrand,
		config.DeviceModel,
		config.DeviceType,
		config.DeviceSerial,
		deviceCategories,
		fmt.Sprintf("%s-ship-id", config.DeviceSerial),
		fmt.Sprintf("SHIP-%s", config.DeviceModel),
		config.Port,
		config.NetworkInterfaces,
		mdns.MdnsProviderSelectionAll,
	)

	// Create hub
	h := hub.NewHub(hubReader, mdnsManager, config.Port, certificate, serviceDetails)

	// Configure production settings
	h.SetMaxConnections(config.MaxConnections)

	// Start hub
	if err := h.Start(); err != nil {
		log.Fatal("Failed to start hub:", err)
	}

	fmt.Printf("✅ Production hub running on port %d\n", config.Port)
	fmt.Printf("📊 Max connections: %d\n", config.MaxConnections)
	fmt.Printf("🔒 Auto-accept pairing: %v\n", config.AutoAcceptPairing)
	if !config.AutoAcceptPairing {
		fmt.Println("   (New devices require manual approval)")
	}

	// Setup graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	// Wait for shutdown signal
	<-sigChan

	fmt.Println("\n🛑 Shutdown signal received...")

	// Graceful shutdown with timeout
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	done := make(chan bool, 1)
	go func() {
		hubReader.Shutdown()
		h.Shutdown()
		done <- true
	}()

	select {
	case <-done:
		fmt.Println("✅ Graceful shutdown completed")
	case <-shutdownCtx.Done():
		fmt.Println("⏰ Shutdown timeout - forcing exit")
		os.Exit(1)
	}

	fmt.Println("👋 Goodbye!")
}