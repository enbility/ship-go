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

// ---------------------------------------------------------------------
// Issue #2: AvahiProvider.Announce() holds a.mux across DBus calls
// (EntryGroupNew, AddService, Commit). chanListener — the goroutine
// that consumes the DBus signal stream delivering replies to those
// calls — needs a.mux via processService → getIfaceIndexes. Blocking
// chanListener prevents DBus reply delivery: hard deadlock.
//
// Tests 1-3 isolate the contention to each individual DBus call.
// Test 4 reproduces the full deadlock chain with a live chanListener.
// Tests 5-6 show collateral damage to other a.mux callers.
//
// All tests assert CORRECT (fixed) behavior: they FAIL against the
// current code and PASS once the fix is applied.
// ---------------------------------------------------------------------

// Test 1: a.mux held across EntryGroupNew blocks getIfaceIndexes.
func (s *IssuesSuite) Test_AnnounceDoesNotBlockGetIfaceIndexesDuringEntryGroupNew() {
	avahiMock := avahiMocks.NewServerInterface(s.T())
	entryGroupMock := avahiMocks.NewEntryGroupInterface(s.T())

	sut := NewAvahiProvider([]int32{1})
	sut.avServer = avahiMock

	entryGroupNewEntered := make(chan struct{})
	releaseEntryGroupNew := make(chan struct{})

	avahiMock.On("EntryGroupNew").Run(func(_ mock.Arguments) {
		close(entryGroupNewEntered)
		<-releaseEntryGroupNew
	}).Return(entryGroupMock, nil).Once()
	entryGroupMock.On("AddService", mock.Anything, mock.Anything, mock.Anything,
		"test", shipZeroConfServiceType, shipZeroConfDomain,
		"", mock.Anything, mock.Anything).Return(nil).Once()
	entryGroupMock.On("Commit").Return(nil).Once()

	announceDone := make(chan error, 1)
	go func() { announceDone <- sut.Announce("test", 4729, []string{"txt=1"}) }()

	select {
	case <-entryGroupNewEntered:
	case <-time.After(2 * time.Second):
		close(releaseEntryGroupNew)
		s.T().Fatal("EntryGroupNew was never entered")
	}

	// a.mux is now held by Announce, inside EntryGroupNew.
	// getIfaceIndexes must complete promptly — it must not contend on a.mux.
	ifacesDone := make(chan struct{})
	go func() {
		_ = sut.getIfaceIndexes()
		close(ifacesDone)
	}()

	select {
	case <-ifacesDone:
		// PASS: a.mux is not held across the DBus call.
	case <-time.After(500 * time.Millisecond):
		s.T().Fatal(
			"CONTENTION: getIfaceIndexes blocked for >500ms while Announce " +
				"held a.mux inside EntryGroupNew. In production, chanListener " +
				"calls getIfaceIndexes — blocking it prevents DBus reply " +
				"delivery, causing a hard deadlock. Fix: release a.mux before " +
				"DBus calls (EntryGroupNew, AddService, Commit).")
	}

	close(releaseEntryGroupNew)

	select {
	case <-announceDone:
	case <-time.After(2 * time.Second):
		s.T().Fatal("Announce did not complete")
	}
}

// Test 2: a.mux held across AddService blocks getIfaceIndexes.
func (s *IssuesSuite) Test_AnnounceDoesNotBlockGetIfaceIndexesDuringAddService() {
	avahiMock := avahiMocks.NewServerInterface(s.T())
	entryGroupMock := avahiMocks.NewEntryGroupInterface(s.T())

	sut := NewAvahiProvider([]int32{1})
	sut.avServer = avahiMock

	addServiceEntered := make(chan struct{})
	releaseAddService := make(chan struct{})

	avahiMock.On("EntryGroupNew").Return(entryGroupMock, nil).Once()
	entryGroupMock.On("AddService", mock.Anything, mock.Anything, mock.Anything,
		"test", shipZeroConfServiceType, shipZeroConfDomain,
		"", mock.Anything, mock.Anything).
		Run(func(_ mock.Arguments) {
			close(addServiceEntered)
			<-releaseAddService
		}).Return(nil).Once()
	entryGroupMock.On("Commit").Return(nil).Once()

	announceDone := make(chan error, 1)
	go func() { announceDone <- sut.Announce("test", 4729, []string{"txt=1"}) }()

	select {
	case <-addServiceEntered:
	case <-time.After(2 * time.Second):
		close(releaseAddService)
		s.T().Fatal("AddService was never entered")
	}

	ifacesDone := make(chan struct{})
	go func() {
		_ = sut.getIfaceIndexes()
		close(ifacesDone)
	}()

	select {
	case <-ifacesDone:
	case <-time.After(500 * time.Millisecond):
		s.T().Fatal(
			"CONTENTION: getIfaceIndexes blocked while Announce held a.mux " +
				"inside AddService. Same deadlock vector as EntryGroupNew.")
	}

	close(releaseAddService)

	select {
	case <-announceDone:
	case <-time.After(2 * time.Second):
		s.T().Fatal("Announce did not complete")
	}
}

