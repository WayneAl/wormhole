package guardiand

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"encoding/hex"
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/certusone/wormhole/node/pkg/common"
	gossipv1 "github.com/certusone/wormhole/node/pkg/proto/gossip/v1"
	"github.com/wormhole-foundation/wormhole/sdk/vaa"

	ethCommon "github.com/ethereum/go-ethereum/common"
	ethCrypto "github.com/ethereum/go-ethereum/crypto"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.uber.org/zap"
)

const (
	testSigner = "beFA429d57cD18b7F8A4d91A2da9AB4AF05d0FBe"

	// Magic retry values used to cause special behavior in the watchers.
	fatalError  = math.MaxInt
	ignoreQuery = math.MaxInt - 1

	// Speed things up for testing purposes.
	requestTimeoutForTest = 100 * time.Millisecond
	retryIntervalForTest  = 10 * time.Millisecond
	pollIntervalForTest   = 5 * time.Millisecond
)

var (
	nonce = uint32(0)

	watcherChainsForTest = []vaa.ChainID{vaa.ChainIDPolygon, vaa.ChainIDBSC}
)

// createPerChainQueryForTesting creates a per chain query for use in tests. The To and Data fields are meaningless gibberish, not ABI.
func createPerChainQueryForTesting(
	chainId vaa.ChainID,
	block string,
	numCalls int,
) *common.PerChainQueryRequest {
	callData := []*common.EthCallData{}
	for count := 0; count < numCalls; count++ {
		callData = append(callData, &common.EthCallData{
			To:   []byte(fmt.Sprintf("%-20s", fmt.Sprintf("To for %d:%d", chainId, count))),
			Data: []byte(fmt.Sprintf("CallData for %d:%d", chainId, count)),
		})
	}

	callRequest := &common.EthCallQueryRequest{
		BlockId:  block,
		CallData: callData,
	}

	return &common.PerChainQueryRequest{
		ChainId: chainId,
		Query:   callRequest,
	}
}

// createSignedQueryRequestForTesting creates a query request object and signs it using the specified key.
func createSignedQueryRequestForTesting(
	sk *ecdsa.PrivateKey,
	perChainQueries []*common.PerChainQueryRequest,
) (*gossipv1.SignedQueryRequest, *common.QueryRequest) {
	nonce += 1
	queryRequest := &common.QueryRequest{
		Nonce:           nonce,
		PerChainQueries: perChainQueries,
	}

	queryRequestBytes, err := queryRequest.Marshal()
	if err != nil {
		panic(err)
	}

	digest := common.QueryRequestDigest(common.UnsafeDevNet, queryRequestBytes)
	sig, err := ethCrypto.Sign(digest.Bytes(), sk)
	if err != nil {
		panic(err)
	}

	signedQueryRequest := &gossipv1.SignedQueryRequest{
		QueryRequest: queryRequestBytes,
		Signature:    sig,
	}

	return signedQueryRequest, queryRequest
}

// createExpectedResultsForTest generates an array of the results expected for a request. These results are returned by the watcher, and used to validate the response.
func createExpectedResultsForTest(perChainQueries []*common.PerChainQueryRequest) []common.PerChainQueryResponse {
	expectedResults := []common.PerChainQueryResponse{}
	for _, pcq := range perChainQueries {
		switch req := pcq.Query.(type) {
		case *common.EthCallQueryRequest:
			now := time.Now()
			blockNum, err := strconv.ParseUint(strings.TrimPrefix(req.BlockId, "0x"), 16, 64)
			if err != nil {
				panic("invalid blockNum!")
			}
			resp := &common.EthCallQueryResponse{
				BlockNumber: blockNum,
				Hash:        ethCommon.HexToHash("0x9999bac44d09a7f69ee7941819b0a19c59ccb1969640cc513be09ef95ed2d8e2"),
				Time:        timeForTest(timeForTest(now)),
				Results:     [][]byte{},
			}
			for _, cd := range req.CallData {
				resp.Results = append(resp.Results, []byte(hex.EncodeToString(cd.To)+":"+hex.EncodeToString(cd.Data)))
			}
			expectedResults = append(expectedResults, common.PerChainQueryResponse{
				ChainId:  pcq.ChainId,
				Response: resp,
			})

		default:
			panic("Invalid call data type!")
		}
	}

	return expectedResults
}

