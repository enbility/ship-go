package mdns

import (
	"errors"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/enbility/go-avahi"
	avahiMocks "github.com/enbility/go-avahi/mocks"
	"github.com/enbility/ship-go/api"
	"github.com/enbility/ship-go/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"
)

// IssuesSuite contains reproducer tests for known issues in the PR.
// Each test asserts the CORRECT expected behavior so it FAILS against
// the current code, and will PASS once the issue is fixed.
type IssuesSuite struct {
	suite.Suite
}

func TestIssuesSuite(t *testing.T) {
	suite.Run(t, new(IssuesSuite))
}

// ---------------------------------------------------------------------
// stopInterfaceRefresh() returns without waiting for the refreshLoop
// goroutine to exit. Shutdown() then sets mdnsProvider=nil while the
// goroutine may still be executing reannounceWithNewInterfaces().
//
// Reproducer: Inject a provider whose Announce() blocks. Trigger the
// refresh path so the goroutine enters Announce(). Call Shutdown().
// If stopInterfaceRefresh() doesn't wait, Shutdown() completes while
// the goroutine is still inside Announce() -- proving the goroutine
// outlives Shutdown().
// ---------------------------------------------------------------------

func (s *IssuesSuite) Test_ShutdownWaitsForRefreshGoroutineToExit() {
	usableIfaceName := findUsableInterfaceName(s.T())

	announceStarted := make(chan struct{})
	announceBlock := make(chan struct{})
	var announceCalls atomic.Int32

	provider := mocks.NewMdnsProviderInterface(s.T())
	provider.On("Shutdown").Maybe().Return()
	provider.On("Unannounce").Maybe().Return()
	provider.On("Announce", mock.Anything, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			call := announceCalls.Add(1)
			if call >= 2 {
				// Signal that we're inside the re-announcement Announce call
				select {
				case announceStarted <- struct{}{}:
				default:
				}
				// Block until test releases us
				<-announceBlock
			}
		}).
		Return(nil)

	mgr := NewMDNS("test", "brand", "model", "EnergyManagementSystem",
		"12345",
		[]api.DeviceCategoryType{api.DeviceCategoryTypeEnergyManagementSystem},
		"shipid", "serviceName",
		4729, []string{usableIfaceName}, MdnsProviderSelectionAll)
	mgr.SetTestProvider(provider)

	err := mgr.Start(mocks.NewMdnsReportInterface(s.T()))
	assert.Nil(s.T(), err)

	// Stop the default refresh goroutine (which has a 15s ticker we
	// can't control) and replace it with one using a manual tick channel.
	mgr.stopInterfaceRefresh()

	stopChan := make(chan struct{})
	tickChan := make(chan time.Time, 1)
	done := make(chan struct{})

	mgr.refreshMux.Lock()
	mgr.refreshStopChan = stopChan
	mgr.refreshDone = done
	mgr.refreshMux.Unlock()

	go mgr.refreshLoop(stopChan, tickChan, done)

	// Simulate interface reappearance so attemptResolveMapping detects a change
	mgr.refreshMux.Lock()
	mgr.missingIfaces = map[string]struct{}{usableIfaceName: {}}
	mgr.currentIfaces = []string{}
	mgr.refreshMux.Unlock()

	// Send a tick -- the refreshLoop goroutine will call
	// attemptResolveMapping -> reannounceWithNewInterfaces -> Announce,
	// which blocks.
	tickChan <- time.Now()

	// Wait for the goroutine to be blocked inside Announce()
	select {
	case <-announceStarted:
	case <-time.After(5 * time.Second):
		close(announceBlock)
		s.T().Fatal("timed out waiting for Announce to be called")
	}

	// Call Shutdown() while the refreshLoop goroutine is still inside
	// Announce(). stopInterfaceRefresh() must wait for it to exit.
	shutdownDone := make(chan struct{})
	go func() {
		mgr.Shutdown()
		close(shutdownDone)
	}()

	// Check: did Shutdown() complete while the goroutine is still blocked?
	// If stopInterfaceRefresh properly waits, Shutdown() should NOT complete
	// until we release the goroutine.
	select {
	case <-shutdownDone:
		close(announceBlock)
		s.T().Fatal("Shutdown() returned while refresh goroutine was still running inside Announce()")
	case <-time.After(200 * time.Millisecond):
		// EXPECTED: Shutdown() is blocked waiting for the goroutine.
		// Release the goroutine so everything can finish.
		close(announceBlock)
	}

	// Wait for shutdown to actually complete
	select {
	case <-shutdownDone:
	case <-time.After(5 * time.Second):
		s.T().Fatal("Shutdown() did not complete after goroutine was released")
	}
}

