package api

import "net"

/* Mdns */

type MdnsEntry struct {
	Name       string               // the mDNS service name
	Ski        string               // mandatory the certificates SKI
	Identifier string               // mandatory, the identifier used for SHIP ID
	Path       string               // mandatory, the websocket path
	Register   bool                 // mandatory, wether auto accept is enabled
	Brand      string               // optional, the brand of the device
	Type       string               // optional, the type of the device
	Model      string               // optional, the model of the device
	Serial     string               // recommended, the serial number of the device
	Categories []DeviceCategoryType // mandatory, the device categories of the device. Can be empty when the device does not conform to SHIP Requirements for Installation Process
	Host       string               // mandatory, the host name
	Port       int                  // mandatory, the port for the websocket service
	Addresses  []net.IP             // mandatory, the IP addresses used by the service
}

// MdnsReportInterface is implemented by Hub and used by mDNS.
// This interface receives discovered SHIP services from mDNS discovery.
type MdnsReportInterface interface {
	// ReportMdnsEntries delivers discovered SHIP services to the Hub.
	// This method is called when mDNS discovery finds new services or when
	// existing services are updated or removed.
	//
	// Parameters:
	//   - entries: Map of discovered services keyed by service name
	//   - newEntries: true if these are newly discovered services, false for updates
	//
	// The Hub uses this information to initiate connections to discovered devices
	// and manage the service registry.
	ReportMdnsEntries(entries map[string]*MdnsEntry, newEntries bool)
}

// MdnsInterface is implemented by mDNS and used by Hub.
// This interface manages SHIP service discovery and announcement via multicast DNS.
type MdnsInterface interface {
	// Start initializes the mDNS service with the specified pairing mode.
	// The callback interface receives discovered services asynchronously.
	//
	// Parameters:
	//   - pairingMode: Controls auto-accept behavior (auto, ask, deny)
	//   - cb: Callback interface for reporting discovered services
	//
	// Returns:
	//   - nil if mDNS service starts successfully
	//   - error if startup fails (e.g., network issues, permissions)
	Start(pairingMode PairingMode, cb MdnsReportInterface) error

	// Shutdown gracefully stops the mDNS service and cleans up resources.
	// This includes stopping discovery, removing announcements, and closing network resources.
	Shutdown()

	// AnnounceMdnsEntry publishes this device's SHIP service via mDNS.
	// Other devices can discover this service to initiate connections.
	// The announcement includes device metadata and connection information.
	//
	// Returns:
	//   - nil if announcement succeeds
	//   - error if announcement fails (e.g., network issues)
	AnnounceMdnsEntry() error

	// UnannounceMdnsEntry removes this device's SHIP service announcement.
	// This makes the device invisible to other devices performing discovery.
	UnannounceMdnsEntry()

	// SetAutoAccept updates the auto-accept flag in the mDNS announcement.
	// This controls whether the device automatically accepts pairing requests.
	//
	// Parameters:
	//   - autoAccept: true to enable auto-accept, false to require manual approval
	SetAutoAccept(bool)

	// DeviceBrand returns the device brand for QR code generation and mDNS announcements.
	// This is optional metadata that helps users identify the device.
	DeviceBrand() string

	// DeviceModel returns the device model for QR code generation and mDNS announcements.
	// This is optional metadata that helps users identify the device.
	DeviceModel() string

	// DeviceSerial returns the device serial number for QR code generation and mDNS announcements.
	// This is recommended metadata for unique device identification.
	DeviceSerial() string

	// DeviceType returns the device type string value for QR code generation and mDNS announcements.
	// This is optional metadata that helps users identify the device.
	DeviceType() string

	// DeviceCategories returns the device categories for QR code generation and mDNS announcements.
	// Categories help classify the device type per SHIP requirements for installation process.
	DeviceCategories() []DeviceCategoryType

	// RequestMdnsEntries triggers an immediate discovery scan for SHIP services.
	// This supplements continuous discovery by forcing an immediate search.
	// Discovered services are reported via the MdnsReportInterface callback.
	RequestMdnsEntries()
}