// validateResponseForTest performs validation on the responses generated by these tests. Note that it is not a generalized validate function.
func validateResponseForTest(
	t *testing.T,
	response *common.QueryResponsePublication,
	signedRequest *gossipv1.SignedQueryRequest,
	queryRequest *common.QueryRequest,
	expectedResults []common.PerChainQueryResponse,
) bool {
	require.NotNil(t, response)
	require.True(t, common.SignedQueryRequestEqual(signedRequest, response.Request))
	require.Equal(t, len(queryRequest.PerChainQueries), len(response.PerChainResponses))
	require.True(t, bytes.Equal(response.Request.Signature, signedRequest.Signature))
	require.Equal(t, len(response.PerChainResponses), len(expectedResults))
	for idx := range response.PerChainResponses {
		require.True(t, response.PerChainResponses[idx].Equal(&expectedResults[idx]))
	}

	return true
}

// A timestamp has nanos, but we only marshal down to micros, so trim our time to micros for testing purposes.
func timeForTest(t time.Time) time.Time {
	return time.UnixMicro(t.UnixMicro())
}

func TestCcqParseAllowedRequestersSuccess(t *testing.T) {
	ccqAllowedRequestersList, err := ccqParseAllowedRequesters(testSigner)
	require.NoError(t, err)
	require.NotNil(t, ccqAllowedRequestersList)
	require.Equal(t, 1, len(ccqAllowedRequestersList))

	_, exists := ccqAllowedRequestersList[ethCommon.BytesToAddress(ethCommon.Hex2Bytes(testSigner))]
	require.True(t, exists)
	_, exists = ccqAllowedRequestersList[ethCommon.BytesToAddress(ethCommon.Hex2Bytes("beFA429d57cD18b7F8A4d91A2da9AB4AF05d0FBf"))]
	require.False(t, exists)

	ccqAllowedRequestersList, err = ccqParseAllowedRequesters("beFA429d57cD18b7F8A4d91A2da9AB4AF05d0FBe,beFA429d57cD18b7F8A4d91A2da9AB4AF05d0FBf")
	require.NoError(t, err)
	require.NotNil(t, ccqAllowedRequestersList)
	require.Equal(t, 2, len(ccqAllowedRequestersList))

	_, exists = ccqAllowedRequestersList[ethCommon.BytesToAddress(ethCommon.Hex2Bytes(testSigner))]
	require.True(t, exists)
	_, exists = ccqAllowedRequestersList[ethCommon.BytesToAddress(ethCommon.Hex2Bytes("beFA429d57cD18b7F8A4d91A2da9AB4AF05d0FBf"))]
	require.True(t, exists)
}

func TestCcqParseAllowedRequestersFailsIfParameterEmpty(t *testing.T) {
	ccqAllowedRequestersList, err := ccqParseAllowedRequesters("")
	require.Error(t, err)
	require.Nil(t, ccqAllowedRequestersList)

	ccqAllowedRequestersList, err = ccqParseAllowedRequesters(",")
	require.Error(t, err)
	require.Nil(t, ccqAllowedRequestersList)
}

func TestCcqParseAllowedRequestersFailsIfInvalidParameter(t *testing.T) {
	ccqAllowedRequestersList, err := ccqParseAllowedRequesters("Hello")
	require.Error(t, err)
	require.Nil(t, ccqAllowedRequestersList)
}

// mockData is the data structure used to mock up the query handler environment.
type mockData struct {
	sk *ecdsa.PrivateKey

	signedQueryReqReadC  <-chan *gossipv1.SignedQueryRequest
	signedQueryReqWriteC chan<- *gossipv1.SignedQueryRequest

	chainQueryReqC map[vaa.ChainID]chan *common.PerChainQueryInternal

	queryResponseReadC  <-chan *common.PerChainQueryResponseInternal
	queryResponseWriteC chan<- *common.PerChainQueryResponseInternal

	queryResponsePublicationReadC  <-chan *common.QueryResponsePublication
	queryResponsePublicationWriteC chan<- *common.QueryResponsePublication

	mutex                    sync.Mutex
	queryResponsePublication *common.QueryResponsePublication
	expectedResults          []common.PerChainQueryResponse
	requestsPerChain         map[vaa.ChainID]int
	retriesPerChain          map[vaa.ChainID]int
}