// ---------------------------------------------------------------------
// mdnsProvider was previously read by the refresh goroutine without
// synchronization while Shutdown() writes nil to it. The fix for
// stopInterfaceRefresh (waiting for the goroutine to exit) eliminates
// this race: Shutdown() now blocks until the goroutine is done before
// touching mdnsProvider.
//
// This test verifies the fix by running Start/Shutdown cycles while
// the refresh goroutine is actively processing interface changes.
// With -race, any remaining unsynchronized access would be detected.
// ---------------------------------------------------------------------

func (s *IssuesSuite) Test_ProviderAccessSynchronizedWithShutdown() {
	usableIfaceName := findUsableInterfaceName(s.T())

	report := mocks.NewMdnsReportInterface(s.T())
	report.On("ReportMdnsEntries", mock.Anything, mock.Anything).Maybe().Return()

	for i := 0; i < 20; i++ {
		provider := mocks.NewMdnsProviderInterface(s.T())
		provider.On("Shutdown").Maybe().Return()
		provider.On("Unannounce").Maybe().Return()
		provider.On("Announce", mock.Anything, mock.Anything, mock.Anything).Maybe().Return(nil)

		mgr := NewMDNS("test", "brand", "model", "EnergyManagementSystem",
			"12345",
			[]api.DeviceCategoryType{api.DeviceCategoryTypeEnergyManagementSystem},
			"shipid", "serviceName",
			4729, []string{usableIfaceName}, MdnsProviderSelectionAll)
		mgr.SetTestProvider(provider)

		err := mgr.Start(report)
		assert.Nil(s.T(), err)

		// Simulate an interface change so the refresh goroutine will
		// call reannounceWithNewInterfaces on its next tick.
		mgr.refreshMux.Lock()
		mgr.missingIfaces = map[string]struct{}{usableIfaceName: {}}
		mgr.currentIfaces = []string{}
		mgr.refreshMux.Unlock()

		// Call Shutdown() which must wait for the refresh goroutine to
		// finish before setting mdnsProvider = nil. With -race, any
		// unsynchronized access would be caught.
		mgr.Shutdown()

		assert.Nil(s.T(), mgr.mdnsProvider)
	}
}

// ---------------------------------------------------------------------
// updateProviderInterfaces() uses a type-switch on concrete
// *AvahiProvider / *ZeroconfProvider. Any other MdnsProviderInterface
// implementation (mocks, future providers) is silently skipped.
//
// Reproducer: Create a minimal provider that implements the interface
// plus an UpdateInterfaces method. Show that updateProviderInterfaces
// never calls it because the type-switch doesn't know about it.
// ---------------------------------------------------------------------

// trackingProvider is a test provider that tracks whether its interfaces
// were updated. It implements MdnsProviderInterface and
// mdnsProviderInterfaceUpdater.
type trackingProvider struct {
	interfacesUpdated atomic.Bool
}

func (t *trackingProvider) Start(bool, api.MdnsResolveCB) bool    { return true }
func (t *trackingProvider) Shutdown()                              {}
func (t *trackingProvider) Announce(string, int, []string) error   { return nil }
func (t *trackingProvider) Unannounce()                            {}
func (t *trackingProvider) UpdateInterfaces([]net.Interface, []int32) {
	t.interfacesUpdated.Store(true)
}

