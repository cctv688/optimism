package dial

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/ethereum-optimism/optimism/op-service/testlog"
	"github.com/ethereum-optimism/optimism/op-service/testutils"
	"github.com/ethereum/go-ethereum/log"
	"github.com/stretchr/testify/require"
)

// endpointProviderTest is a test harness for setting up endpoint provider tests.
type endpointProviderTest struct {
	t             *testing.T
	rollupClients []*testutils.MockRollupClient
	ethClients    []*testutils.MockEthClient
}

// setupEndpointProviderTest sets up the basic structure of the endpoint provider tests.
func setupEndpointProviderTest(t *testing.T, numSequencers int) *endpointProviderTest {
	ept := &endpointProviderTest{
		t:             t,
		rollupClients: make([]*testutils.MockRollupClient, numSequencers),
		ethClients:    make([]*testutils.MockEthClient, numSequencers),
	}

	for i := 0; i < numSequencers; i++ {
		ept.rollupClients[i] = new(testutils.MockRollupClient)
		ept.ethClients[i] = new(testutils.MockEthClient)
	}

	return ept
}

// newActiveL2EndpointProvider constructs a new ActiveL2RollupProvider using the test harness setup.
func (et *endpointProviderTest) newActiveL2RollupProvider(checkDuration time.Duration) (*ActiveL2RollupProvider, error) {
	rollupDialer := func(ctx context.Context, timeout time.Duration, log log.Logger, url string) (RollupClientInterface, error) {
		for i, client := range et.rollupClients {
			if url == fmt.Sprintf("rollup%d", i) {
				return client, nil
			}
		}
		return nil, fmt.Errorf("unknown test url: %s", url)
	}

	// make the "URLs"
	rollupUrls := make([]string, len(et.rollupClients))
	for i := range et.rollupClients {
		rollupUrl := fmt.Sprintf("rollup%d", i)
		rollupUrls[i] = rollupUrl
	}

	return newActiveL2RollupProvider(
		context.Background(),
		rollupUrls,
		checkDuration,
		1*time.Minute,
		testlog.Logger(et.t, log.LvlDebug),
		rollupDialer,
	)
}

// newActiveL2EndpointProvider constructs a new ActiveL2EndpointProvider using the test harness setup.
func (et *endpointProviderTest) newActiveL2EndpointProvider(checkDuration time.Duration) (*ActiveL2EndpointProvider, error) {
	mockRollupDialer := func(ctx context.Context, timeout time.Duration, log log.Logger, url string) (RollupClientInterface, error) {
		for i, client := range et.rollupClients {
			if url == fmt.Sprintf("rollup%d", i) {
				return client, nil
			}
		}
		return nil, fmt.Errorf("unknown test url: %s", url)
	}

	mockEthDialer := func(ctx context.Context, timeout time.Duration, log log.Logger, url string) (EthClientInterface, error) {
		for i, client := range et.ethClients {
			if url == fmt.Sprintf("eth%d", i) {
				return client, nil
			}
		}
		return nil, fmt.Errorf("unknown test url: %s", url)
	}

	// make the "URLs"
	rollupUrls := make([]string, len(et.rollupClients))
	for i := range et.rollupClients {
		rollupUrl := fmt.Sprintf("rollup%d", i)
		rollupUrls[i] = rollupUrl
	}
	ethUrls := make([]string, len(et.ethClients))
	for i := range et.ethClients {
		ethUrl := fmt.Sprintf("eth%d", i)
		ethUrls[i] = ethUrl
	}

	return newActiveL2EndpointProvider(
		context.Background(),
		ethUrls,
		rollupUrls,
		checkDuration,
		1*time.Minute,
		testlog.Logger(et.t, log.LvlDebug),
		mockEthDialer,
		mockRollupDialer,
	)
}