// resetState() is used to reset mock data between queries in the same test.
func (md *mockData) resetState() {
	md.mutex.Lock()
	defer md.mutex.Unlock()
	md.queryResponsePublication = nil
	md.expectedResults = nil
	md.requestsPerChain = make(map[vaa.ChainID]int)
	md.retriesPerChain = make(map[vaa.ChainID]int)
}

// setExpectedResults sets the results to be returned by the watchers.
func (md *mockData) setExpectedResults(expectedResults []common.PerChainQueryResponse) {
	md.mutex.Lock()
	defer md.mutex.Unlock()
	md.expectedResults = expectedResults
}

// setRetries allows a test to specify how many times a given watcher should retry before returning success.
// If the count is the special value `fatalError`, the watcher will return common.QueryFatalError.
func (md *mockData) setRetries(chainId vaa.ChainID, count int) {
	md.mutex.Lock()
	defer md.mutex.Unlock()
	md.retriesPerChain[chainId] = count
}

// incrementRequestsPerChainAlreadyLocked is used by the watchers to keep track of how many times they were invoked in a given test.
func (md *mockData) incrementRequestsPerChainAlreadyLocked(chainId vaa.ChainID) {
	if val, exists := md.requestsPerChain[chainId]; exists {
		md.requestsPerChain[chainId] = val + 1
	} else {
		md.requestsPerChain[chainId] = 1
	}
}

// getQueryResponsePublication returns the latest query response publication received by the mock.
func (md *mockData) getQueryResponsePublication() *common.QueryResponsePublication {
	md.mutex.Lock()
	defer md.mutex.Unlock()
	return md.queryResponsePublication
}

// getRequestsPerChain returns the count of the number of times the given watcher was invoked in a given test.
func (md *mockData) getRequestsPerChain(chainId vaa.ChainID) int {
	md.mutex.Lock()
	defer md.mutex.Unlock()
	if ret, exists := md.requestsPerChain[chainId]; exists {
		return ret
	}
	return 0
}

// shouldIgnoreAlreadyLocked is used by the watchers to see if they should ignore a query (causing a retry).
func (md *mockData) shouldIgnoreAlreadyLocked(chainId vaa.ChainID) bool {
	if val, exists := md.retriesPerChain[chainId]; exists {
		if val == ignoreQuery {
			delete(md.retriesPerChain, chainId)
			return true
		}
	}
	return false
}

// getStatusAlreadyLocked is used by the watchers to determine what query status they should return, based on the `retriesPerChain`.
func (md *mockData) getStatusAlreadyLocked(chainId vaa.ChainID) common.QueryStatus {
	if val, exists := md.retriesPerChain[chainId]; exists {
		if val == fatalError {
			return common.QueryFatalError
		}
		val -= 1
		if val > 0 {
			md.retriesPerChain[chainId] = val
		} else {
			delete(md.retriesPerChain, chainId)
		}
		return common.QueryRetryNeeded
	}
	return common.QuerySuccess
}

// createQueryHandlerForTest creates the query handler mock environment, including the set of watchers and the response listener.
// Most tests will use this function to set up the mock.
func createQueryHandlerForTest(t *testing.T, ctx context.Context, logger *zap.Logger, chains []vaa.ChainID) *mockData {
	md := createQueryHandlerForTestWithoutPublisher(t, ctx, logger, chains)
	md.startResponseListener(ctx)
	return md
}