func (s *IssuesSuite) Test_UpdateProviderInterfacesWorksForAnyProvider() {
	tp := &trackingProvider{}

	mgr := NewMDNS("test", "brand", "model", "EnergyManagementSystem",
		"12345",
		[]api.DeviceCategoryType{api.DeviceCategoryTypeEnergyManagementSystem},
		"shipid", "serviceName",
		4729, nil, MdnsProviderSelectionAll)
	mgr.mdnsProvider = tp

	testIfaces := []net.Interface{{Name: "eth0", Index: 1}}
	testIndexes := []int32{1}

	mgr.updateProviderInterfaces(testIfaces, testIndexes)

	// EXPECTED: the provider's interfaces should have been updated.
	// BUG: the type-switch only handles *AvahiProvider and *ZeroconfProvider,
	// so trackingProvider (or any mock/future provider) is silently skipped.
	assert.True(s.T(), tp.interfacesUpdated.Load(),
		"updateProviderInterfaces should update any provider, not just concrete Avahi/Zeroconf types")
}

// ---------------------------------------------------------------------
// Shutdown() does not explicitly reset isAnnounced. If
// UnannounceMdnsEntry() panics (caught by the recover wrapper),
// isAnnounced remains stale-true after Shutdown().
//
// Reproducer: Set isAnnounced=true, make Unannounce panic, call
// Shutdown(), assert isAnnounced is false (the correct behavior).
// This fails because Shutdown() never resets it.
// ---------------------------------------------------------------------

func (s *IssuesSuite) Test_IsAnnouncedResetAfterShutdownWithPanic() {
	provider := mocks.NewMdnsProviderInterface(s.T())
	provider.On("Shutdown").Maybe().Return()
	provider.On("Announce", mock.Anything, mock.Anything, mock.Anything).Maybe().Return(nil)
	provider.On("Unannounce").Run(func(args mock.Arguments) {
		panic("simulated unannounce panic")
	}).Return()

	mgr := NewMDNS("test", "brand", "model", "EnergyManagementSystem",
		"12345",
		[]api.DeviceCategoryType{api.DeviceCategoryTypeEnergyManagementSystem},
		"shipid", "serviceName",
		4729, []string{"fake_iface_issue4"}, MdnsProviderSelectionAll)
	mgr.SetTestProvider(provider)
	mgr.mdnsProvider = provider
	mgr.setIsServiceAnnounce(true)

	// Shutdown should not panic (it has recover() wrappers)
	assert.NotPanics(s.T(), func() {
		mgr.Shutdown()
	})

	// EXPECTED: isAnnounced should always be false after Shutdown(),
	// regardless of whether Unannounce panicked.
	// BUG: isAnnounced remains true because the panic prevented
	// UnannounceMdnsEntry from clearing it, and Shutdown() never
	// explicitly resets it.
	assert.False(s.T(), mgr.isServiceAnnounced(),
		"isAnnounced must be false after Shutdown, even if Unannounce panics")
}

// ---------------------------------------------------------------------
// AvahiProvider.Announce() uses manual a.mux.Unlock() on each error
// path instead of defer. If a new error path is added without an
// unlock, the mutex stays locked and the next call deadlocks.
//
// Regression guard: Force each error path in Announce(), then call
// Announce() again. If the mutex was not properly released, the
// second call deadlocks (detected via timeout).
// ---------------------------------------------------------------------