// Test 3: a.mux held across Commit blocks getIfaceIndexes.
func (s *IssuesSuite) Test_AnnounceDoesNotBlockGetIfaceIndexesDuringCommit() {
	avahiMock := avahiMocks.NewServerInterface(s.T())
	entryGroupMock := avahiMocks.NewEntryGroupInterface(s.T())

	sut := NewAvahiProvider([]int32{1})
	sut.avServer = avahiMock

	commitEntered := make(chan struct{})
	releaseCommit := make(chan struct{})

	avahiMock.On("EntryGroupNew").Return(entryGroupMock, nil).Once()
	entryGroupMock.On("AddService", mock.Anything, mock.Anything, mock.Anything,
		"test", shipZeroConfServiceType, shipZeroConfDomain,
		"", mock.Anything, mock.Anything).Return(nil).Once()
	entryGroupMock.On("Commit").
		Run(func(_ mock.Arguments) {
			close(commitEntered)
			<-releaseCommit
		}).Return(nil).Once()

	announceDone := make(chan error, 1)
	go func() { announceDone <- sut.Announce("test", 4729, []string{"txt=1"}) }()

	select {
	case <-commitEntered:
	case <-time.After(2 * time.Second):
		close(releaseCommit)
		s.T().Fatal("Commit was never entered")
	}

	ifacesDone := make(chan struct{})
	go func() {
		_ = sut.getIfaceIndexes()
		close(ifacesDone)
	}()

	select {
	case <-ifacesDone:
	case <-time.After(500 * time.Millisecond):
		s.T().Fatal(
			"CONTENTION: getIfaceIndexes blocked while Announce held a.mux " +
				"inside Commit.")
	}

	close(releaseCommit)

	select {
	case <-announceDone:
	case <-time.After(2 * time.Second):
		s.T().Fatal("Announce did not complete")
	}
}