// createQueryHandlerForTestWithoutPublisher creates the query handler mock environment, including the set of watchers but not the response listener.
// This function can be invoked directly to test retries of response publication (by delaying the start of the response listener).
func createQueryHandlerForTestWithoutPublisher(t *testing.T, ctx context.Context, logger *zap.Logger, chains []vaa.ChainID) *mockData {
	md := mockData{}
	var err error

	*unsafeDevMode = true
	md.sk, err = loadGuardianKey("../../hack/query/dev.guardian.key")
	require.NoError(t, err)
	require.NotNil(t, md.sk)

	ccqAllowedRequestersList, err := ccqParseAllowedRequesters(testSigner)
	require.NoError(t, err)

	// Inbound observation requests from the p2p service (for all chains)
	md.signedQueryReqReadC, md.signedQueryReqWriteC = makeChannelPair[*gossipv1.SignedQueryRequest](common.SignedQueryRequestChannelSize)

	// Per-chain query requests
	md.chainQueryReqC = make(map[vaa.ChainID]chan *common.PerChainQueryInternal)
	for _, chainId := range chains {
		md.chainQueryReqC[chainId] = make(chan *common.PerChainQueryInternal)
	}

	// Query responses from watchers to query handler aggregated across all chains
	md.queryResponseReadC, md.queryResponseWriteC = makeChannelPair[*common.PerChainQueryResponseInternal](0)

	// Query responses from query handler to p2p
	md.queryResponsePublicationReadC, md.queryResponsePublicationWriteC = makeChannelPair[*common.QueryResponsePublication](0)

	md.resetState()

	go handleQueryRequestsImpl(ctx, logger, md.signedQueryReqReadC, md.chainQueryReqC, ccqAllowedRequestersList,
		md.queryResponseReadC, md.queryResponsePublicationWriteC, common.GoTest, requestTimeoutForTest, retryIntervalForTest)

	// Create a routine for each configured watcher. It will take a per chain query and return the corresponding expected result.
	// It also pegs a counter of the number of requests the watcher received, for verification purposes.
	for chainId := range md.chainQueryReqC {
		go func(chainId vaa.ChainID, chainQueryReqC <-chan *common.PerChainQueryInternal) {
			for {
				select {
				case <-ctx.Done():
					return
				case pcqr := <-chainQueryReqC:
					require.Equal(t, chainId, pcqr.Request.ChainId)
					md.mutex.Lock()
					md.incrementRequestsPerChainAlreadyLocked(chainId)
					if md.shouldIgnoreAlreadyLocked(chainId) {
						logger.Info("watcher ignoring query", zap.String("chainId", chainId.String()), zap.Int("requestIdx", pcqr.RequestIdx))
					} else {
						results := md.expectedResults[pcqr.RequestIdx].Response
						status := md.getStatusAlreadyLocked(chainId)
						logger.Info("watcher returning", zap.String("chainId", chainId.String()), zap.Int("requestIdx", pcqr.RequestIdx), zap.Int("status", int(status)))
						queryResponse := common.CreatePerChainQueryResponseInternal(pcqr.RequestID, pcqr.RequestIdx, pcqr.Request.ChainId, status, results)
						md.queryResponseWriteC <- queryResponse
					}
					md.mutex.Unlock()
				}
			}
		}(chainId, md.chainQueryReqC[chainId])
	}

	return &md
}

// startResponseListener starts the response listener routine. It is called as part of the standard mock environment set up. Or, it can be used
// along with `createQueryHandlerForTestWithoutPublisher“ to test retries of response publication (by delaying the start of the response listener).
func (md *mockData) startResponseListener(ctx context.Context) {
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case qrp := <-md.queryResponsePublicationReadC:
				md.mutex.Lock()
				md.queryResponsePublication = qrp
				md.mutex.Unlock()
			}
		}
	}()
}

// waitForResponse is used by the tests to wait for a response publication. It will eventually timeout if the query fails.
func (md *mockData) waitForResponse() *common.QueryResponsePublication {
	for count := 0; count < 50; count++ {
		time.Sleep(pollIntervalForTest)
		ret := md.getQueryResponsePublication()
		if ret != nil {
			return ret
		}
	}
	return nil
}