// MdnsPairingInterface provides SHIP Pairing Service functionality via mDNS.
// This interface is implemented by MdnsManager when pairing service support is needed.
// It handles the _shippairing._tcp service type for device pairing operations.
type MdnsPairingInterface interface {
	// AnnouncePairingService publishes a _shippairing._tcp service with pairing data.
	// This announces that the device is available for SHIP pairing using the
	// provided TXT record containing HMAC-signed pairing information.
	//
	// Parameters:
	//   - txtRecord: The SHIP pairing TXT record containing pairing data and HMAC signature
	//
	// Returns:
	//   - instanceID: Unique identifier for this announcement, used for later removal
	//   - error: nil if announcement succeeds, error if it fails
	//
	// The announcement remains active until removed via UnannouncePairingService
	AnnouncePairingService(txtRecord *ShipPairingTXT) (string, error)

	// UnannouncePairingService removes a _shippairing._tcp announcement by instance ID.
	// This stops advertising the pairing service and makes the device unavailable
	// for pairing via the specified announcement.
	//
	// Parameters:
	//   - instanceID: The unique identifier returned by AnnouncePairingService
	//
	// Returns:
	//   - nil if unannouncement succeeds
	//   - error if the instance ID is invalid or unannouncement fails
	UnannouncePairingService(instanceID string) error

	// SearchPairingServices discovers _shippairing._tcp services on the network.
	// This method searches for devices advertising pairing availability and
	// calls the callback for each discovered service.
	//
	// Parameters:
	//   - callback: Function called for each discovered pairing service
	//               Returns true to continue searching, false to stop
	//
	// Returns:
	//   - nil if search completes successfully
	//   - error if search cannot be initiated or fails
	//
	// The search runs until the callback returns false or an error occurs
	SearchPairingServices(callback func(*ShipPairingTXT) bool) error

	// RequestPairingEntries triggers an immediate discovery scan for SHIP Pairing Services.
	// This supplements continuous pairing discovery by forcing an immediate search.
	// Returns a map of currently discovered pairing services keyed by service name.
	//
	// This method is the pairing equivalent of RequestMdnsEntries() for regular SHIP services.
	//
	// Returns:
	//   - map[string]*ShipPairingTXT: Currently discovered pairing services
	//   - error: nil if request succeeds, error if the mDNS manager is not started
	RequestPairingEntries() (map[string]*ShipPairingTXT, error)

	// IsPairingServiceAnnounced checks if any pairing service is currently announced.
	// This helps prevent duplicate announcements and provides status information.
	//
	// Returns:
	//   - true if at least one _shippairing._tcp service is currently announced
	//   - false if no pairing services are announced
	IsPairingServiceAnnounced() bool
}

// MdnsResolveCB is a callback function used by mDNS providers to report service changes.
// This callback is invoked when services are discovered, updated, or removed from the network.
// It provides detailed information about each service event for processing by the mDNS manager.
//
// Parameters:
//   - elements: TXT record key-value pairs containing service metadata
//   - name: The service instance name (e.g., "Device-ABC._ship._tcp.local")
//   - host: The hostname where the service is available
//   - serviceType: The service type (e.g., "_ship._tcp", "_shippairing._tcp")
//   - addresses: List of IP addresses where the service can be reached
//   - port: The TCP port number where the service is listening
//   - remove: true if the service was removed, false if discovered/updated
//
// This callback is called asynchronously from network discovery threads and should
// handle the information efficiently without blocking.
type MdnsResolveCB func(elements map[string]string, name, host, serviceType string, addresses []net.IP, port int, remove bool)

// MdnsProviderInterface is implemented by mDNS providers and used by MdnsManager.
// This interface abstracts the underlying mDNS implementation (Avahi, Zeroconf, etc.)
// and provides platform-specific mDNS functionality.
type MdnsProviderInterface interface {
	// Start initializes the mDNS provider with the specified configuration.
	// This establishes the connection to the underlying mDNS system and begins
	// monitoring for service announcements.
	//
	// Parameters:
	//   - pairingMode: Controls auto-accept behavior for discovered services
	//   - autoReconnect: Whether to automatically reconnect on network changes
	//   - cb: Callback function for reporting discovered/removed services
	//
	// Returns:
	//   - true if the provider starts successfully
	//   - false if startup fails (e.g., mDNS daemon unavailable, permissions)
	Start(pairingMode PairingMode, autoReconnect bool, cb MdnsResolveCB) bool

	// Shutdown gracefully stops the mDNS provider and releases system resources.
	// This disconnects from the mDNS system and stops all service monitoring.
	Shutdown()

	// AnnounceService publishes a service announcement via the mDNS provider.
	// This is used for both regular SHIP services and SHIP Pairing Services.
	// The service remains announced until explicitly removed.
	//
	// Parameters:
	//   - serviceType: The service type (e.g., "_ship._tcp", "_shippairing._tcp")
	//   - serviceName: The unique service instance name
	//   - port: The TCP port where the service is available
	//   - txt: Array of TXT record strings containing service metadata
	//
	// Returns:
	//   - instanceID: Unique identifier for this announcement
	//   - error: nil if announcement succeeds, error if it fails
	AnnounceService(serviceType, serviceName string, port int, txt []string) (string, error)

	// UnannounceService removes a service announcement by instance ID.
	// This stops advertising the service and makes it unavailable to other devices.
	//
	// Parameters:
	//   - instanceID: The unique identifier returned by AnnounceService
	//
	// Returns:
	//   - nil if unannouncement succeeds
	//   - error if the instance ID is invalid or unannouncement fails
	UnannounceService(instanceID string) error
}