// Test 4 (crown jewel): Full chanListener deadlock simulation.
//
// Start the provider so chanListener is running. Block EntryGroupNew
// (simulating a DBus round-trip) while Announce holds a.mux. Inject a
// service event on addServiceChan. chanListener picks it up but blocks
// in getIfaceIndexes on a.mux. In production the DBus reply to
// EntryGroupNew is delivered through chanListener — with it blocked,
// the reply never arrives. Neither goroutine can proceed: hard deadlock.
func (s *IssuesSuite) Test_AnnounceDuringActiveServiceDiscoveryDoesNotDeadlock() {
	avahiMock := avahiMocks.NewServerInterface(s.T())
	serviceBrowserMock := avahiMocks.NewServiceBrowserInterface(s.T())
	entryGroupMock := avahiMocks.NewEntryGroupInterface(s.T())

	sut := NewAvahiProvider([]int32{1})
	sut.avServer = avahiMock

	// Start the provider — spawns chanListener goroutine.
	avahiMock.EXPECT().Setup(mock.Anything).Return(nil).Once()
	avahiMock.EXPECT().Start().Return().Once()
	avahiMock.EXPECT().GetAPIVersion().Return(int32(0), nil).Once()
	avahiMock.EXPECT().ServiceBrowserNew(
		mock.AnythingOfType("chan avahi.Service"),
		mock.AnythingOfType("chan avahi.Service"),
		int32(-1), int32(-1),
		shipZeroConfServiceType, shipZeroConfDomain,
		uint32(0)).Return(serviceBrowserMock, nil).Once()

	noopCB := func(map[string]string, string, string, []net.IP, int, bool) {}
	assert.True(s.T(), sut.Start(true, noopCB))

	// Give chanListener time to enter its select loop.
	time.Sleep(50 * time.Millisecond)

	// Mock EntryGroupNew to block, simulating a DBus round-trip.
	entryGroupNewEntered := make(chan struct{})
	releaseEntryGroupNew := make(chan struct{})
	avahiMock.On("EntryGroupNew").
		Run(func(_ mock.Arguments) {
			close(entryGroupNewEntered)
			<-releaseEntryGroupNew
		}).Return(entryGroupMock, nil).Once()
	entryGroupMock.On("AddService", mock.Anything, mock.Anything, mock.Anything,
		"test", shipZeroConfServiceType, shipZeroConfDomain,
		"", mock.Anything, mock.Anything).Return(nil).Once()
	entryGroupMock.On("Commit").Return(nil).Once()

	// Start Announce — acquires a.mux and enters the blocked EntryGroupNew.
	announceDone := make(chan error, 1)
	go func() {
		announceDone <- sut.Announce("test", 4729, []string{"txt=1"})
	}()

	select {
	case <-entryGroupNewEntered:
	case <-time.After(2 * time.Second):
		close(releaseEntryGroupNew)
		s.T().Fatal("EntryGroupNew was never entered")
	}

	// Announce now holds a.mux inside EntryGroupNew.
	// Inject a service event — chanListener will pick it up and enter
	// processService → getIfaceIndexes → a.mux.Lock() → BLOCKED.
	//
	// In production this creates a hard deadlock: chanListener is the
	// DBus signal consumer, and with it blocked, the reply to
	// EntryGroupNew can never be delivered.
	testService := avahi.Service{
		Interface: 1,
		Name:      "DiscoveredDuringAnnounce",
		Type:      "_ship._tcp",
		Domain:    "local",
		Aprotocol: -1,
	}

	// ResolveService mock — chanListener calls this if it gets past
	// getIfaceIndexes. With the bug, it never reaches this point.
	resolveReached := make(chan struct{}, 1)
	avahiMock.On("ResolveService",
		mock.Anything, mock.Anything, mock.Anything,
		mock.Anything, mock.Anything, mock.Anything, mock.Anything,
	).Run(func(_ mock.Arguments) {
		select {
		case resolveReached <- struct{}{}:
		default:
		}
	}).Return(avahi.Service{Address: "127.0.0.1"}, nil).Maybe()

	// Send the service. chanListener reads it from addServiceChan,
	// enters processService, and (with the bug) blocks on getIfaceIndexes.
	sut.addServiceChan <- testService

	// EXPECTED (correct behavior): chanListener should fully process the
	// service because a.mux is NOT held during EntryGroupNew.
	select {
	case <-resolveReached:
		// PASS: chanListener was not blocked by Announce's lock.
	case <-time.After(500 * time.Millisecond):
		s.T().Fatal(
			"DEADLOCK: chanListener is blocked in getIfaceIndexes on a.mux " +
				"while Announce holds a.mux inside EntryGroupNew. In production " +
				"chanListener is the DBus signal consumer — blocking it prevents " +
				"the EntryGroupNew reply from being delivered. Neither goroutine " +
				"can make progress. This is the exact deadlock described in the " +
				"goroutine dump. Fix: release a.mux before calling EntryGroupNew, " +
				"AddService, and Commit — the mutex should protect struct-state " +
				"updates only, not span DBus round-trips.")
	}

	close(releaseEntryGroupNew)

	select {
	case <-announceDone:
	case <-time.After(2 * time.Second):
		s.T().Fatal("Announce did not complete")
	}

	// Clean shutdown.
	// Shutdown → Unannounce frees the entry group that Announce stored.
	avahiMock.EXPECT().EntryGroupFree(entryGroupMock).Return().Once()
	avahiMock.EXPECT().ServiceBrowserFree(serviceBrowserMock).Return().Once()
	avahiMock.EXPECT().Shutdown().Return().Once()
	sut.Shutdown()
}