// TestInvalidQueries tests all the obvious reasons why a query may fail (aside from watcher failures).
func TestInvalidQueries(t *testing.T) {
	ctx := context.Background()
	logger, err := zap.NewDevelopment()
	require.NoError(t, err)

	md := createQueryHandlerForTest(t, ctx, logger, watcherChainsForTest)

	var perChainQueries []*common.PerChainQueryRequest
	var signedQueryRequest *gossipv1.SignedQueryRequest

	// Query with a bad signature should fail.
	md.resetState()
	perChainQueries = []*common.PerChainQueryRequest{createPerChainQueryForTesting(vaa.ChainIDPolygon, "0x28d9630", 2)}
	signedQueryRequest, _ = createSignedQueryRequestForTesting(md.sk, perChainQueries)
	signedQueryRequest.Signature[0] += 1 // Corrupt the signature.
	md.signedQueryReqWriteC <- signedQueryRequest
	require.Nil(t, md.waitForResponse())

	// Query for an unsupported chain should fail. The supported chains are defined in supportedChains in query.go
	md.resetState()
	perChainQueries = []*common.PerChainQueryRequest{createPerChainQueryForTesting(vaa.ChainIDAlgorand, "0x28d9630", 2)}
	signedQueryRequest, _ = createSignedQueryRequestForTesting(md.sk, perChainQueries)
	md.signedQueryReqWriteC <- signedQueryRequest
	require.Nil(t, md.waitForResponse())

	// Query for a chain that supports queries but that is not in the watcher channel map should fail.
	md.resetState()
	perChainQueries = []*common.PerChainQueryRequest{createPerChainQueryForTesting(vaa.ChainIDSepolia, "0x28d9630", 2)}
	signedQueryRequest, _ = createSignedQueryRequestForTesting(md.sk, perChainQueries)
	md.signedQueryReqWriteC <- signedQueryRequest
	require.Nil(t, md.waitForResponse())
}

func TestSingleQueryShouldSucceed(t *testing.T) {
	ctx := context.Background()
	logger, err := zap.NewDevelopment()
	require.NoError(t, err)

	md := createQueryHandlerForTest(t, ctx, logger, watcherChainsForTest)

	// Create the request and the expected results. Give the expected results to the mock.
	perChainQueries := []*common.PerChainQueryRequest{createPerChainQueryForTesting(vaa.ChainIDPolygon, "0x28d9630", 2)}
	signedQueryRequest, queryRequest := createSignedQueryRequestForTesting(md.sk, perChainQueries)
	expectedResults := createExpectedResultsForTest(queryRequest.PerChainQueries)
	md.setExpectedResults(expectedResults)

	// Submit the query request to the handler.
	md.signedQueryReqWriteC <- signedQueryRequest

	// Wait until we receive a response or timeout.
	queryResponsePublication := md.waitForResponse()
	require.NotNil(t, queryResponsePublication)

	assert.Equal(t, 1, md.getRequestsPerChain(vaa.ChainIDPolygon))
	assert.True(t, validateResponseForTest(t, queryResponsePublication, signedQueryRequest, queryRequest, expectedResults))
}

func TestBatchOfTwoQueriesShouldSucceed(t *testing.T) {
	ctx := context.Background()
	logger, err := zap.NewDevelopment()
	require.NoError(t, err)

	md := createQueryHandlerForTest(t, ctx, logger, watcherChainsForTest)

	// Create the request and the expected results. Give the expected results to the mock.
	perChainQueries := []*common.PerChainQueryRequest{
		createPerChainQueryForTesting(vaa.ChainIDPolygon, "0x28d9630", 2),
		createPerChainQueryForTesting(vaa.ChainIDBSC, "0x28d9123", 3),
	}
	signedQueryRequest, queryRequest := createSignedQueryRequestForTesting(md.sk, perChainQueries)
	expectedResults := createExpectedResultsForTest(queryRequest.PerChainQueries)
	md.setExpectedResults(expectedResults)

	// Submit the query request to the handler.
	md.signedQueryReqWriteC <- signedQueryRequest

	// Wait until we receive a response or timeout.
	queryResponsePublication := md.waitForResponse()
	require.NotNil(t, queryResponsePublication)

	assert.Equal(t, 1, md.getRequestsPerChain(vaa.ChainIDPolygon))
	assert.Equal(t, 1, md.getRequestsPerChain(vaa.ChainIDBSC))
	assert.True(t, validateResponseForTest(t, queryResponsePublication, signedQueryRequest, queryRequest, expectedResults))
}