// TestRollupProvider_FailoverOnInactiveSequencer verifies that the ActiveL2RollupProvider
// will switch to the next provider if the current one becomes inactive.
func TestRollupProvider_FailoverOnInactiveSequencer(t *testing.T) {
	ept := setupEndpointProviderTest(t, 2)
	primarySequencer, secondarySequencer := ept.rollupClients[0], ept.rollupClients[1]

	primarySequencer.ExpectSequencerActive(true, nil) // respond true once on creation
	primarySequencer.ExpectSequencerActive(true, nil) // respond true again when the test calls `RollupClient()` the first time

	shortCheckDuration := 1 * time.Microsecond
	rollupProvider, err := ept.newActiveL2RollupProvider(shortCheckDuration)
	require.NoError(t, err)

	firstSequencerUsed, err := rollupProvider.RollupClient(context.Background())
	require.NoError(t, err)
	require.Same(t, primarySequencer, firstSequencerUsed)

	primarySequencer.ExpectSequencerActive(false, nil) // become inactive after that
	primarySequencer.ExpectClose()
	secondarySequencer.ExpectSequencerActive(true, nil)
	secondSequencerUsed, err := rollupProvider.RollupClient(context.Background())
	require.NoError(t, err)
	require.Same(t, secondarySequencer, secondSequencerUsed)
}

// TestEndpointProvider_FailoverOnInactiveSequencer verifies that the ActiveL2EndpointProvider
// will switch to the next provider if the current one becomes inactive.
func TestEndpointProvider_FailoverOnInactiveSequencer(t *testing.T) {
	// as TestActiveSequencerFailoverBehavior_RollupProviders,
	// but ensure the added `EthClient()` method also triggers the failover.
	ept := setupEndpointProviderTest(t, 2)
	primarySequencer, secondarySequencer := ept.rollupClients[0], ept.rollupClients[1]
	primarySequencer.ExpectSequencerActive(true, nil) // primary sequencer gets hit once on creation: embedded call of `RollupClient()`
	primarySequencer.ExpectSequencerActive(true, nil) // primary sequencer gets hit twice on creation: implicit call of `EthClient()`
	primarySequencer.ExpectSequencerActive(true, nil) // respond true again when the test calls `EthClient()` the first time

	shortCheckDuration := 1 * time.Microsecond
	activeProvider, err := ept.newActiveL2EndpointProvider(shortCheckDuration)
	require.NoError(t, err)

	firstSequencerUsed, err := activeProvider.EthClient(context.Background())
	require.NoError(t, err)
	require.Same(t, ept.ethClients[0], firstSequencerUsed)

	primarySequencer.ExpectSequencerActive(false, nil) // become inactive after that
	secondarySequencer.ExpectSequencerActive(true, nil)
	primarySequencer.ExpectClose()
	ept.ethClients[0].ExpectClose() // we close the ethclient when we switch over to the next sequencer
	secondSequencerUsed, err := activeProvider.EthClient(context.Background())
	require.NoError(t, err)
	require.Same(t, ept.ethClients[1], secondSequencerUsed)
}

// TestRollupProvider_FailoverOnErroredSequencer verifies that the ActiveL2RollupProvider
// will switch to the next provider if the current one returns an error.
func TestRollupProvider_FailoverOnErroredSequencer(t *testing.T) {
	ept := setupEndpointProviderTest(t, 2)
	primarySequencer, secondarySequencer := ept.rollupClients[0], ept.rollupClients[1]

	primarySequencer.ExpectSequencerActive(true, nil) // respond true once on creation
	primarySequencer.ExpectSequencerActive(true, nil) // respond true again when the test calls `RollupClient()` the first time

	shortCheckDuration := 1 * time.Microsecond
	rollupProvider, err := ept.newActiveL2RollupProvider(shortCheckDuration)
	require.NoError(t, err)

	firstSequencerUsed, err := rollupProvider.RollupClient(context.Background())
	require.NoError(t, err)
	require.Same(t, primarySequencer, firstSequencerUsed)

	primarySequencer.ExpectSequencerActive(true, fmt.Errorf("a test error")) // error-out after that
	primarySequencer.ExpectClose()
	secondarySequencer.ExpectSequencerActive(true, nil)
	secondSequencerUsed, err := rollupProvider.RollupClient(context.Background())
	require.NoError(t, err)
	require.Same(t, secondarySequencer, secondSequencerUsed)
}