// Test 5: Announce must not block UpdateInterfaces.
// UpdateInterfaces (line 66) acquires a.mux. A caller updating network
// interfaces while Announce is mid-DBus would stall. Announce releases
// a.mux across its DBus phase precisely so that UpdateInterfaces (and
// chanListener) can proceed in parallel.
//
// If UpdateInterfaces mutates a.ifaceIndexes during the DBus phase, the
// in-flight Announce commits with the stale snapshot. That is acceptable
// because the only production caller of UpdateInterfaces is
// reannounceWithNewInterfaces, which pairs every UpdateInterfaces with a
// follow-up Announce (serialized behind announceMux) that overwrites the
// stale commit with fresh data.
func (s *IssuesSuite) Test_AnnounceDoesNotBlockUpdateInterfaces() {
	avahiMock := avahiMocks.NewServerInterface(s.T())
	entryGroupMock := avahiMocks.NewEntryGroupInterface(s.T())

	sut := NewAvahiProvider([]int32{1})
	sut.avServer = avahiMock

	entryGroupNewEntered := make(chan struct{})
	releaseEntryGroupNew := make(chan struct{})

	// Announce snapshots ifaceIndexes=[1], releases a.mux, then blocks in
	// EntryGroupNew (simulating a DBus round-trip). UpdateInterfaces must
	// not stall while Announce is parked here.
	avahiMock.On("EntryGroupNew").Run(func(_ mock.Arguments) {
		close(entryGroupNewEntered)
		<-releaseEntryGroupNew
	}).Return(entryGroupMock, nil).Once()
	entryGroupMock.On("AddService", mock.Anything, mock.Anything, mock.Anything,
		"test", shipZeroConfServiceType, shipZeroConfDomain,
		"", mock.Anything, mock.Anything).Return(nil).Once()
	entryGroupMock.On("Commit").Return(nil).Once()

	announceDone := make(chan error, 1)
	go func() { announceDone <- sut.Announce("test", 4729, []string{"txt=1"}) }()

	select {
	case <-entryGroupNewEntered:
	case <-time.After(2 * time.Second):
		close(releaseEntryGroupNew)
		s.T().Fatal("EntryGroupNew was never entered")
	}

	updateDone := make(chan struct{})
	go func() {
		sut.UpdateInterfaces(nil, []int32{1, 2})
		close(updateDone)
	}()

	select {
	case <-updateDone:
	case <-time.After(500 * time.Millisecond):
		s.T().Fatal(
			"CONTENTION: UpdateInterfaces blocked for >500ms while Announce " +
				"held a.mux inside EntryGroupNew. Interface updates (e.g. from " +
				"the refresh goroutine) must not stall on DBus round-trip latency.")
	}

	close(releaseEntryGroupNew)

	select {
	case err := <-announceDone:
		s.Require().NoError(err)
	case <-time.After(2 * time.Second):
		s.T().Fatal("Announce did not complete")
	}

	// UpdateInterfaces mutated a.ifaceIndexes directly and independently of
	// Announce's snapshot — confirm the post-state reflects that mutation.
	s.Equal([]int32{1, 2}, sut.getIfaceIndexes())
}

// Test 6: Shutdown waits for in-flight Announce via announceMux, then
// proceeds with orderly teardown. Shutdown must not deadlock on a.mux
// (the original bug), but it SHOULD wait for the in-flight Announce to
// finish so that no zombie entry group is left referencing a shut-down
// server.
func (s *IssuesSuite) Test_ShutdownWaitsForInflightAnnounce() {
	avahiMock := avahiMocks.NewServerInterface(s.T())
	entryGroupMock := avahiMocks.NewEntryGroupInterface(s.T())

	sut := NewAvahiProvider([]int32{1})
	sut.avServer = avahiMock
	sut.setupSuccessful = true

	commitEntered := make(chan struct{})
	releaseCommit := make(chan struct{})

	avahiMock.On("EntryGroupNew").Return(entryGroupMock, nil).Once()
	entryGroupMock.On("AddService", mock.Anything, mock.Anything, mock.Anything,
		"test", shipZeroConfServiceType, shipZeroConfDomain,
		"", mock.Anything, mock.Anything).Return(nil).Once()
	entryGroupMock.On("Commit").
		Run(func(_ mock.Arguments) {
			close(commitEntered)
			<-releaseCommit
		}).Return(nil).Once()
	// Shutdown's Unannounce frees the entry group that Announce stored.
	avahiMock.On("EntryGroupFree", entryGroupMock).Return().Once()
	avahiMock.On("Shutdown").Return().Once()

	announceDone := make(chan error, 1)
	go func() { announceDone <- sut.Announce("test", 4729, []string{"txt=1"}) }()

	select {
	case <-commitEntered:
	case <-time.After(2 * time.Second):
		close(releaseCommit)
		s.T().Fatal("Commit was never entered")
	}

	shutdownDone := make(chan struct{})
	go func() {
		sut.Shutdown()
		close(shutdownDone)
	}()

	// Shutdown should NOT complete while Announce is still blocked in
	// Commit, because Shutdown acquires announceMux to wait for it.
	select {
	case <-shutdownDone:
		close(releaseCommit)
		s.T().Fatal(
			"Shutdown completed while Announce was still in-flight. " +
				"announceMux should make Shutdown wait for the in-flight " +
				"Announce to finish before tearing down the server.")
	case <-time.After(200 * time.Millisecond):
		// PASS: Shutdown is correctly waiting for Announce.
	}

	// Release Announce — both should now complete.
	close(releaseCommit)

	select {
	case <-announceDone:
	case <-time.After(2 * time.Second):
		s.T().Fatal("Announce did not complete after Commit was released")
	}

	select {
	case <-shutdownDone:
		// PASS: Shutdown completed after Announce finished.
	case <-time.After(2 * time.Second):
		s.T().Fatal("Shutdown did not complete after Announce finished")
	}
}

// Helper: verify avahi.InterfaceUnspec is what we expect
func init() {
	_ = avahi.InterfaceUnspec // ensure import is used
}