func TestQueryWithLimitedRetriesShouldSucceed(t *testing.T) {
	ctx := context.Background()
	logger, err := zap.NewDevelopment()
	require.NoError(t, err)

	md := createQueryHandlerForTest(t, ctx, logger, watcherChainsForTest)

	// Create the request and the expected results. Give the expected results to the mock.
	perChainQueries := []*common.PerChainQueryRequest{createPerChainQueryForTesting(vaa.ChainIDPolygon, "0x28d9630", 2)}
	signedQueryRequest, queryRequest := createSignedQueryRequestForTesting(md.sk, perChainQueries)
	expectedResults := createExpectedResultsForTest(queryRequest.PerChainQueries)
	md.setExpectedResults(expectedResults)

	// Make it retry a couple of times, but not enough to make it fail.
	retries := 2
	md.setRetries(vaa.ChainIDPolygon, retries)

	// Submit the query request to the handler.
	md.signedQueryReqWriteC <- signedQueryRequest

	// The request should eventually succeed.
	queryResponsePublication := md.waitForResponse()
	require.NotNil(t, queryResponsePublication)

	assert.Equal(t, retries+1, md.getRequestsPerChain(vaa.ChainIDPolygon))
	assert.True(t, validateResponseForTest(t, queryResponsePublication, signedQueryRequest, queryRequest, expectedResults))
}

func TestQueryWithRetryDueToTimeoutShouldSucceed(t *testing.T) {
	ctx := context.Background()
	logger, err := zap.NewDevelopment()
	require.NoError(t, err)

	md := createQueryHandlerForTest(t, ctx, logger, watcherChainsForTest)

	// Create the request and the expected results. Give the expected results to the mock.
	perChainQueries := []*common.PerChainQueryRequest{createPerChainQueryForTesting(vaa.ChainIDPolygon, "0x28d9630", 2)}
	signedQueryRequest, queryRequest := createSignedQueryRequestForTesting(md.sk, perChainQueries)
	expectedResults := createExpectedResultsForTest(queryRequest.PerChainQueries)
	md.setExpectedResults(expectedResults)

	// Make the first per chain query timeout, but the retry should succeed.
	md.setRetries(vaa.ChainIDPolygon, ignoreQuery)

	// Submit the query request to the handler.
	md.signedQueryReqWriteC <- signedQueryRequest

	// The request should eventually succeed.
	queryResponsePublication := md.waitForResponse()
	require.NotNil(t, queryResponsePublication)

	assert.Equal(t, 2, md.getRequestsPerChain(vaa.ChainIDPolygon))
	assert.True(t, validateResponseForTest(t, queryResponsePublication, signedQueryRequest, queryRequest, expectedResults))
}

func TestQueryWithTooManyRetriesShouldFail(t *testing.T) {
	ctx := context.Background()
	logger, err := zap.NewDevelopment()
	require.NoError(t, err)

	md := createQueryHandlerForTest(t, ctx, logger, watcherChainsForTest)

	// Create the request and the expected results. Give the expected results to the mock.
	perChainQueries := []*common.PerChainQueryRequest{
		createPerChainQueryForTesting(vaa.ChainIDPolygon, "0x28d9630", 2),
		createPerChainQueryForTesting(vaa.ChainIDBSC, "0x28d9123", 3),
	}
	signedQueryRequest, queryRequest := createSignedQueryRequestForTesting(md.sk, perChainQueries)
	expectedResults := createExpectedResultsForTest(queryRequest.PerChainQueries)
	md.setExpectedResults(expectedResults)

	// Make polygon retry a couple of times, but not enough to make it fail.
	retriesForPolygon := 2
	md.setRetries(vaa.ChainIDPolygon, retriesForPolygon)

	// Make BSC retry so many times that the request times out.
	md.setRetries(vaa.ChainIDBSC, 1000)

	// Submit the query request to the handler.
	md.signedQueryReqWriteC <- signedQueryRequest

	// The request should timeout.
	queryResponsePublication := md.waitForResponse()
	require.Nil(t, queryResponsePublication)

	assert.Equal(t, retriesForPolygon+1, md.getRequestsPerChain(vaa.ChainIDPolygon))
}