// TestEndpointProvider_FailoverOnErroredSequencer verifies that the ActiveL2EndpointProvider
// will switch to the next provider if the current one returns an error.
func TestEndpointProvider_FailoverOnErroredSequencer(t *testing.T) {
	ept := setupEndpointProviderTest(t, 2)
	primarySequencer, secondarySequencer := ept.rollupClients[0], ept.rollupClients[1]
	primaryEthClient, secondaryEthClient := ept.ethClients[0], ept.ethClients[1]

	primarySequencer.ExpectSequencerActive(true, nil) // primary sequencer gets hit once on creation: embedded call of `RollupClient()`
	primarySequencer.ExpectSequencerActive(true, nil) // primary sequencer gets hit twice on creation: implicit call of `EthClient()`

	shortCheckDuration := 1 * time.Microsecond
	activeProvider, err := ept.newActiveL2EndpointProvider(shortCheckDuration)
	require.NoError(t, err)

	primarySequencer.ExpectSequencerActive(true, nil) // respond true again when the test calls `EthClient()` the first time
	firstSequencerUsed, err := activeProvider.EthClient(context.Background())
	require.NoError(t, err)
	require.Same(t, primaryEthClient, firstSequencerUsed)

	primarySequencer.ExpectSequencerActive(true, fmt.Errorf("a test error")) // error out after that
	primarySequencer.ExpectClose()
	primaryEthClient.ExpectClose()
	secondarySequencer.ExpectSequencerActive(true, nil)

	secondSequencerUsed, err := activeProvider.EthClient(context.Background())
	require.NoError(t, err)
	require.Same(t, secondaryEthClient, secondSequencerUsed)
}

// TestRollupProvider_NoExtraCheckOnActiveSequencer verifies that the ActiveL2RollupProvider
// does not change if the current sequencer is active.
func TestRollupProvider_NoExtraCheckOnActiveSequencer(t *testing.T) {
	ept := setupEndpointProviderTest(t, 2)
	primarySequencer := ept.rollupClients[0]

	primarySequencer.ExpectSequencerActive(true, nil) // default test provider, which always checks, checks Active on creation

	shortCheckDuration := 1 * time.Microsecond
	rollupProvider, err := ept.newActiveL2RollupProvider(shortCheckDuration)
	require.NoError(t, err)
	require.Same(t, primarySequencer, rollupProvider.currentRollupClient)

	primarySequencer.ExpectSequencerActive(true, nil) // default test provider, which always checks, checks again on RollupClient()

	firstSequencerUsed, err := rollupProvider.RollupClient(context.Background())
	require.NoError(t, err)
	require.Same(t, primarySequencer, firstSequencerUsed)
}

// TestEndpointProvider_NoExtraCheckOnActiveSequencer verifies that the ActiveL2EndpointProvider
// does not change if the current sequencer is active.
func TestEndpointProvider_NoExtraCheckOnActiveSequencer(t *testing.T) {
	ept := setupEndpointProviderTest(t, 2)
	primarySequencer := ept.rollupClients[0]

	primarySequencer.ExpectSequencerActive(true, nil) // default test provider, which always checks, checks Active twice on creation (once for internal RollupClient() call)
	primarySequencer.ExpectSequencerActive(true, nil) // default test provider, which always checks, checks Active twice on creation (once for internal EthClient() call)

	shortCheckDuration := 1 * time.Microsecond
	endpointProvider, err := ept.newActiveL2EndpointProvider(shortCheckDuration)
	require.NoError(t, err)
	require.Same(t, ept.ethClients[0], endpointProvider.currentEthClient)

	primarySequencer.ExpectSequencerActive(true, nil) // default test provider, which always checks, checks again on EthClient()

	firstEthClientUsed, err := endpointProvider.EthClient(context.Background())
	require.NoError(t, err)
	require.Same(t, ept.ethClients[0], firstEthClientUsed)
}

// TestRollupProvider_FailoverAndReturn verifies the ActiveL2RollupProvider's ability to
// failover and then return to the primary sequencer once it becomes active again.
func TestRollupProvider_FailoverAndReturn(t *testing.T) {
	ept := setupEndpointProviderTest(t, 2)
	primarySequencer, secondarySequencer := ept.rollupClients[0], ept.rollupClients[1]

	// Primary initially active
	primarySequencer.ExpectSequencerActive(true, nil)
	shortCheckDuration := 1 * time.Microsecond
	rollupProvider, err := ept.newActiveL2RollupProvider(shortCheckDuration)
	require.NoError(t, err)

	// Primary becomes inactive, secondary active
	primarySequencer.ExpectSequencerActive(false, nil)
	primarySequencer.ExpectClose()
	secondarySequencer.ExpectSequencerActive(true, nil)

	// Fails over to secondary
	secondSequencerUsed, err := rollupProvider.RollupClient(context.Background())
	require.NoError(t, err)
	require.Same(t, secondarySequencer, secondSequencerUsed)

	// Primary becomes active again, secondary becomes inactive
	primarySequencer.ExpectSequencerActive(true, nil)
	secondarySequencer.ExpectSequencerActive(false, nil)
	secondarySequencer.ExpectClose()

	// Should return to primary
	thirdSequencerUsed, err := rollupProvider.RollupClient(context.Background())
	require.NoError(t, err)
	require.Same(t, primarySequencer, thirdSequencerUsed)
}