func (s *IssuesSuite) Test_AvahiAnnounceMutexReleasedOnAllErrorPaths() {
	someError := errors.New("some error")

	// Error path 1: EntryGroupNew fails
	s.Run("EntryGroupNew_fails", func() {
		avahiMock := avahiMocks.NewServerInterface(s.T())
		entryGroupMock := avahiMocks.NewEntryGroupInterface(s.T())

		sut := NewAvahiProvider([]int32{1})
		sut.avServer = avahiMock

		// First call: EntryGroupNew fails
		avahiMock.EXPECT().EntryGroupNew().Return(nil, someError).Once()
		err := sut.Announce("test", 4729, []string{"txt=1"})
		assert.NotNil(s.T(), err)

		// Second call: must not deadlock (mutex was released on error)
		avahiMock.EXPECT().EntryGroupNew().Return(entryGroupMock, nil).Once()
		entryGroupMock.EXPECT().AddService(
			mock.Anything, mock.Anything, mock.Anything,
			"test", shipZeroConfServiceType, shipZeroConfDomain,
			"", mock.Anything, mock.Anything,
		).Return(nil).Once()
		entryGroupMock.EXPECT().Commit().Return(nil).Once()

		done := make(chan error, 1)
		go func() {
			done <- sut.Announce("test", 4729, []string{"txt=1"})
		}()

		select {
		case err := <-done:
			assert.Nil(s.T(), err, "second Announce should succeed")
		case <-time.After(2 * time.Second):
			s.T().Fatal("deadlock: mutex not released after EntryGroupNew error")
		}
	})

	// Error path 2: AddService fails
	s.Run("AddService_fails", func() {
		avahiMock := avahiMocks.NewServerInterface(s.T())
		entryGroupMock := avahiMocks.NewEntryGroupInterface(s.T())

		sut := NewAvahiProvider([]int32{1})
		sut.avServer = avahiMock

		// First call: AddService fails
		avahiMock.EXPECT().EntryGroupNew().Return(entryGroupMock, nil).Once()
		entryGroupMock.EXPECT().AddService(
			mock.Anything, mock.Anything, mock.Anything,
			"test", shipZeroConfServiceType, shipZeroConfDomain,
			"", mock.Anything, mock.Anything,
		).Return(someError).Once()
		avahiMock.EXPECT().EntryGroupFree(entryGroupMock).Return().Once()
		err := sut.Announce("test", 4729, []string{"txt=1"})
		assert.NotNil(s.T(), err)

		// Second call: must not deadlock
		entryGroupMock2 := avahiMocks.NewEntryGroupInterface(s.T())
		avahiMock.EXPECT().EntryGroupNew().Return(entryGroupMock2, nil).Once()
		entryGroupMock2.EXPECT().AddService(
			mock.Anything, mock.Anything, mock.Anything,
			"test", shipZeroConfServiceType, shipZeroConfDomain,
			"", mock.Anything, mock.Anything,
		).Return(nil).Once()
		entryGroupMock2.EXPECT().Commit().Return(nil).Once()

		done := make(chan error, 1)
		go func() {
			done <- sut.Announce("test", 4729, []string{"txt=1"})
		}()

		select {
		case err := <-done:
			assert.Nil(s.T(), err, "second Announce should succeed")
		case <-time.After(2 * time.Second):
			s.T().Fatal("deadlock: mutex not released after AddService error")
		}
	})

	// Error path 3: Commit fails
	s.Run("Commit_fails", func() {
		avahiMock := avahiMocks.NewServerInterface(s.T())
		entryGroupMock := avahiMocks.NewEntryGroupInterface(s.T())

		sut := NewAvahiProvider([]int32{1})
		sut.avServer = avahiMock

		// First call: Commit fails
		avahiMock.EXPECT().EntryGroupNew().Return(entryGroupMock, nil).Once()
		entryGroupMock.EXPECT().AddService(
			mock.Anything, mock.Anything, mock.Anything,
			"test", shipZeroConfServiceType, shipZeroConfDomain,
			"", mock.Anything, mock.Anything,
		).Return(nil).Once()
		entryGroupMock.EXPECT().Commit().Return(someError).Once()
		avahiMock.EXPECT().EntryGroupFree(entryGroupMock).Return().Once()
		err := sut.Announce("test", 4729, []string{"txt=1"})
		assert.NotNil(s.T(), err)

		// Second call: must not deadlock
		entryGroupMock2 := avahiMocks.NewEntryGroupInterface(s.T())
		avahiMock.EXPECT().EntryGroupNew().Return(entryGroupMock2, nil).Once()
		entryGroupMock2.EXPECT().AddService(
			mock.Anything, mock.Anything, mock.Anything,
			"test", shipZeroConfServiceType, shipZeroConfDomain,
			"", mock.Anything, mock.Anything,
		).Return(nil).Once()
		entryGroupMock2.EXPECT().Commit().Return(nil).Once()

		done := make(chan error, 1)
		go func() {
			done <- sut.Announce("test", 4729, []string{"txt=1"})
		}()

		select {
		case err := <-done:
			assert.Nil(s.T(), err, "second Announce should succeed")
		case <-time.After(2 * time.Second):
			s.T().Fatal("deadlock: mutex not released after Commit error")
		}
	})
}