func TestQueryWithLimitedRetriesOnMultipleChainsShouldSucceed(t *testing.T) {
	ctx := context.Background()
	logger, err := zap.NewDevelopment()
	require.NoError(t, err)

	md := createQueryHandlerForTest(t, ctx, logger, watcherChainsForTest)

	// Create the request and the expected results. Give the expected results to the mock.
	perChainQueries := []*common.PerChainQueryRequest{
		createPerChainQueryForTesting(vaa.ChainIDPolygon, "0x28d9630", 2),
		createPerChainQueryForTesting(vaa.ChainIDBSC, "0x28d9123", 3),
	}
	signedQueryRequest, queryRequest := createSignedQueryRequestForTesting(md.sk, perChainQueries)
	expectedResults := createExpectedResultsForTest(queryRequest.PerChainQueries)
	md.setExpectedResults(expectedResults)

	// Make both chains retry a couple of times, but not enough to make it fail.
	retriesForPolygon := 2
	md.setRetries(vaa.ChainIDPolygon, retriesForPolygon)

	retriesForBSC := 3
	md.setRetries(vaa.ChainIDBSC, retriesForBSC)

	// Submit the query request to the handler.
	md.signedQueryReqWriteC <- signedQueryRequest

	// The request should eventually succeed.
	queryResponsePublication := md.waitForResponse()
	require.NotNil(t, queryResponsePublication)

	assert.Equal(t, retriesForPolygon+1, md.getRequestsPerChain(vaa.ChainIDPolygon))
	assert.Equal(t, retriesForBSC+1, md.getRequestsPerChain(vaa.ChainIDBSC))
	assert.True(t, validateResponseForTest(t, queryResponsePublication, signedQueryRequest, queryRequest, expectedResults))
}

func TestFatalErrorOnPerChainQueryShouldCauseRequestToFail(t *testing.T) {
	ctx := context.Background()
	logger, err := zap.NewDevelopment()
	require.NoError(t, err)

	md := createQueryHandlerForTest(t, ctx, logger, watcherChainsForTest)

	// Create the request and the expected results. Give the expected results to the mock.
	perChainQueries := []*common.PerChainQueryRequest{
		createPerChainQueryForTesting(vaa.ChainIDPolygon, "0x28d9630", 2),
		createPerChainQueryForTesting(vaa.ChainIDBSC, "0x28d9123", 3),
	}
	signedQueryRequest, queryRequest := createSignedQueryRequestForTesting(md.sk, perChainQueries)
	expectedResults := createExpectedResultsForTest(queryRequest.PerChainQueries)
	md.setExpectedResults(expectedResults)

	// Make BSC return a fatal error.
	md.setRetries(vaa.ChainIDBSC, fatalError)

	// Submit the query request to the handler.
	md.signedQueryReqWriteC <- signedQueryRequest

	// The request should timeout.
	queryResponsePublication := md.waitForResponse()
	require.Nil(t, queryResponsePublication)

	assert.Equal(t, 1, md.getRequestsPerChain(vaa.ChainIDPolygon))
	assert.Equal(t, 1, md.getRequestsPerChain(vaa.ChainIDBSC))
}

func TestPublishRetrySucceeds(t *testing.T) {
	ctx := context.Background()
	logger, err := zap.NewDevelopment()
	require.NoError(t, err)

	md := createQueryHandlerForTestWithoutPublisher(t, ctx, logger, watcherChainsForTest)

	// Create the request and the expected results. Give the expected results to the mock.
	perChainQueries := []*common.PerChainQueryRequest{createPerChainQueryForTesting(vaa.ChainIDPolygon, "0x28d9630", 2)}
	signedQueryRequest, queryRequest := createSignedQueryRequestForTesting(md.sk, perChainQueries)
	expectedResults := createExpectedResultsForTest(queryRequest.PerChainQueries)
	md.setExpectedResults(expectedResults)

	// Submit the query request to the handler.
	md.signedQueryReqWriteC <- signedQueryRequest

	// Sleep for a bit before we start listening for published results.
	// If you look in the log, you should see one of these: "failed to publish query response to p2p, will retry publishing next interval"
	// and at least one of these: "resend of query response to p2p failed again, will keep retrying".
	time.Sleep(retryIntervalForTest * 3)

	// Now start the publisher routine.
	// If you look in the log, you should see one of these: "resend of query response to p2p succeeded".
	md.startResponseListener(ctx)

	// The response should still get published.
	queryResponsePublication := md.waitForResponse()
	require.NotNil(t, queryResponsePublication)

	assert.Equal(t, 1, md.getRequestsPerChain(vaa.ChainIDPolygon))
	assert.True(t, validateResponseForTest(t, queryResponsePublication, signedQueryRequest, queryRequest, expectedResults))
}
