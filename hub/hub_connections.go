package hub

// This file now contains only the core connection management functionality
// that wasn't moved to the specialized files:
// - hub_connections_server.go: WebSocket server and incoming connections
// - hub_connections_client.go: Outgoing connections and client logic
// - hub_connections_registry.go: Connection registry and double-connection prevention
// - hub_connections_retry.go: Connection retry logic and backoff
// - hub_connections_timers.go: Timer management for connection delays

// All connection-related functionality has been moved to the appropriate specialized files
// following the existing patterns in the ship-go codebase.