// ---------------------------------------------------------------------
// AvahiProvider.Shutdown() sends on an unbuffered shutdownChan while
// holding a.mux (avahi.go:154). If chanListener is not sitting at the
// `select` -- e.g. because it received a service from addServiceChan and
// is now inside processService -- the send blocks forever. Shutdown()
// cannot release a.mux because the send is not complete; chanListener
// cannot return to the select because it is busy elsewhere in
// processService. Classic lock/channel deadlock.
//
// This PR makes the bug materially worse by introducing getIfaceIndexes(),
// a new code path inside processService that acquires a.mux. Before the
// PR, processService read a.ifaceIndexes without locking, so a Shutdown
// holding a.mux could at worst wait for ResolveService to return. Now,
// Shutdown holding a.mux *actively poisons* chanListener's progress by
// blocking it inside getIfaceIndexes the instant it enters processService,
// guaranteeing the deadlock cycle.
//
// This reproducer deterministically pins chanListener inside
// processService (past getIfaceIndexes, blocked inside a mocked
// ResolveService) and then invokes Shutdown(). With the bug, Shutdown()
// never completes. With the fix, Shutdown() returns promptly regardless
// of chanListener's state.
//
// Fix: buffer shutdownChan (make(chan struct{}, 1)) or move the send
// after a.mux.Unlock(). Either eliminates the lock/channel cycle and
// also covers the getIfaceIndexes variant, which shares the same root
// cause.
// ---------------------------------------------------------------------

func (s *IssuesSuite) Test_AvahiShutdownDoesNotDeadlockOnBusyChanListener() {
	avahiMock := avahiMocks.NewServerInterface(s.T())
	serviceBrowserMock := avahiMocks.NewServiceBrowserInterface(s.T())

	sut := NewAvahiProvider([]int32{1})
	sut.avServer = avahiMock

	// Start the provider. Spawns chanListener in a goroutine.
	avahiMock.EXPECT().Setup(mock.Anything).Return(nil).Once()
	avahiMock.EXPECT().Start().Return().Once()
	avahiMock.EXPECT().GetAPIVersion().Return(0, nil).Once()
	avahiMock.EXPECT().ServiceBrowserNew(
		mock.AnythingOfType("chan avahi.Service"),
		mock.AnythingOfType("chan avahi.Service"),
		int32(-1), int32(-1),
		shipZeroConfServiceType, shipZeroConfDomain,
		uint32(0)).Return(serviceBrowserMock, nil).Once()

	noopCB := func(map[string]string, string, string, []net.IP, int, bool) {}
	assert.True(s.T(), sut.Start(true, noopCB))

	// Park chanListener inside processService: ResolveService blocks on a
	// signal. By the time it runs, chanListener has already received from
	// addServiceChan, passed getIfaceIndexes, and is no longer sitting on
	// the select -- exactly the state where an unbuffered send on
	// shutdownChan has no receiver available.
	resolveInProgress := make(chan struct{})
	releaseResolve := make(chan struct{})
	avahiMock.On("ResolveService",
		mock.Anything, mock.Anything, mock.Anything, mock.Anything,
		mock.Anything, mock.Anything, mock.Anything,
	).Run(func(args mock.Arguments) {
		close(resolveInProgress)
		<-releaseResolve
	}).Return(avahi.Service{}, errors.New("released after test")).Once()

	testService := avahi.Service{
		Interface: 1, // must match ifaceIndexes[0] so processService reaches ResolveService
		Name:      "TestService",
		Type:      "_ship._tcp",
		Domain:    "local",
		Aprotocol: -1,
	}

	// Unbuffered send: returns only after chanListener has received it
	// and is now executing processService.
	sut.addServiceChan <- testService

	// Wait until chanListener is definitely inside ResolveService.
	select {
	case <-resolveInProgress:
	case <-time.After(2 * time.Second):
		close(releaseResolve)
		s.T().Fatal("ResolveService was never invoked by chanListener")
	}

	// Mocks used on the Shutdown path.
	avahiMock.EXPECT().ServiceBrowserFree(serviceBrowserMock).Return().Once()
	avahiMock.EXPECT().Shutdown().Return().Once()

	// Invoke Shutdown() from a goroutine and measure its liveness. With
	// the fix in place, Shutdown() returns promptly regardless of what
	// chanListener is doing.
	shutdownDone := make(chan struct{})
	go func() {
		sut.Shutdown()
		close(shutdownDone)
	}()

	const shutdownDeadline = 500 * time.Millisecond
	select {
	case <-shutdownDone:
		// PASS: Shutdown is decoupled from chanListener's progress.
		// Let chanListener unwind for clean teardown.
		close(releaseResolve)

	case <-time.After(shutdownDeadline):
		// BUG: Shutdown() is blocked on `a.shutdownChan <- struct{}{}`
		// at avahi.go:154. Release chanListener so Shutdown can
		// eventually unwind and we can report the failure cleanly --
		// once chanListener returns to its select it will receive the
		// pending send and unblock Shutdown.
		close(releaseResolve)
		select {
		case <-shutdownDone:
		case <-time.After(2 * time.Second):
			s.T().Fatal("Shutdown remained blocked even after releasing chanListener")
		}
		s.T().Fatalf(
			"DEADLOCK: AvahiProvider.Shutdown() did not complete within %s "+
				"while chanListener was busy inside processService. "+
				"Shutdown is blocked on the unbuffered send "+
				"`a.shutdownChan <- struct{}{}` at avahi.go:154 while "+
				"holding a.mux. Its liveness must not depend on chanListener "+
				"being idle. Fix: buffer shutdownChan with capacity 1, or "+
				"move the send after a.mux.Unlock().",
			shutdownDeadline,
		)
	}
}