// TestEndpointProvider_FailoverAndReturn verifies the ActiveL2EndpointProvider's ability to
// failover and then return to the primary sequencer once it becomes active again.
func TestEndpointProvider_FailoverAndReturn(t *testing.T) {
	ept := setupEndpointProviderTest(t, 2)
	primarySequencer, secondarySequencer := ept.rollupClients[0], ept.rollupClients[1]

	// Primary initially active
	primarySequencer.ExpectSequencerActive(true, nil)
	primarySequencer.ExpectSequencerActive(true, nil) // see comment in other tests about why we expect this twice
	shortCheckDuration := 1 * time.Microsecond
	endpointProvider, err := ept.newActiveL2EndpointProvider(shortCheckDuration)
	require.NoError(t, err)

	// Primary becomes inactive, secondary active
	primarySequencer.ExpectSequencerActive(false, nil)
	primarySequencer.ExpectClose()
	ept.ethClients[0].ExpectClose()
	secondarySequencer.ExpectSequencerActive(true, nil)

	// Fails over to secondary
	secondEthClient, err := endpointProvider.EthClient(context.Background())
	require.NoError(t, err)
	require.Same(t, ept.ethClients[1], secondEthClient)

	// Primary becomes active again, secondary becomes inactive
	primarySequencer.ExpectSequencerActive(true, nil)
	secondarySequencer.ExpectSequencerActive(false, nil)
	secondarySequencer.ExpectClose()
	ept.ethClients[1].ExpectClose()

	// // Should return to primary
	thirdSequencerUsed, err := endpointProvider.EthClient(context.Background())
	require.NoError(t, err)
	require.Same(t, ept.ethClients[0], thirdSequencerUsed)
}

// TestRollupProvider_InitialActiveSequencerSelection verifies that the ActiveL2RollupProvider
// selects the active sequencer correctly at the time of creation.
func TestRollupProvider_InitialActiveSequencerSelection(t *testing.T) {
	ept := setupEndpointProviderTest(t, 2)
	primarySequencer := ept.rollupClients[0]

	// Primary active at creation
	primarySequencer.ExpectSequencerActive(true, nil)

	shortCheckDuration := 1 * time.Microsecond
	rollupProvider, err := ept.newActiveL2RollupProvider(shortCheckDuration)
	require.NoError(t, err)

	// Check immediately after creation without additional Active check
	require.Same(t, primarySequencer, rollupProvider.currentRollupClient)
}

// TestEndpointProvider_InitialActiveSequencerSelection verifies that the ActiveL2EndpointProvider
// selects the active sequencer correctly at the time of creation.
func TestEndpointProvider_InitialActiveSequencerSelection(t *testing.T) {
	ept := setupEndpointProviderTest(t, 2)
	primarySequencer := ept.rollupClients[0]

	// Primary active at creation
	primarySequencer.ExpectSequencerActive(true, nil)
	primarySequencer.ExpectSequencerActive(true, nil) // see comment in other tests about why we expect this twice

	shortCheckDuration := 1 * time.Microsecond
	rollupProvider, err := ept.newActiveL2EndpointProvider(shortCheckDuration)
	require.NoError(t, err)

	// Check immediately after creation without additional Active check
	require.Same(t, primarySequencer, rollupProvider.currentRollupClient)
}

// TestRollupProvider_FailOnAllInactiveSequencers verifies that the ActiveL2RollupProvider
// fails to be created when all sequencers are inactive.
func TestRollupProvider_FailOnAllInactiveSequencers(t *testing.T) {
	ept := setupEndpointProviderTest(t, 2)

	// All sequencers are inactive
	for _, sequencer := range ept.rollupClients {
		sequencer.ExpectSequencerActive(false, nil)
		sequencer.ExpectClose()
	}

	shortCheckDuration := 1 * time.Microsecond
	_, err := ept.newActiveL2RollupProvider(shortCheckDuration)
	require.Error(t, err) // Expect an error as all sequencers are inactive
}

