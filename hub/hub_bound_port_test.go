package hub

import (
	"testing"

	"github.com/enbility/ship-go/api"
	"github.com/enbility/ship-go/cert"
	"github.com/enbility/ship-go/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

// TestHub_BoundPort_ResolvesRealPortForEphemeralConfig: a hub configured with
// port 0 must resolve and announce the real OS-assigned port, not 0.
func TestHub_BoundPort_ResolvesRealPortForEphemeralConfig(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	hubReader := mocks.NewMockHubReaderInterface(ctrl)
	mdnsService := mocks.NewMockMdnsInterface(ctrl)

	var announcedPort int
	mdnsService.EXPECT().SetPort(gomock.Any()).Do(func(port int) {
		announcedPort = port
	}).Times(1)
	mdnsService.EXPECT().Start(gomock.Any(), gomock.Any()).Return(nil).Times(1)
	mdnsService.EXPECT().Shutdown().Times(1)

	certificate, err := cert.CreateCertificate("unit", "org", "DE", "CN")
	require.NoError(t, err)
	localService, err := api.NewServiceDetails("localSKI", "", "")
	require.NoError(t, err)

	hub, err := newTestHub(hubReader, mdnsService, 0, certificate, localService, nil)
	require.NoError(t, err)

	assert.Equal(t, 0, hub.Port(), "Port() before Start() should reflect the configured port")

	err = hub.Start()
	require.NoError(t, err)
	defer hub.Shutdown()

	boundPort := hub.Port()
	assert.NotZero(t, boundPort, "Port() after Start() must resolve a real OS-assigned port")
	assert.Greater(t, boundPort, 0)
	assert.LessOrEqual(t, boundPort, 65535)

	assert.Equal(t, boundPort, announcedPort,
		"mDNS must be told the actual bound port, not the configured 0")
}