// ---------------------------------------------------------------------
// MdnsManager.SetAutoAccept() calls Unannounce() before re-announcing,
// unlike reannounceWithNewInterfaces() which uses create-then-swap.
// The explicit Unannounce sends mDNS goodbye packets, causing remote
// devices to think the service left the network. It also performs
// blocking I/O (DBus / zeroconf Shutdown) that compounds with the
// muxReg lock-hold issue in Hub.SetAutoAccept.
//
// Reproducer: Set up an announced MdnsManager, call SetAutoAccept,
// and verify that the provider's Unannounce() is NOT called — only
// Announce() should be called (create-then-swap pattern).
//
// EXPECTED (current code): FAIL — Unannounce is called.
// EXPECTED (after fix):    PASS — only Announce is called.
// ---------------------------------------------------------------------

func (s *IssuesSuite) Test_SetAutoAcceptDoesNotCallUnannounce() {
	var unannounced atomic.Bool

	provider := mocks.NewMdnsProviderInterface(s.T())
	provider.On("Announce", mock.Anything, mock.Anything, mock.Anything).Maybe().Return(nil)
	provider.On("Unannounce").Maybe().Run(func(_ mock.Arguments) {
		unannounced.Store(true)
	}).Return()

	mgr := NewMDNS("test", "brand", "model", "EnergyManagementSystem",
		"12345",
		[]api.DeviceCategoryType{api.DeviceCategoryTypeEnergyManagementSystem},
		"shipid", "serviceName",
		4729, nil, MdnsProviderSelectionAll)
	mgr.SetTestProvider(provider)
	mgr.mdnsProvider = provider
	mgr.setIsServiceAnnounce(true)

	mgr.SetAutoAccept(true)

	// The provider's Announce should have been called (to re-announce
	// with the updated autoaccept TXT record).
	provider.AssertCalled(s.T(), "Announce", mock.Anything, mock.Anything, mock.Anything)

	// Unannounce should NOT have been called. The create-then-swap
	// pattern used by reannounceWithNewInterfaces avoids goodbye
	// packets that disrupt remote devices.
	assert.False(s.T(), unannounced.Load(),
		"SetAutoAccept must not call Unannounce — use create-then-swap like reannounceWithNewInterfaces")
}

// Helper: verify avahi.InterfaceUnspec is what we expect
func init() {
	_ = avahi.InterfaceUnspec // ensure import is used
}