// TestEndpointProvider_FailOnAllInactiveSequencers verifies that the ActiveL2EndpointProvider
// fails to be created when all sequencers are inactive.
func TestEndpointProvider_FailOnAllInactiveSequencers(t *testing.T) {
	ept := setupEndpointProviderTest(t, 2)
	ept.rollupClients[0].ExpectSequencerActive(false, nil) // see comment in other tests about why we expect this an extra time

	// All sequencers are inactive
	for _, sequencer := range ept.rollupClients {
		sequencer.ExpectSequencerActive(false, nil)
		sequencer.ExpectClose()
	}

	shortCheckDuration := 1 * time.Microsecond
	_, err := ept.newActiveL2EndpointProvider(shortCheckDuration)
	require.Error(t, err) // Expect an error as all sequencers are inactive
}

// TestRollupProvider_FailOnAllErroredSequencers verifies that the ActiveL2RollupProvider
// fails to create when all sequencers return an error.
func TestRollupProvider_FailOnAllErroredSequencers(t *testing.T) {
	ept := setupEndpointProviderTest(t, 2)

	// All sequencers are inactive
	for _, sequencer := range ept.rollupClients {
		sequencer.ExpectSequencerActive(true, fmt.Errorf("a test error"))
		sequencer.ExpectClose()
	}

	shortCheckDuration := 1 * time.Microsecond
	_, err := ept.newActiveL2RollupProvider(shortCheckDuration)
	require.Error(t, err) // Expect an error as all sequencers are inactive
}

// TestEndpointProvider_FailOnAllErroredSequencers verifies that the ActiveL2EndpointProvider
// fails to create when all sequencers return an error.
func TestEndpointProvider_FailOnAllErroredSequencers(t *testing.T) {
	ept := setupEndpointProviderTest(t, 2)
	ept.rollupClients[0].ExpectSequencerActive(true, fmt.Errorf("a test error")) // see comment in other tests about why we expect this an extra time

	// All sequencers are inactive
	for _, sequencer := range ept.rollupClients {
		sequencer.ExpectSequencerActive(true, fmt.Errorf("a test error"))
		sequencer.ExpectClose()
	}

	shortCheckDuration := 1 * time.Microsecond
	_, err := ept.newActiveL2EndpointProvider(shortCheckDuration)
	require.Error(t, err) // Expect an error as all sequencers are inactive
}

// TestRollupProvider_LongCheckDuration verifies the behavior of ActiveL2RollupProvider with a long check duration.
func TestRollupProvider_LongCheckDuration(t *testing.T) {
	ept := setupEndpointProviderTest(t, 2)
	primarySequencer := ept.rollupClients[0]

	longCheckDuration := 1 * time.Hour
	primarySequencer.ExpectSequencerActive(true, nil) // Active check on creation

	rollupProvider, err := ept.newActiveL2RollupProvider(longCheckDuration)
	require.NoError(t, err)

	// Should return the same client without extra checks
	firstSequencerUsed, err := rollupProvider.RollupClient(context.Background())
	require.NoError(t, err)
	require.Same(t, primarySequencer, firstSequencerUsed)

	secondSequencerUsed, err := rollupProvider.RollupClient(context.Background())
	require.NoError(t, err)
	require.Same(t, primarySequencer, secondSequencerUsed)
}

// TestEndpointProvider_LongCheckDuration verifies the behavior of ActiveL2EndpointProvider with a long check duration.
func TestEndpointProvider_LongCheckDuration(t *testing.T) {
	ept := setupEndpointProviderTest(t, 2)
	primarySequencer := ept.rollupClients[0]

	longCheckDuration := 1 * time.Hour
	primarySequencer.ExpectSequencerActive(true, nil) // Active check on creation

	endpointProvider, err := ept.newActiveL2EndpointProvider(longCheckDuration)
	require.NoError(t, err)

	// Should return the same client without extra checks
	firstEthClient, err := endpointProvider.EthClient(context.Background())
	require.NoError(t, err)
	require.Same(t, ept.ethClients[0], firstEthClient)

	secondEthClient, err := endpointProvider.EthClient(context.Background())
	require.NoError(t, err)
	require.Same(t, ept.ethClients[0], secondEthClient)
}
